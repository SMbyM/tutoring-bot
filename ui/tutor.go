package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) requireTutor(r *req) error {
	if r.role != domain.RoleTutor {
		return domain.ErrNotAllowed
	}
	return e.S.EnsureTutor(r.ctx, r.u.ID)
}

// tutorWindows — недельные окна, исключения и длительность урока.
func (e *Engine) tutorWindows(r *req) error {
	if err := e.requireTutor(r); err != nil {
		return err
	}
	ws, err := e.S.Windows(r.ctx, r.u.ID)
	if err != nil {
		return err
	}
	ex, err := e.S.Exceptions(r.ctx, r.u.ID, r.loc())
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("🕒 Окна для записи (каждую неделю):\n")
	if len(ws) == 0 {
		b.WriteString("пока не заданы — ученики не смогут записаться")
	} else {
		b.WriteString(domain.FormatWindows(ws))
	}
	var rows [][]msg.Button
	if e.MiniAppURL != "" {
		rows = append(rows, msg.Row(msg.Button{Text: "🖥 Открыть редактор расписания", WebApp: e.MiniAppURL + "/app/"}))
	}
	rows = append(rows,
		msg.Row(msg.Btn("✏️ Задать текстом", "twt")),
		msg.Row(msg.Btn("🏖 Добавить отпуск / перерыв", "twe")))
	if len(ex) > 0 {
		b.WriteString("\n\n🏖 Недоступен:")
		for _, x := range ex {
			span := x.From.Format("02.01")
			if !x.To.Equal(x.From) {
				span += "–" + x.To.Format("02.01")
			}
			fmt.Fprintf(&b, "\n• %s %s", span, x.Note)
			rows = append(rows, msg.Row(msg.Btn("❌ Убрать "+span, "twx", x.ID)))
		}
	}
	rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "home")))
	r.screen(b.String(), rows...)
	return nil
}

func (e *Engine) tutorProfile(r *req) error {
	t, err := e.S.Tutor(r.ctx, r.u.ID)
	if err != nil {
		return err
	}
	subs, err := e.S.Subjects(r.ctx)
	if err != nil {
		return err
	}
	has := map[int]bool{}
	for _, s := range t.Subjects {
		has[s.ID] = true
	}
	bio := t.Bio
	if bio == "" {
		bio = "(не заполнено — расскажите ученикам о себе)"
	}
	text := fmt.Sprintf("👤 %s\n\nО себе: %s\n\nДлительность урока: %d мин", t.Name, bio, t.LessonMinutes)
	rows := [][]msg.Button{msg.Row(msg.Btn("✏️ Изменить «О себе»", "tpb"))}
	var durRow []msg.Button
	for _, m := range []int{45, 60, 90} {
		label := fmt.Sprintf("%d мин", m)
		if m == t.LessonMinutes {
			label = "✅ " + label
		}
		durRow = append(durRow, msg.Btn(label, "tpd", m))
	}
	rows = append(rows, durRow)
	for _, s := range subs {
		label := "◻️ " + s.Name
		if has[s.ID] {
			label = "✅ " + s.Name
		}
		rows = append(rows, msg.Row(msg.Btn(label, "tps", s.ID)))
	}
	rows = append(rows, msg.Row(msg.Btn("⬅️ Назад", "home")))
	r.screen(text, rows...)
	return nil
}

