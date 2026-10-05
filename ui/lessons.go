package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

func storeInvite(code, kind string, by int64) store.Invite {
	return store.Invite{Code: code, Kind: kind, CreatedBy: by}
}

func (e *Engine) lessonAction(r *req, p msg.Parsed) (bool, error) {
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "ls": // уроки ученика
		studentID := p.Int(0)
		if err := e.App.CanActForStudent(ctx, u, studentID); err != nil {
			return true, err
		}
		ls, err := e.S.UpcomingForStudent(ctx, studentID, 12)
		if err != nil {
			return true, err
		}
		return true, e.lessonList(r, ls, true, false, msg.Row(msg.Btn("➕ Записаться", "nb", studentID), msg.Btn("⬅️ Назад", "home")))

	case "tl": // уроки репетитора
		ls, err := e.S.UpcomingForTutor(ctx, u.ID, 20)
		if err != nil {
			return true, err
		}
		return true, e.lessonList(r, ls, false, true, msg.Row(msg.Btn("⬅️ Назад", "home")))

	case "lo": // карточка урока
		l, err := e.S.Lesson(ctx, p.Int(0))
		if err != nil {
			return true, err
		}
		if err := e.canSeeLesson(r, l); err != nil {
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
			rows = append(rows, msg.Row(msg.Btn("🔄 Перенести", "rs", l.ID), msg.Btn("🚫 Отменить", "cx", l.ID)))
			if l.RecurringID != 0 {
				rows = append(rows, msg.Row(msg.Btn("🗓 Отказаться от постоянного времени", "rx", l.RecurringID)))
			}
		}
		back := msg.Btn("⬅️ К урокам", "ls", l.StudentID)
		if u.ID == l.TutorID {
			back = msg.Btn("⬅️ К урокам", "tl")
		}
		rows = append(rows, msg.Row(back))
		r.screen(b.String(), rows...)
		return true, nil

	case "rs": // перенос: выбор дня
		l, err := e.S.Lesson(ctx, p.Int(0))
		if err != nil {
			return true, err
		}
		if err := e.canSeeLesson(r, l); err != nil {
			return true, err
		}
		slots, _, err := e.App.FreeSlots(ctx, l.TutorID, l.StudentID, l.ID)
		if err != nil {
			return true, err
		}
		back := msg.Row(msg.Btn("⬅️ Назад", "lo", l.ID))
		if len(slots) == 0 {
			r.screen("У репетитора нет свободных окон в ближайшие две недели.", back)
			return true, nil
		}
		r.screen("Перенос урока "+domain.FormatDateTime(l.StartsAt, r.loc())+"\nВыберите новый день:", append(dayRows(slots, r.loc(), func(day string) msg.Button {
			return msg.Button{Action: msg.Act("rd", l.ID, day)}
		}), back)...)
		return true, nil

	case "rd":
		l, err := e.S.Lesson(ctx, p.Int(0))
		if err != nil {
			return true, err
		}
		slots, _, err := e.App.FreeSlots(ctx, l.TutorID, l.StudentID, l.ID)
		if err != nil {
			return true, err
		}
		rows := timeRows(slots, p.Str(1), r.loc(), func(t time.Time) string { return msg.Act("rt", l.ID, t.Unix()) })
		rows = append(rows, msg.Row(msg.Btn("⬅️ Другой день", "rs", l.ID)))
		r.screen("Выберите новое время:", rows...)
		return true, nil

	case "rt":
		return true, e.applyChange(r, p.Int(0), core.ChangeReschedule, time.Unix(p.Int(1), 0), "")

	case "cx":
		l, err := e.S.Lesson(ctx, p.Int(0))
		if err != nil {
			return true, err
		}
		r.screen("Отменить урок?\n"+core.LessonLine(l, r.loc(), true, true),
			msg.Row(msg.Btn("Да, отменить", "cxy", l.ID), msg.Btn("Нет", "lo", l.ID)))
		return true, nil

	case "cxy":
		return true, e.applyChange(r, p.Int(0), core.ChangeCancel, time.Time{}, "")

	case "rx":
		n, err := e.App.StopRecurring(ctx, u, p.Int(0))
		if err != nil {
			return true, err
		}
		r.screen(fmt.Sprintf("Постоянное время отменено. Отменено будущих уроков: %d", n), msg.Row(msg.Btn("🏠 Меню", "home")))
		return true, nil

	case "cr": // решение родителя
		text, err := e.App.DecideRequest(ctx, u, p.Int(0), p.Str(1) == "1")
		if err != nil {
			return true, err
		}
		r.screen(text)
		return true, nil

	case "mk": // отметка репетитора
		text, err := e.App.MarkLesson(ctx, u, p.Int(0), p.Str(1))
		if err != nil {
			return true, err
		}
		r.screen(text)
		return true, nil
	}
	return false, nil
}

func (e *Engine) canSeeLesson(r *req, l domain.Lesson) error {
	if r.u.ID == l.TutorID || r.role == domain.RoleAdmin {
		return nil
	}
	return e.App.CanActForStudent(r.ctx, r.u, l.StudentID)
}

func (e *Engine) lessonList(r *req, ls []domain.Lesson, withTutor, withStudent bool, footer []msg.Button) error {
	if len(ls) == 0 {
		r.screen("Ближайших уроков нет.", footer)
		return nil
	}
	var rows [][]msg.Button
	for _, l := range ls {
		rows = append(rows, msg.Row(msg.Btn(trim(core.LessonLine(l, r.loc(), withTutor, withStudent), 60), "lo", l.ID)))
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
		data := map[string]string{"lesson": fmt.Sprint(lessonID), "kind": string(kind)}
		if !start.IsZero() {
			data["start"] = fmt.Sprint(start.Unix())
		}
		if err := e.S.SetState(r.ctx, r.u.ID, "reason", data); err != nil {
			return err
		}
		r.screen("До урока меньше 12 часов. Напишите коротко причину — её увидят репетитор и администратор.\n(/menu — передумать)")
	case core.PendingApproval:
		r.screen("📨 Запрос отправлен родителю. Как только он подтвердит, урок изменится, а все получат уведомление.",
			msg.Row(msg.Btn("🏠 Меню", "home")))
	case core.Applied:
		text := "✅ Урок отменён. Репетитор получил уведомление."
		if kind == core.ChangeReschedule {
			text = "✅ Урок перенесён на " + domain.FormatDateTime(start, r.loc()) + ". Все участники получили уведомление."
		}
		r.screen(text, msg.Row(msg.Btn("🏠 Меню", "home")))
	}
	return nil
}
