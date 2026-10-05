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

func (e *Engine) studentAction(r *req, act actions.Action) (bool, error) {
	ctx, u := r.ctx, r.u
	switch a := act.(type) {
	case actions.PickSubject:
		// права на ученика проверит core на следующем шаге
		subs, err := e.App.Subjects(ctx)
		if err != nil {
			return true, err
		}
		var rows [][]msg.Button
		for _, s := range subs {
			rows = append(rows, msg.Row(msg.Btn(s.Name, actions.PickTutor{StudentID: a.StudentID, SubjectID: s.ID})))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
		r.screen("Какой предмет?", rows...)
		return true, nil

	case actions.PickTutor:
		ts, err := e.App.TutorsForSubject(ctx, u, a.StudentID, a.SubjectID)
		if err != nil {
			return true, err
		}
		back := msg.Row(msg.Btn("⬅️ Назад", actions.PickSubject{StudentID: a.StudentID}))
		if len(ts) == 0 {
			r.screen("По этому предмету пока нет репетиторов.", back)
			return true, nil
		}
		var rows [][]msg.Button
		for _, t := range ts {
			label := t.Tutor.Name
			if t.Enrolled {
				label = "⭐ " + label + " (ваш репетитор)"
			}
			rows = append(rows, msg.Row(msg.Btn(label, actions.TutorCard{StudentID: a.StudentID, SubjectID: a.SubjectID, TutorID: t.Tutor.ID})))
		}
		rows = append(rows, back)
		r.screen("Выберите репетитора. У каждого можно взять один бесплатный пробный урок.", rows...)
		return true, nil

	case actions.TutorCard:
		o, err := e.App.TutorOffer(ctx, u, a.StudentID, a.SubjectID, a.TutorID)
		if err != nil {
			return true, err
		}
		pick := func(mode actions.BookMode) actions.Action {
			return actions.PickDay{TutorID: a.TutorID, SubjectID: a.SubjectID, StudentID: a.StudentID, Mode: mode}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "👩‍🏫 %s\n", o.Tutor.Name)
		if o.Tutor.Bio != "" {
			fmt.Fprintf(&b, "\n%s\n", o.Tutor.Bio)
		}
		fmt.Fprintf(&b, "\nУрок: %d мин · %s", o.Tutor.LessonMinutes, domain.FormatRub(o.Price))
		var rows [][]msg.Button
		switch {
		case o.Enrolled:
			b.WriteString("\n\n⭐ Вы занимаетесь у этого репетитора.")
			rows = append(rows,
				msg.Row(msg.Btn("🔁 Постоянное время (каждую неделю)", pick(actions.ModeRecurring))),
				msg.Row(msg.Btn("📅 Разовый урок", pick(actions.ModeOnce))))
		case o.Liked:
			b.WriteString("\n\nПробный урок понравился — можно закрепиться за репетитором.")
			rows = append(rows, msg.Row(msg.Btn("🤝 Заниматься у этого репетитора",
				actions.Enroll{TutorID: a.TutorID, SubjectID: a.SubjectID, StudentID: a.StudentID})))
		case o.TrialUsed:
			b.WriteString("\n\nПробный урок уже записан или проведён.")
		default:
			rows = append(rows, msg.Row(msg.Btn("🎁 Пробный урок (бесплатно)", pick(actions.ModeTrial))))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", actions.PickTutor{StudentID: a.StudentID, SubjectID: a.SubjectID})))
		r.screen(b.String(), rows...)
		return true, nil

	case actions.PickDay:
		slots, _, err := e.App.FreeSlots(ctx, u, a.TutorID, a.StudentID, 0)
		if err != nil {
			return true, err
		}
		back := msg.Row(msg.Btn("⬅️ Назад", actions.TutorCard{StudentID: a.StudentID, SubjectID: a.SubjectID, TutorID: a.TutorID}))
		if len(slots) == 0 {
			r.screen("Свободных окон в ближайшие две недели нет. Попробуйте позже или выберите другого репетитора.", back)
			return true, nil
		}
		r.screen(modeTitle(a.Mode)+"\nВыберите день:", append(dayRows(slots, r.loc(), func(day actions.Day) actions.Action {
			return actions.PickTime{TutorID: a.TutorID, SubjectID: a.SubjectID, StudentID: a.StudentID, Mode: a.Mode, Day: day}
		}), back)...)
		return true, nil

	case actions.PickTime:
		slots, _, err := e.App.FreeSlots(ctx, u, a.TutorID, a.StudentID, 0)
		if err != nil {
			return true, err
		}
		rows := timeRows(slots, a.Day, r.loc(), func(t time.Time) actions.Action {
			return actions.Book{TutorID: a.TutorID, SubjectID: a.SubjectID, StudentID: a.StudentID, Mode: a.Mode, Start: t.Unix()}
		})
		rows = append(rows, msg.Row(msg.Btn("⬅️ Другой день",
			actions.PickDay{TutorID: a.TutorID, SubjectID: a.SubjectID, StudentID: a.StudentID, Mode: a.Mode})))
		r.screen(modeTitle(a.Mode)+"\nВыберите время:", rows...)
		return true, nil

	case actions.Book:
		start := a.StartTime()
		var err error
		var text string
		switch a.Mode {
		case actions.ModeTrial:
			_, err = e.App.BookTrial(ctx, u, a.StudentID, a.TutorID, a.SubjectID, start)
			text = "✅ Записали на пробный урок: " + domain.FormatDateTime(start, r.loc()) + "\nНапомним утром в день урока и за час до начала."
		case actions.ModeOnce:
			_, err = e.App.BookOnce(ctx, u, a.StudentID, a.TutorID, a.SubjectID, start)
			text = "✅ Записали на урок: " + domain.FormatDateTime(start, r.loc())
		case actions.ModeRecurring:
			var n int
			_, n, err = e.App.BookRecurring(ctx, u, a.StudentID, a.TutorID, a.SubjectID, start)
			lt := start.In(r.loc())
			text = fmt.Sprintf("✅ Постоянное время: каждую неделю — %s, %s.\nСоздано уроков на ближайшие недели: %d",
				domain.WeekdayFull(lt.Weekday()), domain.FormatTime(start, r.loc()), n)
		}
		if err != nil {
			return true, err
		}
		r.screen(text, msg.Row(msg.Btn("📅 Мои уроки", actions.StudentLessons{StudentID: a.StudentID}), msg.Btn("🏠 Меню", actions.Home{})))
		return true, nil

	case actions.Enroll:
		if err := e.App.Enroll(ctx, u, a.StudentID, a.TutorID, a.SubjectID); err != nil {
			return true, err
		}
		pick := func(mode actions.BookMode) actions.Action {
			return actions.PickDay{TutorID: a.TutorID, SubjectID: a.SubjectID, StudentID: a.StudentID, Mode: mode}
		}
		r.screen("🤝 Готово! Теперь можно выбрать постоянное время занятий или записаться на разовый урок.",
			msg.Row(msg.Btn("🔁 Постоянное время", pick(actions.ModeRecurring))),
			msg.Row(msg.Btn("📅 Разовый урок", pick(actions.ModeOnce))),
			msg.Row(msg.Btn("💳 Оплатить занятия", actions.PayOptions{StudentID: a.StudentID, TutorID: a.TutorID})))
		return true, nil

	case actions.Balances:
		ens, err := e.App.Balances(ctx, u, a.StudentID)
		if err != nil {
			return true, err
		}
		if len(ens) == 0 {
			r.screen("Оплата появится после выбора репетитора: сначала пробный урок, затем «Заниматься у этого репетитора».",
				msg.Row(msg.Btn("➕ Записаться", actions.PickSubject{StudentID: a.StudentID})), msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
			return true, nil
		}
		var b strings.Builder
		b.WriteString("💳 Оплаченные уроки:\n")
		var rows [][]msg.Button
		for _, en := range ens {
			fmt.Fprintf(&b, "\n• %s (%s): %d", en.TutorName, en.Subject, en.Balance)
			rows = append(rows, msg.Row(msg.Btn("Оплатить — "+en.TutorName, actions.PayOptions{StudentID: a.StudentID, TutorID: en.TutorID})))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
		r.screen(b.String(), rows...)
		return true, nil

	case actions.PayOptions:
		offers, err := e.App.Offers(ctx, u, a.StudentID, a.TutorID)
		if err != nil {
			return true, err
		}
		var rows [][]msg.Button
		for _, o := range offers {
			label := fmt.Sprintf("%s — %s", o.Product.Name, domain.FormatRub(o.Price))
			if o.Product.DiscountPct > 0 {
				label += fmt.Sprintf(" (−%d%%)", o.Product.DiscountPct)
			}
			rows = append(rows, msg.Row(msg.Btn(label, actions.Purchase{StudentID: a.StudentID, TutorID: a.TutorID, ProductID: o.Product.ID})))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", actions.Balances{StudentID: a.StudentID})))
		r.screen("Выберите вариант оплаты.\n🧪 Сейчас оплата тестовая: деньги не списываются, уроки начисляются сразу.", rows...)
		return true, nil

	case actions.Purchase:
		res, err := e.App.Purchase(ctx, u, a.StudentID, a.TutorID, a.ProductID)
		if err != nil {
			return true, err
		}
		r.screen(res, msg.Row(msg.Btn("🏠 Меню", actions.Home{})))
		return true, nil

	case actions.InviteParent:
		payload, err := e.App.CreateInvite(ctx, u, core.InviteParent)
		if err != nil {
			return true, err
		}
		r.screen("Перешлите эту ссылку родителю — после перехода аккаунты свяжутся. Ссылка одноразовая, действует 7 дней:\n\n"+e.BotLink(payload),
			msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
		return true, nil

	case actions.Feedback:
		f, err := e.App.LeaveFeedback(ctx, u, a.LessonID, a.Liked)
		if err != nil {
			return true, err
		}
		if f.ID == 0 {
			r.screen(f.Text)
			return true, nil
		}
		rows := [][]msg.Button{msg.Row(msg.Btn("💬 Добавить комментарий", actions.FeedbackComment{FeedbackID: f.ID}))}
		if f.Liked {
			rows = append([][]msg.Button{msg.Row(msg.Btn("🤝 Заниматься у этого репетитора",
				actions.Enroll{TutorID: f.TutorID, SubjectID: f.SubjectID, StudentID: f.StudentID}))}, rows...)
		} else {
			rows = append(rows, msg.Row(msg.Btn("🔎 Другие репетиторы", actions.PickTutor{StudentID: f.StudentID, SubjectID: f.SubjectID})))
		}
		r.screen(f.Text, rows...)
		return true, nil

	case actions.FeedbackComment:
		if err := e.ask(r, askFeedbackComment{FeedbackID: a.FeedbackID}); err != nil {
			return true, err
		}
		r.send("Напишите комментарий одним сообщением. Его увидят родители и администратор школы, репетитору он напрямую не передаётся.")
		return true, nil
	}
	return false, nil
}

func modeTitle(mode actions.BookMode) string {
	switch mode {
	case actions.ModeTrial:
		return "🎁 Пробный урок"
	case actions.ModeRecurring:
		return "🔁 Постоянное время — урок будет повторяться каждую неделю в выбранный день и час"
	}
	return "📅 Разовый урок"
}

// dayRows — кнопки дней, в которых есть свободные слоты (по 2 в ряд).
func dayRows(slots []time.Time, loc *time.Location, mk func(actions.Day) actions.Action) [][]msg.Button {
	var rows [][]msg.Button
	var row []msg.Button
	seen := map[actions.Day]bool{}
	for _, s := range slots {
		day := actions.DayOf(s, loc)
		if seen[day] {
			continue
		}
		seen[day] = true
		row = append(row, msg.Btn(domain.FormatDay(s, loc), mk(day)))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// timeRows — кнопки времени выбранного дня (по 4 в ряд).
func timeRows(slots []time.Time, day actions.Day, loc *time.Location, mk func(time.Time) actions.Action) [][]msg.Button {
	var rows [][]msg.Button
	var row []msg.Button
	for _, s := range slots {
		if actions.DayOf(s, loc) != day {
			continue
		}
		row = append(row, msg.Btn(domain.FormatTime(s, loc), mk(s)))
		if len(row) == 4 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}
