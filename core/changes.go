package core

import (
	"context"
	"fmt"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

type Outcome int

const (
	Applied         Outcome = iota // изменение применено
	PendingApproval                // ждём подтверждения родителя
	NeedReason                     // поздняя отмена/перенос — нужна причина
)

type ChangeKind string

const (
	ChangeCancel     ChangeKind = "cancel"
	ChangeReschedule ChangeKind = "reschedule"
)

// RequestChange — отмена или перенос урока с учётом правила 12 часов и приоритета родителя.
// Если урок поздний и причина пустая — возвращает NeedReason, ничего не меняя.
func (a *App) RequestChange(ctx context.Context, u domain.User, lessonID int64, kind ChangeKind, newStart time.Time, reason string) (Outcome, error) {
	l, err := a.S.Lesson(ctx, lessonID)
	if err != nil {
		return 0, err
	}
	if l.Status != domain.StatusScheduled {
		return 0, domain.ErrNotScheduled
	}
	act, err := a.ActorFor(ctx, u, l)
	if err != nil {
		return 0, err
	}
	st, err := a.S.Settings(ctx, l.StudentID)
	if err != nil {
		return 0, err
	}
	parents := a.parentIDs(ctx, l.StudentID)
	decision := domain.DecideChange(act, st.RescheduleNeedsParent, len(parents) > 0)
	if decision == domain.ChangeForbidden {
		return 0, domain.ErrNotAllowed
	}
	late := domain.IsLate(a.Now(), l.StartsAt)
	if late && reason == "" {
		return NeedReason, nil
	}
	if kind == ChangeReschedule {
		// проверяем время заранее, чтобы родитель не одобрял заведомо занятое окно
		if err := a.validateMove(ctx, l, newStart); err != nil {
			return 0, err
		}
	}
	if decision == domain.ChangeNeedsApproval {
		var ns *time.Time
		if kind == ChangeReschedule {
			ns = &newStart
		}
		id, err := a.S.InsertChangeRequest(ctx, store.ChangeRequest{LessonID: l.ID, RequestedBy: u.ID, Kind: string(kind), NewStart: ns, Reason: reason})
		if err != nil {
			return 0, err
		}
		a.askParents(ctx, id, l, kind, newStart, reason, parents)
		return PendingApproval, nil
	}
	return Applied, a.applyChange(ctx, u, l, kind, newStart, reason, late)
}

func (a *App) validateMove(ctx context.Context, l domain.Lesson, start time.Time) error {
	return a.S.Tx(ctx, func(s *store.Store) error {
		t, err := s.Tutor(ctx, l.TutorID)
		if err != nil {
			return err
		}
		return a.checkSlot(ctx, s, t, l.StudentID, start, l.ID)
	})
}

func (a *App) askParents(ctx context.Context, reqID int64, l domain.Lesson, kind ChangeKind, newStart time.Time, reason string, parents []int64) {
	t, _ := a.S.Tutor(ctx, l.TutorID)
	loc := t.Location()
	var what string
	if kind == ChangeCancel {
		what = "отменить урок"
	} else {
		what = "перенести урок на " + domain.FormatDateTime(newStart, loc)
	}
	text := fmt.Sprintf("❓ %s хочет %s:\n%s", l.StudentName, what, LessonLine(l, loc, true, false))
	if reason != "" {
		text += "\nПричина: " + reason
	}
	m := msg.Message{Text: text, Buttons: [][]msg.Button{msg.Row(
		msg.Btn("✅ Разрешить", "cr", reqID, true), msg.Btn("❌ Отклонить", "cr", reqID, false))}}
	a.NotifyMany(ctx, parents, m)
}

// DecideRequest — решение родителя по запросу ребёнка. Кто первый из родителей ответил, тот и решил.
func (a *App) DecideRequest(ctx context.Context, parent domain.User, reqID int64, approve bool) (string, error) {
	r, err := a.S.ChangeRequest(ctx, reqID)
	if err != nil {
		return "", err
	}
	l, err := a.S.Lesson(ctx, r.LessonID)
	if err != nil {
		return "", err
	}
	if ok, err := a.S.IsParentOf(ctx, parent.ID, l.StudentID); err != nil || !ok {
		return "", domain.ErrNotAllowed
	}
	ok, err := a.S.DecideChangeRequest(ctx, reqID, approve, parent.ID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "Этот запрос уже решён.", nil
	}
	if !approve {
		a.Notify(ctx, r.RequestedBy, msg.Text("🙅 Родитель не одобрил изменение урока:\n"+LessonLine(l, domain.LoadLocation(""), true, false)))
		return "Отклонено, ребёнок получит уведомление.", nil
	}
	var ns time.Time
	if r.NewStart != nil {
		ns = *r.NewStart
	}
	late := domain.IsLate(a.Now(), l.StartsAt)
	requester, err := a.S.UserByID(ctx, r.RequestedBy)
	if err != nil {
		return "", err
	}
	if err := a.applyChange(ctx, requester, l, ChangeKind(r.Kind), ns, r.Reason, late); err != nil {
		return "", err
	}
	return "Готово, изменение применено.", nil
}

func (a *App) applyChange(ctx context.Context, u domain.User, l domain.Lesson, kind ChangeKind, newStart time.Time, reason string, late bool) error {
	t, err := a.S.Tutor(ctx, l.TutorID)
	if err != nil {
		return err
	}
	loc := t.Location()
	var text string
	switch kind {
	case ChangeCancel:
		if err := a.S.CancelLesson(ctx, l.ID, u.ID, reason, late); err != nil {
			return err
		}
		text = fmt.Sprintf("🚫 Урок отменён (%s):\n%s", u.Name, LessonLine(l, loc, true, true))
	case ChangeReschedule:
		err := a.S.Tx(ctx, func(s *store.Store) error {
			if err := s.LockTutor(ctx, l.TutorID); err != nil {
				return err
			}
			if err := a.checkSlot(ctx, s, t, l.StudentID, newStart, l.ID); err != nil {
				return err
			}
			return s.MoveLesson(ctx, l.ID, newStart, late, reason)
		})
		if err != nil {
			return err
		}
		text = fmt.Sprintf("🔄 Урок перенесён (%s):\n%s → %s", u.Name, LessonLine(l, loc, true, true), domain.FormatDateTime(newStart, loc))
	default:
		return fmt.Errorf("неизвестное изменение %q", kind)
	}
	if late {
		text += "\n⚠️ Позже чем за 12 часов. Причина: " + reason
	}
	var others []int64
	for _, id := range append([]int64{l.TutorID, l.StudentID}, a.parentIDs(ctx, l.StudentID)...) {
		if id != u.ID {
			others = append(others, id)
		}
	}
	a.NotifyMany(ctx, others, msg.Text(text))
	return nil
}

// StopRecurring — отказ от постоянного слота (будущие уроки по нему отменяются).
func (a *App) StopRecurring(ctx context.Context, u domain.User, slotID int64) (int, error) {
	r, err := a.S.RecurringByID(ctx, slotID)
	if err != nil {
		return 0, err
	}
	if u.ID != r.TutorID && u.EffectiveRole(a.Opt.Debug) != domain.RoleAdmin {
		if err := a.CanActForStudent(ctx, u, r.StudentID); err != nil {
			return 0, err
		}
	}
	n, err := a.S.StopRecurring(ctx, slotID, u.ID, "постоянное расписание отменено")
	if err != nil {
		return 0, err
	}
	st, _ := a.S.UserByID(ctx, r.StudentID)
	var others []int64
	for _, id := range append([]int64{r.TutorID, r.StudentID}, a.parentIDs(ctx, r.StudentID)...) {
		if id != u.ID {
			others = append(others, id)
		}
	}
	a.NotifyMany(ctx, others, msg.Text(fmt.Sprintf("🗓 %s отменил(а) постоянное расписание ученика %s. Отменено будущих уроков: %d", u.Name, st.Name, n)))
	return n, nil
}
