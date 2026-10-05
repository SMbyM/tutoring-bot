package core

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SMbyM/tutoring-bot/actions"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

// MarkLesson — отметка репетитора после урока. moved=true означает «урок перенесли»:
// он закрывается как отменённый, а ученику предлагается выбрать новое время.
func (a *App) MarkLesson(ctx context.Context, u domain.User, lessonID int64, mark string) (string, error) {
	l, err := a.S.Lesson(ctx, lessonID)
	if err != nil {
		return "", err
	}
	if l.TutorID != u.ID && u.EffectiveRole(a.Opt.Debug) != domain.RoleAdmin {
		return "", domain.ErrNotAllowed
	}
	loc := domain.LoadLocation("")
	if t, err := a.S.Tutor(ctx, l.TutorID); err == nil {
		loc = t.Location()
	}
	switch mark {
	case "held":
		ok, err := a.S.SetStatus(ctx, l.ID, domain.StatusHeld, false)
		if err != nil {
			return "", err
		}
		if !ok {
			return "Урок уже отмечен.", nil
		}
		l.Status = domain.StatusHeld
		a.onHeld(ctx, l)
		return "Отмечено: урок состоялся.", nil
	case "noshow":
		ok, err := a.S.SetStatus(ctx, l.ID, domain.StatusNoShow, false)
		if err != nil {
			return "", err
		}
		if !ok {
			return "Урок уже отмечен.", nil
		}
		a.NotifyMany(ctx, append([]int64{l.StudentID}, a.parentIDs(ctx, l.StudentID)...),
			msg.Text("⚠️ Репетитор отметил, что ученик не пришёл на урок:\n"+LessonLine(l, loc, true, false)))
		return "Отмечено: ученик не пришёл.", nil
	case "moved":
		if err := a.S.CancelLesson(ctx, l.ID, u.ID, "перенесён по договорённости", false); err != nil {
			return "", err
		}
		m := msg.Message{Text: "🔄 Репетитор отметил, что урок перенесён:\n" + LessonLine(l, loc, true, false) +
			"\nВыберите новое время.", Buttons: [][]msg.Button{msg.Row(msg.Btn("📅 Выбрать время", actions.PickDay{TutorID: l.TutorID, SubjectID: l.SubjectID, StudentID: l.StudentID, Mode: actions.ModeOnce}))}}
		a.NotifyMany(ctx, append([]int64{l.StudentID}, a.parentIDs(ctx, l.StudentID)...), m)
		return "Отмечено: перенесён. Ученику предложено выбрать новое время.", nil
	}
	return "", fmt.Errorf("неизвестная отметка %q", mark)
}

// onHeld — последствия проведённого урока: списание с баланса или запрос отзыва о пробном.
func (a *App) onHeld(ctx context.Context, l domain.Lesson) {
	if l.Kind == domain.KindRegular {
		if err := a.S.AddLedger(ctx, store.LedgerEntry{StudentID: l.StudentID, TutorID: l.TutorID, Delta: -1, Reason: "lesson", LessonID: l.ID}); err != nil {
			a.Log.Error("списание урока", "lesson", l.ID, "err", err)
		}
		bal, err := a.S.Balance(ctx, l.StudentID, l.TutorID)
		if err == nil && bal <= 0 {
			a.NotifyMany(ctx, append([]int64{l.StudentID}, a.parentIDs(ctx, l.StudentID)...),
				msg.Message{Text: fmt.Sprintf("💳 Оплаченные уроки у репетитора %s закончились (баланс: %d).", l.TutorName, bal),
					Buttons: [][]msg.Button{msg.Row(msg.Btn("Оплатить", actions.PayOptions{StudentID: l.StudentID, TutorID: l.TutorID}))}})
		}
		return
	}
	text := fmt.Sprintf("Как прошёл пробный урок у репетитора %s (%s)?\nОт ответа зависит доступ к закрытому каналу с материалами.", l.TutorName, l.Subject)
	m := msg.Message{Text: text, Buttons: [][]msg.Button{msg.Row(
		msg.Btn("👍 Понравился", actions.Feedback{LessonID: l.ID, Liked: true}), msg.Btn("👎 Не понравился", actions.Feedback{LessonID: l.ID}))}}
	a.Notify(ctx, l.StudentID, m)
}

// LeaveFeedback — отзыв о пробном. Видят родитель и админ; админ решает, передавать ли репетитору.
// FeedbackResult — итог отзыва: что сказать ученику и что ему можно сделать дальше.
type FeedbackResult struct {
	ID        int64 // 0 — отзыв уже был оставлен раньше
	Text      string
	Liked     bool
	TutorID   int64
	SubjectID int
	StudentID int64
}

