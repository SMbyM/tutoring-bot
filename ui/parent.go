package ui

import (
	"fmt"
	"time"

	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) parentAction(r *req, p msg.Parsed) (bool, error) {
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "kid": // меню ребёнка
		kidID := p.Int(0)
		if err := e.App.CanActForStudent(ctx, u, kidID); err != nil {
			return true, err
		}
		kid, err := e.S.UserByID(ctx, kidID)
		if err != nil {
			return true, err
		}
		st, err := e.S.Settings(ctx, kidID)
		if err != nil {
			return true, err
		}
		mode := "свободно (ребёнок меняет сам)"
		if st.RescheduleNeedsParent {
			mode = "с моего подтверждения"
		}
		text := fmt.Sprintf("👤 %s, %d класс\nПеренос и отмена уроков ребёнком: %s", kid.Name, kid.Grade, mode)
		if kid.ManagedBy != 0 {
			text += "\nАккаунт без Telegram — уведомления приходят вам."
		}
		r.screen(text,
			msg.Row(msg.Btn("📅 Уроки", "ls", kidID), msg.Btn("➕ Записать", "nb", kidID)),
			msg.Row(msg.Btn("💳 Оплата и баланс", "py", kidID)),
			msg.Row(msg.Btn("🔐 Перенос: изменить режим", "kpm", kidID)),
			msg.Row(msg.Btn("⬅️ Назад", "home")))
		return true, nil

	case "kpm": // режим переноса для ребёнка
		kidID := p.Int(0)
		if ok, err := e.S.IsParentOf(ctx, u.ID, kidID); err != nil || !ok {
			return true, errNotParent
		}
		st, err := e.S.Settings(ctx, kidID)
		if err != nil {
			return true, err
		}
		st.RescheduleNeedsParent = !st.RescheduleNeedsParent
		if err := e.S.SaveSettings(ctx, kidID, st); err != nil {
			return true, err
		}
		return e.parentAction(r, msg.Parse(msg.Act("kid", kidID)))

	case "cinv": // родитель приглашает ребёнка с Telegram
		code := newCode()
		if err := e.S.CreateInvite(ctx, storeInvite(code, "child_link", u.ID), 7*24*time.Hour); err != nil {
			return true, err
		}
		r.screen("Отправьте эту ссылку ребёнку — после перехода его аккаунт привяжется к вашему. Одноразовая, действует 7 дней:\n\n"+e.BotLink("c_"+code),
			msg.Row(msg.Btn("⬅️ Назад", "home")))
		return true, nil

	case "kidadd":
		if err := e.S.SetState(ctx, u.ID, "kidname", nil); err != nil {
			return true, err
		}
		r.screen("Как зовут ребёнка? Напишите имя.\nВсе уведомления для него будут приходить вам.")
		return true, nil

	case "kg": // класс ребёнка без Telegram → создаём аккаунт
		state, data, err := e.S.State(ctx, u.ID)
		if err != nil {
			return true, err
		}
		if state != "kidgrade" || data["name"] == "" {
			return true, e.home(r)
		}
		id, err := e.S.CreateManagedChild(ctx, u.ID, data["name"], int(p.Int(0)))
		if err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		r.screen("✅ "+data["name"]+" добавлен(а). Теперь можно записать на пробный урок.",
			msg.Row(msg.Btn("➕ Записать", "nb", id)), msg.Row(msg.Btn("🏠 Меню", "home")))
		return true, nil
	}
	return false, nil
}

var errNotParent = uerr(fmt.Errorf("это может сделать только родитель ребёнка"))
