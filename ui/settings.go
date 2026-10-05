package ui

import (
	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

// settingsScreen — настройки уведомлений. При первой регистрации (onboarding) пользователю
// показываются значения по умолчанию: каждое можно оставить или поменять одной кнопкой.
func (e *Engine) settingsScreen(r *req, onboarding bool) error {
	st, err := e.App.Settings(r.ctx, r.u)
	if err != nil {
		return err
	}
	text := "⚙️ Настройки напоминаний"
	if onboarding {
		text = "Почти готово! Вот настройки по умолчанию — нажмите на пункт, чтобы изменить, или оставьте как есть:"
	}
	rows := [][]msg.Button{
		msg.Row(msg.Btn("☀️ Утром в день урока: "+onOff(st.RemindMorning)+" — изменить", "stt", string(core.SettingMorning))),
	}
	// за час до урока напоминаем ученику и репетитору; родителю — только утреннее
	if r.role != domain.RoleParent {
		rows = append(rows, msg.Row(msg.Btn("⏰ За час до урока: "+onOff(st.RemindHour)+" — изменить", "stt", string(core.SettingHour))))
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
	switch p.Name {
	case "st":
		return e.settingsScreen(r, false)
	case "stt":
		st, err := e.App.ToggleSetting(r.ctx, r.u, core.SettingKey(p.Str(0)))
		if err != nil {
			return err
		}
		return e.settingsScreen(r, !st.Onboarded)
	case "stok":
		first, err := e.App.FinishOnboarding(r.ctx, r.u)
		if err != nil {
			return err
		}
		if err := e.mainMenu(r); err != nil {
			return err
		}
		if first {
			r.toast("Настройки сохранены")
		}
	}
	return nil
}
