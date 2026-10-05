package ui

import (
	"fmt"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) parentAction(r *req, p msg.Parsed) (bool, error) {
	ctx, u := r.ctx, r.u
	switch p.Name {
	case "kid": // меню ребёнка
		c, err := e.App.Child(ctx, u, p.Int(0))
		if err != nil {
			return true, err
		}
		mode := "свободно (ребёнок меняет сам)"
		if c.NeedsApproval {
			mode = "с моего подтверждения"
		}
		text := fmt.Sprintf("👤 %s, %d класс\nПеренос и отмена уроков ребёнком: %s", c.Kid.Name, c.Kid.Grade, mode)
		if c.Kid.ManagedBy != 0 {
			text += "\nАккаунт без Telegram — уведомления приходят вам."
		}
		kidID := c.Kid.ID
		r.screen(text,
			msg.Row(msg.Btn("📅 Уроки", "ls", kidID), msg.Btn("➕ Записать", "nb", kidID)),
			msg.Row(msg.Btn("💳 Оплата и баланс", "py", kidID)),
			msg.Row(msg.Btn("🔐 Перенос: изменить режим", "kpm", kidID)),
			msg.Row(msg.Btn("⬅️ Назад", "home")))
		return true, nil

	case "kpm": // режим переноса для ребёнка
		if err := e.App.ToggleChildApproval(ctx, u, p.Int(0)); err != nil {
			return true, err
		}
		return e.parentAction(r, msg.Parse(msg.Act("kid", p.Int(0))))

	case "cinv": // родитель приглашает ребёнка с Telegram
		payload, err := e.App.CreateInvite(ctx, u, core.InviteChild)
		if err != nil {
			return true, err
		}
		r.screen("Отправьте эту ссылку ребёнку — после перехода его аккаунт привяжется к вашему. Одноразовая, действует 7 дней:\n\n"+e.BotLink(payload),
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
		id, err := e.App.AddManagedChild(ctx, u, data["name"], int(p.Int(0)))
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
