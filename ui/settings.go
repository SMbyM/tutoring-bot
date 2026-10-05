package ui

import (
	"github.com/SMbyM/tutoring-bot/msg"
)

// settingsScreen — настройки уведомлений. При первой регистрации (onboarding) пользователю
// показываются значения по умолчанию: каждое можно оставить или поменять одной кнопкой.
func (e *Engine) settingsScreen(r *req, onboarding bool) error {
	st, err := e.S.Settings(r.ctx, r.u.ID)
	if err != nil {
		return err
	}
	text := "⚙️ Настройки напоминаний"
	if onboarding {
		text = "Почти готово! Вот настройки по умолчанию — нажмите на пункт, чтобы изменить, или оставьте как есть:"
	}
	rows := [][]msg.Button{
		msg.Row(msg.Btn("☀️ Утром в день урока: "+onOff(st.RemindMorning)+" — изменить", "stt", "m")),
	}
	// за час до урока напоминаем ученику и репетитору; родителю — только утреннее
	if r.role != "parent" {
		rows = append(rows, msg.Row(msg.Btn("⏰ За час до урока: "+onOff(st.RemindHour)+" — изменить", "stt", "h")))
	}
	done := "✅ Готово"
	if onboarding {
		done = "✅ Всё оставить как есть"
	}
	rows = append(rows, msg.Row(msg.Btn(done, "stok")))
	r.screen(text, rows...)
	return nil
}

func (e *Engine) settingsAction(r *req, p msg.Parsed) error {
	st, err := e.S.Settings(r.ctx, r.u.ID)
	if err != nil {
		return err
	}
	switch p.Name {
	case "st":
		return e.settingsScreen(r, false)
	case "stt":
		switch p.Str(0) {
		case "m":
			st.RemindMorning = !st.RemindMorning
		case "h":
			st.RemindHour = !st.RemindHour
		}
		if err := e.S.SaveSettings(r.ctx, r.u.ID, st); err != nil {
			return err
		}
		return e.settingsScreen(r, !st.Onboarded)
	case "stok":
		first := !st.Onboarded
		st.Onboarded = true
		if err := e.S.SaveSettings(r.ctx, r.u.ID, st); err != nil {
			return err
		}
		if first {
			r.toast("Настройки сохранены")
		}
		return e.mainMenu(r)
	}
	return nil
}
