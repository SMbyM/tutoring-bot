package ui

import (
	"fmt"

	"github.com/SMbyM/tutoring-bot/actions"
	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) parentAction(r *req, act actions.Action) (bool, error) {
	ctx, u := r.ctx, r.u
	switch a := act.(type) {
	case actions.Child:
		c, err := e.App.Child(ctx, u, a.KidID)
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
			msg.Row(msg.Btn("📅 Уроки", actions.StudentLessons{StudentID: kidID}), msg.Btn("➕ Записать", actions.PickSubject{StudentID: kidID})),
			msg.Row(msg.Btn("💳 Оплата и баланс", actions.Balances{StudentID: kidID})),
			msg.Row(msg.Btn("🔐 Перенос: изменить режим", actions.ToggleChildApproval{KidID: kidID})),
			msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
		return true, nil

	case actions.ToggleChildApproval:
		if err := e.App.ToggleChildApproval(ctx, u, a.KidID); err != nil {
			return true, err
		}
		return e.parentAction(r, actions.Child{KidID: a.KidID})

	case actions.InviteChild:
		payload, err := e.App.CreateInvite(ctx, u, core.InviteChild)
		if err != nil {
			return true, err
		}
		r.screen("Отправьте эту ссылку ребёнку — после перехода его аккаунт привяжется к вашему. Одноразовая, действует 7 дней:\n\n"+e.BotLink(payload),
			msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
		return true, nil

	case actions.AddChild:
		if err := e.S.SetState(ctx, u.ID, "kidname", nil); err != nil {
			return true, err
		}
		r.screen("Как зовут ребёнка? Напишите имя.\nВсе уведомления для него будут приходить вам.")
		return true, nil

	case actions.ChildGrade: // класс ребёнка без Telegram → создаём аккаунт
		state, data, err := e.S.State(ctx, u.ID)
		if err != nil {
			return true, err
		}
		if state != "kidgrade" || data["name"] == "" {
			return true, e.home(r)
		}
		id, err := e.App.AddManagedChild(ctx, u, data["name"], a.Grade)
		if err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		r.screen("✅ "+data["name"]+" добавлен(а). Теперь можно записать на пробный урок.",
			msg.Row(msg.Btn("➕ Записать", actions.PickSubject{StudentID: id})), msg.Row(msg.Btn("🏠 Меню", actions.Home{})))
		return true, nil
	}
	return false, nil
}
