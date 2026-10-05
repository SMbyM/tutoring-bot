package ui

import (
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
	"github.com/SMbyM/tutoring-bot/internal/msg"
)

// Debug-режим (APP_ENV=debug): админы и репетиторы могут примерять любые роли
// и «проматывать» время урока. В production все эти действия недоступны.

func (e *Engine) debugMenu(r *req) error {
	if !r.u.CanSwitchRoles(e.debug()) {
		r.screen("Режим тестирования недоступен.")
		return nil
	}
	cur := r.role.Title()
	roleBtn := func(role domain.Role) msg.Button {
		label := role.Title()
		if r.role == role {
			label = "✅ " + label
		}
		return msg.Btn(label, "dbr", string(role))
	}
	r.screen("🧪 Тестовый режим. Сейчас вы видите бота как: "+cur+
		"\nНастоящая роль: "+r.u.Role.Title()+". В рабочем режиме это меню отключено.",
		msg.Row(roleBtn(domain.RoleStudent), roleBtn(domain.RoleParent)),
		msg.Row(roleBtn(domain.RoleTutor), roleBtn(domain.RoleAdmin)),
		msg.Row(msg.Btn("↩️ Вернуть свою роль", "dbr", "off")),
		msg.Row(msg.Btn("⏩ Завершить ближайший урок", "dbf")),
		msg.Row(msg.Btn("⚙️ Запустить фоновые задачи", "dbt")),
		msg.Row(msg.Btn("⬅️ В меню", "home")))
	return nil
}

func (e *Engine) debugAction(r *req, p msg.Parsed) (bool, error) {
	switch p.Name {
	case "dbg", "dbr", "dbf", "dbt":
	default:
		return false, nil
	}
	if !r.u.CanSwitchRoles(e.debug()) {
		return true, domain.ErrNotAllowed
	}
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "dbg":
		return true, e.debugMenu(r)
	case "dbr":
		role := domain.Role(p.Str(0))
		if !role.Valid() {
			role = domain.RoleNone
		}
		if err := e.S.SetDebugRole(ctx, u.ID, role); err != nil {
			return true, err
		}
		if role == domain.RoleTutor {
			if err := e.S.EnsureTutor(ctx, u.ID); err != nil {
				return true, err
			}
		}
		r.u.DebugRole = role
		r.role = r.u.EffectiveRole(true)
		r.toast("Роль: " + r.role.Title())
		return true, e.home(r)
	case "dbf":
		ls, err := e.S.UpcomingForStudent(ctx, u.ID, 1)
		if err != nil {
			return true, err
		}
		if len(ls) == 0 {
			if ls, err = e.S.UpcomingForTutor(ctx, u.ID, 1); err != nil {
				return true, err
			}
		}
		if len(ls) == 0 {
			r.screen("Нет запланированных уроков, где вы ученик или репетитор.", msg.Row(msg.Btn("⬅️ Назад", "dbg")))
			return true, nil
		}
		l := ls[0]
		if err := e.S.DebugShiftLesson(ctx, l.ID, e.App.Now().Add(-l.Duration-time.Minute)); err != nil {
			return true, err
		}
		e.App.Tick(ctx)
		r.screen("⏩ Урок «"+l.Subject+"» (ученик "+l.StudentName+") теперь в прошлом. Репетитору отправлен вопрос, состоялся ли урок.",
			msg.Row(msg.Btn("⬅️ Назад", "dbg")))
		return true, nil
	case "dbt":
		e.App.Tick(ctx)
		e.App.DailyTick(ctx)
		r.toast("Фоновые задачи выполнены")
		return true, e.debugMenu(r)
	}
	return false, nil
}
