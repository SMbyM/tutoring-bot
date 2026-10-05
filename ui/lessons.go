package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/actions"
	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) lessonAction(r *req, act actions.Action) (bool, error) {
	ctx, u := r.ctx, r.u
	switch a := act.(type) {
	case actions.StudentLessons:
		ls, err := e.App.StudentLessons(ctx, u, a.StudentID, 12)
		if err != nil {
			return true, err
		}
		return true, e.lessonList(r, ls, true, false,
			msg.Row(msg.Btn("➕ Записаться", actions.PickSubject{StudentID: a.StudentID}), msg.Btn("⬅️ Назад", actions.Home{})))

	case actions.TutorLessons:
		ls, err := e.App.TutorLessons(ctx, u, 20)
		if err != nil {
			return true, err
		}
		return true, e.lessonList(r, ls, false, true, msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))

	case actions.OpenLesson:
		l, err := e.App.Lesson(ctx, u, a.LessonID)
		if err != nil {
			return true, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n%s\nРепетитор: %s\nУченик: %s\nСтатус: %s", l.Subject, domain.FormatDateTime(l.StartsAt, r.loc()),
			l.TutorName, l.StudentName, l.Status.Title())
		if l.Kind == domain.KindTrial {
			b.WriteString("\nПробный урок (бесплатно)")
		}
		if l.RecurringID != 0 {
			b.WriteString("\n🔁 Постоянное время")
		}
		if domain.IsLate(e.App.Now(), l.StartsAt) {
			b.WriteString("\n\nДо урока меньше 12 часов: перенос и отмена возможны, но нужно указать причину.")
		}
		var rows [][]msg.Button
		if l.Status == domain.StatusScheduled {
			rows = append(rows, msg.Row(msg.Btn("🔄 Перенести", actions.RescheduleDay{LessonID: l.ID}), msg.Btn("🚫 Отменить", actions.AskCancel{LessonID: l.ID})))
			if l.RecurringID != 0 {
				rows = append(rows, msg.Row(msg.Btn("🗓 Отказаться от постоянного времени", actions.StopRecurring{SlotID: l.RecurringID})))
			}
		}
		var back actions.Action = actions.StudentLessons{StudentID: l.StudentID}
		if u.ID == l.TutorID {
			back = actions.TutorLessons{}
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ К урокам", back)))
		r.screen(b.String(), rows...)
		return true, nil

	case actions.RescheduleDay:
		l, err := e.App.Lesson(ctx, u, a.LessonID)
		if err != nil {
			return true, err
		}
		slots, _, err := e.App.FreeSlots(ctx, u, l.TutorID, l.StudentID, l.ID)
		if err != nil {
			return true, err
		}
		back := msg.Row(msg.Btn("⬅️ Назад", actions.OpenLesson{LessonID: l.ID}))
		if len(slots) == 0 {
			r.screen("У репетитора нет свободных окон в ближайшие две недели.", back)
			return true, nil
		}
		r.screen("Перенос урока "+domain.FormatDateTime(l.StartsAt, r.loc())+"\nВыберите новый день:", append(dayRows(slots, r.loc(), func(day actions.Day) actions.Action {
			return actions.RescheduleTime{LessonID: l.ID, Day: day}
		}), back)...)
		return true, nil

	case actions.RescheduleTime:
		l, err := e.App.Lesson(ctx, u, a.LessonID)
		if err != nil {
			return true, err
		}
		slots, _, err := e.App.FreeSlots(ctx, u, l.TutorID, l.StudentID, l.ID)
		if err != nil {
			return true, err
		}
		rows := timeRows(slots, a.Day, r.loc(), func(t time.Time) actions.Action { return actions.Reschedule{LessonID: l.ID, Start: t.Unix()} })
		rows = append(rows, msg.Row(msg.Btn("⬅️ Другой день", actions.RescheduleDay{LessonID: l.ID})))
		r.screen("Выберите новое время:", rows...)
		return true, nil

	case actions.Reschedule:
		return true, e.applyChange(r, a.LessonID, core.ChangeReschedule, a.StartTime(), "")

	case actions.AskCancel:
		l, err := e.App.Lesson(ctx, u, a.LessonID)
		if err != nil {
			return true, err
		}
		r.screen("Отменить урок?\n"+core.LessonLine(l, r.loc(), true, true),
			msg.Row(msg.Btn("Да, отменить", actions.Cancel{LessonID: l.ID}), msg.Btn("Нет", actions.OpenLesson{LessonID: l.ID})))
		return true, nil

	case actions.Cancel:
		return true, e.applyChange(r, a.LessonID, core.ChangeCancel, time.Time{}, "")

	case actions.StopRecurring:
		n, err := e.App.StopRecurring(ctx, u, a.SlotID)
		if err != nil {
			return true, err
		}
		r.screen(fmt.Sprintf("Постоянное время отменено. Отменено будущих уроков: %d", n), msg.Row(msg.Btn("🏠 Меню", actions.Home{})))
		return true, nil

	case actions.DecideRequest:
		text, err := e.App.DecideRequest(ctx, u, a.RequestID, a.Approve)
		if err != nil {
			return true, err
		}
		r.screen(text)
		return true, nil

	case actions.MarkLesson:
		text, err := e.App.MarkLesson(ctx, u, a.LessonID, string(a.Mark))
		if err != nil {
			return true, err
		}
		r.screen(text)
		return true, nil
	}
	return false, nil
}

func (e *Engine) lessonList(r *req, ls []domain.Lesson, withTutor, withStudent bool, footer []msg.Button) error {
	if len(ls) == 0 {
		r.screen("Ближайших уроков нет.", footer)
		return nil
	}
	var rows [][]msg.Button
	for _, l := range ls {
		rows = append(rows, msg.Row(msg.Btn(trim(core.LessonLine(l, r.loc(), withTutor, withStudent), 60), actions.OpenLesson{LessonID: l.ID})))
	}
	rows = append(rows, footer)
	r.screen("📅 Ближайшие уроки (нажмите, чтобы перенести или отменить):", rows...)
	return nil
}

// applyChange — общий хвост переноса/отмены: просит причину для поздних изменений.
func (e *Engine) applyChange(r *req, lessonID int64, kind core.ChangeKind, start time.Time, reason string) error {
	oc, err := e.App.RequestChange(r.ctx, r.u, lessonID, kind, start, reason)
	if err != nil {
		return err
	}
	switch oc {
	case core.NeedReason:
		q := askReason{LessonID: lessonID, Kind: kind}
		if !start.IsZero() {
			q.Start = start.Unix()
		}
		if err := e.ask(r, q); err != nil {
			return err
		}
		r.screen("До урока меньше 12 часов. Напишите коротко причину — её увидят репетитор и администратор.\n(/menu — передумать)")
	case core.PendingApproval:
		r.screen("📨 Запрос отправлен родителю. Как только он подтвердит, урок изменится, а все получат уведомление.",
			msg.Row(msg.Btn("🏠 Меню", actions.Home{})))
	case core.Applied:
		text := "✅ Урок отменён. Репетитор получил уведомление."
		if kind == core.ChangeReschedule {
			text = "✅ Урок перенесён на " + domain.FormatDateTime(start, r.loc()) + ". Все участники получили уведомление."
		}
		r.screen(text, msg.Row(msg.Btn("🏠 Меню", actions.Home{})))
	}
	return nil
}
