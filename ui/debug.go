package ui

import (
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

// Debug-режим (APP_ENV=debug): админы и репетиторы могут примерять любые роли
// и «проматывать» время урока. Права проверяет core; в production всё отклоняется.

func (e *Engine) debugMenu(r *req) error {
	if !r.u.CanSwitchRoles(e.debug()) {
		r.screen("Режим тестирования недоступен.")
		return nil
	}
	roleBtn := func(role domain.Role) msg.Button {
		label := role.Title()
		if r.role == role {
			label = "✅ " + label
		}
		return msg.Btn(label, "dbr", string(role))
	}
	r.screen("🧪 Тестовый режим. Сейчас вы видите бота как: "+r.role.Title()+
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
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "dbg":
		return true, e.debugMenu(r)
	case "dbr":
		role := domain.Role(p.Str(0))
		if err := e.App.SetDebugRole(ctx, u, role); err != nil {
			return true, err
		}
		if !role.Valid() {
			role = domain.RoleNone
		}
		r.u.DebugRole = role
		r.role = r.u.EffectiveRole(e.debug())
		r.toast("Роль: " + r.role.Title())
		return true, e.home(r)
	case "dbf":
		l, ok, err := e.App.DebugFinishNearest(ctx, u)
		if err != nil {
			return true, err
		}
		if !ok {
			r.screen("Нет запланированных уроков, где вы ученик или репетитор.", msg.Row(msg.Btn("⬅️ Назад", "dbg")))
			return true, nil
		}
		r.screen("⏩ Урок «"+l.Subject+"» (ученик "+l.StudentName+") теперь в прошлом. Репетитору отправлен вопрос, состоялся ли урок.",
			msg.Row(msg.Btn("⬅️ Назад", "dbg")))
		return true, nil
	case "dbt":
		if err := e.App.DebugRunJobs(ctx, u); err != nil {
			return true, err
		}
		r.toast("Фоновые задачи выполнены")
		return true, e.debugMenu(r)
	}
	return false, nil
}
