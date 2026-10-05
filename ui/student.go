package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

// Режимы записи: t — пробный, o — разовый, r — постоянное время.
const (
	modeTrial     = "t"
	modeOnce      = "o"
	modeRecurring = "r"
)

func (e *Engine) studentAction(r *req, p msg.Parsed) (bool, error) {
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "nb": // выбор предмета
		studentID := p.Int(0) // права на ученика проверит core на следующем шаге
		subs, err := e.App.Subjects(ctx)
		if err != nil {
			return true, err
		}
		var rows [][]msg.Button
		for _, s := range subs {
			rows = append(rows, msg.Row(msg.Btn(s.Name, "ns", studentID, s.ID)))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "home")))
		r.screen("Какой предмет?", rows...)
		return true, nil

	case "ns": // список репетиторов по предмету
		studentID, subjectID := p.Int(0), int(p.Int(1))
		ts, err := e.App.TutorsForSubject(ctx, u, studentID, subjectID)
		if err != nil {
			return true, err
		}
		if len(ts) == 0 {
			r.screen("По этому предмету пока нет репетиторов.", msg.Row(msg.Btn("⬅️ Назад", "nb", studentID)))
			return true, nil
		}
		var rows [][]msg.Button
		for _, t := range ts {
			label := t.Tutor.Name
			if t.Enrolled {
				label = "⭐ " + label + " (ваш репетитор)"
			}
			rows = append(rows, msg.Row(msg.Btn(label, "tc", studentID, subjectID, t.Tutor.ID)))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "nb", studentID)))
		r.screen("Выберите репетитора. У каждого можно взять один бесплатный пробный урок.", rows...)
		return true, nil

	case "tc": // карточка репетитора
		studentID, subjectID, tutorID := p.Int(0), int(p.Int(1)), p.Int(2)
		o, err := e.App.TutorOffer(ctx, u, studentID, subjectID, tutorID)
		if err != nil {
			return true, err
		}
		t, price, enrolled, liked, trialUsed := o.Tutor, o.Price, o.Enrolled, o.Liked, o.TrialUsed
		var b strings.Builder
		fmt.Fprintf(&b, "👩‍🏫 %s\n", t.Name)
		if t.Bio != "" {
			fmt.Fprintf(&b, "\n%s\n", t.Bio)
		}
		fmt.Fprintf(&b, "\nУрок: %d мин · %s", t.LessonMinutes, domain.FormatRub(price))
		var rows [][]msg.Button
		switch {
		case enrolled:
			b.WriteString("\n\n⭐ Вы занимаетесь у этого репетитора.")
			rows = append(rows,
				msg.Row(msg.Btn("🔁 Постоянное время (каждую неделю)", "bk", tutorID, subjectID, studentID, modeRecurring)),
				msg.Row(msg.Btn("📅 Разовый урок", "bk", tutorID, subjectID, studentID, modeOnce)))
		case liked:
			b.WriteString("\n\nПробный урок понравился — можно закрепиться за репетитором.")
			rows = append(rows, msg.Row(msg.Btn("🤝 Заниматься у этого репетитора", "enr", tutorID, subjectID, studentID)))
		case trialUsed:
			b.WriteString("\n\nПробный урок уже записан или проведён.")
		default:
			rows = append(rows, msg.Row(msg.Btn("🎁 Пробный урок (бесплатно)", "bk", tutorID, subjectID, studentID, modeTrial)))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "ns", studentID, subjectID)))
		r.screen(b.String(), rows...)
		return true, nil

	case "bk": // выбор дня
		tutorID, subjectID, studentID, mode := p.Int(0), int(p.Int(1)), p.Int(2), p.Str(3)
		slots, _, err := e.App.FreeSlots(ctx, u, tutorID, studentID, 0)
		if err != nil {
			return true, err
		}
		back := msg.Row(msg.Btn("⬅️ Назад", "tc", studentID, subjectID, tutorID))
		if len(slots) == 0 {
			r.screen("Свободных окон в ближайшие две недели нет. Попробуйте позже или выберите другого репетитора.", back)
			return true, nil
		}
		r.screen(modeTitle(mode)+"\nВыберите день:", append(dayRows(slots, r.loc(), func(day string) msg.Button {
			return msg.Button{Action: msg.Act("bd", tutorID, subjectID, studentID, mode, day)}
		}), back)...)
		return true, nil

	case "bd": // выбор времени
		tutorID, subjectID, studentID, mode, day := p.Int(0), int(p.Int(1)), p.Int(2), p.Str(3), p.Str(4)
		slots, _, err := e.App.FreeSlots(ctx, u, tutorID, studentID, 0)
		if err != nil {
			return true, err
		}
		rows := timeRows(slots, day, r.loc(), func(t time.Time) string {
			return msg.Act("bt", tutorID, subjectID, studentID, mode, t.Unix())
		})
		rows = append(rows, msg.Row(msg.Btn("⬅️ Другой день", "bk", tutorID, subjectID, studentID, mode)))
		r.screen(modeTitle(mode)+"\nВыберите время:", rows...)
		return true, nil

	case "bt": // запись
		tutorID, subjectID, studentID, mode, start := p.Int(0), int(p.Int(1)), p.Int(2), p.Str(3), time.Unix(p.Int(4), 0)
		var err error
		var text string
		switch mode {
		case modeTrial:
			_, err = e.App.BookTrial(ctx, u, studentID, tutorID, subjectID, start)
			text = "✅ Записали на пробный урок: " + domain.FormatDateTime(start, r.loc()) + "\nНапомним утром в день урока и за час до начала."
		case modeOnce:
			_, err = e.App.BookOnce(ctx, u, studentID, tutorID, subjectID, start)
			text = "✅ Записали на урок: " + domain.FormatDateTime(start, r.loc())
		case modeRecurring:
			var n int
			_, n, err = e.App.BookRecurring(ctx, u, studentID, tutorID, subjectID, start)
			lt := start.In(r.loc())
			text = fmt.Sprintf("✅ Постоянное время: каждую неделю — %s, %s.\nСоздано уроков на ближайшие недели: %d",
				domain.WeekdayFull(lt.Weekday()), domain.FormatTime(start, r.loc()), n)
		}
		if err != nil {
			return true, err
		}
		r.screen(text, msg.Row(msg.Btn("📅 Мои уроки", "ls", studentID), msg.Btn("🏠 Меню", "home")))
		return true, nil

	case "enr":
		tutorID, subjectID, studentID := p.Int(0), int(p.Int(1)), p.Int(2)
		if err := e.App.Enroll(ctx, u, studentID, tutorID, subjectID); err != nil {
			return true, err
		}
		r.screen("🤝 Готово! Теперь можно выбрать постоянное время занятий или записаться на разовый урок.",
			msg.Row(msg.Btn("🔁 Постоянное время", "bk", tutorID, subjectID, studentID, modeRecurring)),
			msg.Row(msg.Btn("📅 Разовый урок", "bk", tutorID, subjectID, studentID, modeOnce)),
			msg.Row(msg.Btn("💳 Оплатить занятия", "pay", studentID, tutorID)))
		return true, nil

	case "py": // баланс по репетиторам
		studentID := p.Int(0)
		ens, err := e.App.Balances(ctx, u, studentID)
		if err != nil {
			return true, err
		}
		if len(ens) == 0 {
			r.screen("Оплата появится после выбора репетитора: сначала пробный урок, затем «Заниматься у этого репетитора».",
				msg.Row(msg.Btn("➕ Записаться", "nb", studentID)), msg.Row(msg.Btn("⬅️ Назад", "home")))
			return true, nil
		}
		var b strings.Builder
		b.WriteString("💳 Оплаченные уроки:\n")
		var rows [][]msg.Button
		for _, en := range ens {
			fmt.Fprintf(&b, "\n• %s (%s): %d", en.TutorName, en.Subject, en.Balance)
			rows = append(rows, msg.Row(msg.Btn("Оплатить — "+en.TutorName, "pay", studentID, en.TutorID)))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "home")))
		r.screen(b.String(), rows...)
		return true, nil

	case "pay": // тарифы
		studentID, tutorID := p.Int(0), p.Int(1)
		offers, err := e.App.Offers(ctx, u, studentID, tutorID)
		if err != nil {
			return true, err
		}
		var rows [][]msg.Button
		for _, o := range offers {
			label := fmt.Sprintf("%s — %s", o.Product.Name, domain.FormatRub(o.Price))
			if o.Product.DiscountPct > 0 {
				label += fmt.Sprintf(" (−%d%%)", o.Product.DiscountPct)
			}
			rows = append(rows, msg.Row(msg.Btn(label, "pp", studentID, tutorID, o.Product.ID)))
		}
		rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "py", studentID)))
		r.screen("Выберите вариант оплаты.\n🧪 Сейчас оплата тестовая: деньги не списываются, уроки начисляются сразу.", rows...)
		return true, nil

	case "pp":
		res, err := e.App.Purchase(ctx, u, p.Int(0), p.Int(1), int(p.Int(2)))
		if err != nil {
			return true, err
		}
		r.screen(res, msg.Row(msg.Btn("🏠 Меню", "home")))
		return true, nil

	case "pinv": // ученик приглашает родителя
		payload, err := e.App.CreateInvite(ctx, u, core.InviteParent)
		if err != nil {
			return true, err
		}
		r.screen("Перешлите эту ссылку родителю — после перехода аккаунты свяжутся. Ссылка одноразовая, действует 7 дней:\n\n"+e.BotLink(payload),
			msg.Row(msg.Btn("⬅️ Назад", "home")))
		return true, nil

	case "fb": // отзыв о пробном
		f, err := e.App.LeaveFeedback(ctx, u, p.Int(0), p.Str(1) == "1")
		if err != nil {
			return true, err
		}
		if f.ID == 0 {
			r.screen(f.Text)
			return true, nil
		}
		rows := [][]msg.Button{msg.Row(msg.Btn("💬 Добавить комментарий", "fbc", f.ID))}
		if f.Liked {
			rows = append([][]msg.Button{msg.Row(msg.Btn("🤝 Заниматься у этого репетитора", "enr", f.TutorID, f.SubjectID, f.StudentID))}, rows...)
		} else {
			rows = append(rows, msg.Row(msg.Btn("🔎 Другие репетиторы", "ns", f.StudentID, f.SubjectID)))
		}
		r.screen(f.Text, rows...)
		return true, nil

	case "fbc":
		if err := e.S.SetState(ctx, u.ID, "fbcomment", map[string]string{"fb": itoa(int(p.Int(0)))}); err != nil {
			return true, err
		}
		r.send("Напишите комментарий одним сообщением. Его увидят родители и администратор школы, репетитору он напрямую не передаётся.")
		return true, nil
	}
	return false, nil
}

func modeTitle(mode string) string {
	switch mode {
	case modeTrial:
		return "🎁 Пробный урок"
	case modeRecurring:
		return "🔁 Постоянное время — урок будет повторяться каждую неделю в выбранный день и час"
	}
	return "📅 Разовый урок"
}

// dayRows — кнопки дней, в которых есть свободные слоты (по 2 в ряд).
func dayRows(slots []time.Time, loc *time.Location, mk func(day string) msg.Button) [][]msg.Button {
	var rows [][]msg.Button
	var row []msg.Button
	seen := map[string]bool{}
	for _, s := range slots {
		key := dayKey(s, loc)
		if seen[key] {
			continue
		}
		seen[key] = true
		b := mk(key)
		b.Text = domain.FormatDay(s, loc)
		row = append(row, b)
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
func timeRows(slots []time.Time, day string, loc *time.Location, action func(time.Time) string) [][]msg.Button {
	var rows [][]msg.Button
	var row []msg.Button
	for _, s := range slots {
		if dayKey(s, loc) != day {
			continue
		}
		row = append(row, msg.Button{Text: domain.FormatTime(s, loc), Action: action(s)})
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