func (a *App) LeaveFeedback(ctx context.Context, u domain.User, lessonID int64, liked bool) (FeedbackResult, error) {
	l, err := a.S.Lesson(ctx, lessonID)
	if err != nil {
		return FeedbackResult{}, err
	}
	if l.Kind != domain.KindTrial || l.Status != domain.StatusHeld {
		return FeedbackResult{}, domain.ErrNotAllowed
	}
	if err := a.CanActForStudent(ctx, u, l.StudentID); err != nil {
		return FeedbackResult{}, err
	}
	res := FeedbackResult{Liked: liked, TutorID: l.TutorID, SubjectID: l.SubjectID, StudentID: l.StudentID}
	id, ok, err := a.S.InsertFeedback(ctx, l.ID, l.StudentID, l.TutorID, liked)
	if err != nil {
		return res, err
	}
	if !ok {
		res.Text = "Отзыв на этот урок уже оставлен."
		return res, nil
	}
	res.ID = id
	verdict := map[bool]string{true: "👍 понравился", false: "👎 не понравился"}[liked]
	text := fmt.Sprintf("Отзыв о пробном уроке: %s — репетитор %s (%s): %s", l.StudentName, l.TutorName, l.Subject, verdict)
	var parents []int64
	for _, p := range a.parentIDs(ctx, l.StudentID) {
		if p != u.ID {
			parents = append(parents, p)
		}
	}
	a.NotifyMany(ctx, parents, msg.Text(text))
	a.NotifyMany(ctx, a.adminIDs(ctx), msg.Message{Text: text,
		Buttons: [][]msg.Button{msg.Row(msg.Btn("📨 Сообщить репетитору", actions.ForwardFeedback{FeedbackID: id}))}})
	if liked {
		if err := a.SyncAccess(ctx, l.StudentID); err != nil {
			a.Log.Warn("доступ в канал", "err", err)
		}
		res.Text = "Спасибо! Можно закрепиться за этим репетитором и записаться на постоянные занятия."
		return res, nil
	}
	res.Text = "Спасибо за честный ответ! Можно попробовать другого репетитора."
	return res, nil
}

// SetFeedbackComment — необязательный комментарий к отзыву (видят родители и админ).
// Возвращает отзыв, чтобы адаптер мог предложить следующий шаг (например, закрепиться за репетитором).
func (a *App) SetFeedbackComment(ctx context.Context, u domain.User, fbID int64, comment string) (FeedbackResult, error) {
	f, err := a.S.Feedback(ctx, fbID)
	if err != nil {
		return FeedbackResult{}, err
	}
	if err := a.CanActForStudent(ctx, u, f.StudentID); err != nil {
		return FeedbackResult{}, err
	}
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return FeedbackResult{}, domain.InputError("комментарий пустой")
	}
	if utf8.RuneCountInString(comment) > 1000 {
		comment = string([]rune(comment)[:1000])
	}
	if err := a.S.SetFeedbackComment(ctx, fbID, comment); err != nil {
		return FeedbackResult{}, err
	}
	l, err := a.S.Lesson(ctx, f.LessonID)
	if err != nil {
		return FeedbackResult{}, err
	}
	text := fmt.Sprintf("💬 Комментарий к отзыву %s о репетиторе %s:\n%s", f.StudentName, f.TutorName, comment)
	var parents []int64
	for _, p := range a.parentIDs(ctx, f.StudentID) {
		if p != u.ID {
			parents = append(parents, p)
		}
	}
	a.NotifyMany(ctx, append(parents, a.adminIDs(ctx)...), msg.Text(text))
	return FeedbackResult{ID: f.ID, Liked: f.Liked, TutorID: f.TutorID, SubjectID: l.SubjectID, StudentID: f.StudentID}, nil
}

// ForwardFeedback — админ сообщает репетитору итог пробного (без комментария ученика).
func (a *App) ForwardFeedback(ctx context.Context, admin domain.User, fbID int64) (string, error) {
	if admin.EffectiveRole(a.Opt.Debug) != domain.RoleAdmin {
		return "", domain.ErrNotAllowed
	}
	f, err := a.S.Feedback(ctx, fbID)
	if err != nil {
		return "", err
	}
	ok, err := a.S.MarkForwarded(ctx, fbID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "Уже передано репетитору.", nil
	}
	var text string
	if f.Liked {
		text = fmt.Sprintf("🎉 Ученику %s понравился пробный урок с вами!", f.StudentName)
	} else {
		text = fmt.Sprintf("Ученик %s решил пока не продолжать занятия после пробного урока. Так бывает — дело в подходящем формате, а не в вас.", f.StudentName)
	}
	a.Notify(ctx, f.TutorID, msg.Text(text))
	return "Передано репетитору.", nil
}

// Enroll — ученик закрепляется за репетитором по предмету (нужен понравившийся пробный у него).
func (a *App) Enroll(ctx context.Context, u domain.User, studentID, tutorID int64, subjectID int) error {
	if err := a.CanActForStudent(ctx, u, studentID); err != nil {
		return err
	}
	liked, err := a.S.LikedTutor(ctx, studentID, tutorID)
	if err != nil {
		return err
	}
	if !liked {
		return domain.ErrNotEnrolled
	}
	prev, err := a.S.Enroll(ctx, studentID, tutorID, subjectID)
	if err != nil {
		return err
	}
	st, _ := a.S.UserByID(ctx, studentID)
	a.Notify(ctx, tutorID, msg.Text(fmt.Sprintf("🤝 Ученик %s теперь занимается у вас.", st.Name)))
	if prev != 0 {
		a.Notify(ctx, prev, msg.Text(fmt.Sprintf("Ученик %s перешёл к другому репетитору. Его постоянное расписание у вас отменено.", st.Name)))
	}
	return nil
}