func (e *Engine) tutorAction(r *req, p msg.Parsed) (bool, error) {
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "tw", "twt", "twe", "twx", "tst", "tp", "tpb", "tpd", "tps":
		if err := e.requireTutor(r); err != nil {
			return true, err
		}
	default:
		return false, nil
	}
	switch p.Name {
	case "tw":
		return true, e.tutorWindows(r)
	case "twt":
		ws, _ := e.S.Windows(ctx, u.ID)
		cur := ""
		if len(ws) > 0 {
			cur = "\n\nСейчас:\n" + domain.FormatWindows(ws)
		}
		if err := e.S.SetState(ctx, u.ID, "windows", nil); err != nil {
			return true, err
		}
		r.screen("Отправьте окна одним сообщением, по строке на окно. Это заменит текущее расписание. Например:\n\nпн 15:00-19:00\nср, пт 10:00-13:00\nсб 11:00-15:00" + cur + "\n\n(/menu — отмена)")
		return true, nil
	case "twe":
		if err := e.S.SetState(ctx, u.ID, "exception", nil); err != nil {
			return true, err
		}
		r.screen("Когда вы недоступны? Например:\n\n20.10-26.10 отпуск\n03.11 сессия\n\n(/menu — отмена)")
		return true, nil
	case "twx":
		if err := e.S.DeleteException(ctx, u.ID, p.Int(0)); err != nil {
			return true, err
		}
		return true, e.tutorWindows(r)
	case "tst":
		sts, err := e.S.TutorStudents(ctx, u.ID)
		if err != nil {
			return true, err
		}
		if len(sts) == 0 {
			r.screen("Пока нет закреплённых учеников.", msg.Row(msg.Btn("⬅️ Назад", "home")))
			return true, nil
		}
		var b strings.Builder
		b.WriteString("👥 Ваши ученики (оплачено уроков):\n")
		for _, s := range sts {
			bal, _ := e.S.Balance(ctx, s.ID, u.ID)
			fmt.Fprintf(&b, "\n• %s, %d кл. — %d", s.Name, s.Grade, bal)
		}
		r.screen(b.String(), msg.Row(msg.Btn("⬅️ Назад", "home")))
		return true, nil
	case "tp":
		return true, e.tutorProfile(r)
	case "tpb":
		if err := e.S.SetState(ctx, u.ID, "bio", nil); err != nil {
			return true, err
		}
		r.screen("Напишите пару предложений о себе: опыт, с какими классами работаете, к чему готовите.")
		return true, nil
	case "tpd":
		if err := e.S.SetTutorDuration(ctx, u.ID, int(p.Int(0))); err != nil {
			return true, err
		}
		return true, e.tutorProfile(r)
	case "tps":
		if err := e.S.ToggleTutorSubject(ctx, u.ID, int(p.Int(0))); err != nil {
			return true, err
		}
		return true, e.tutorProfile(r)
	}
	return false, nil
}

func (e *Engine) tutorText(r *req, state string, _ map[string]string, text string) (bool, error) {
	ctx, u := r.ctx, r.u
	switch state {
	case "windows":
		ws, err := domain.ParseWindows(text)
		if err != nil {
			return true, uerr(err)
		}
		if err := e.S.ReplaceWindows(ctx, u.ID, ws); err != nil {
			return true, uerr(err)
		}
		_ = e.S.ClearState(ctx, u.ID)
		r.send("✅ Расписание сохранено. Уже записанные уроки не меняются.")
		return true, e.tutorWindows(r)
	case "exception":
		from, to, note, err := parseException(text, e.App.Now(), r.loc())
		if err != nil {
			return true, uerr(err)
		}
		if err := e.S.AddException(ctx, u.ID, from, to, note); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		r.send("✅ Добавлено. В эти дни записаться к вам не получится. Уже записанные уроки на эти даты перенесите или отмените вручную.")
		return true, e.tutorWindows(r)
	case "bio":
		if err := e.S.SetTutorBio(ctx, u.ID, trim(text, 800)); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.tutorProfile(r)
	}
	return false, nil
}

// parseException: «20.10-26.10 отпуск», «03.11 сессия», «20.10.2026-02.11.2026».
func parseException(text string, now time.Time, loc *time.Location) (time.Time, time.Time, string, error) {
	text = strings.TrimSpace(text)
	span, note, _ := strings.Cut(text, " ")
	parts := strings.Split(span, "-")
	if len(parts) > 2 {
		return time.Time{}, time.Time{}, "", errors.New("пишите даты как 20.10-26.10")
	}
	parse := func(s string) (time.Time, error) {
		f := strings.Split(strings.TrimSpace(s), ".")
		if len(f) < 2 || len(f) > 3 {
			return time.Time{}, fmt.Errorf("дата %q: нужен формат ДД.ММ", s)
		}
		d, err1 := strconv.Atoi(f[0])
		m, err2 := strconv.Atoi(f[1])
		y := now.In(loc).Year()
		var err3 error
		if len(f) == 3 {
			y, err3 = strconv.Atoi(f[2])
		}
		if err1 != nil || err2 != nil || err3 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
			return time.Time{}, fmt.Errorf("дата %q некорректна", s)
		}
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, loc)
		if t.Day() != d {
			return time.Time{}, fmt.Errorf("дата %q некорректна", s)
		}
		// без года и уже прошла — значит, следующий год
		if len(f) == 2 && t.Before(now.In(loc).AddDate(0, 0, -1)) {
			t = t.AddDate(1, 0, 0)
		}
		return t, nil
	}
	from, err := parse(parts[0])
	if err != nil {
		return from, from, "", err
	}
	to := from
	if len(parts) == 2 {
		if to, err = parse(parts[1]); err != nil {
			return from, to, "", err
		}
		if to.Before(from) {
			to = to.AddDate(1, 0, 0)
		}
	}
	return from, to, trim(strings.TrimSpace(note), 100), nil
}
