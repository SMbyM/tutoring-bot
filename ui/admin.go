package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SMbyM/tutoring-bot/actions"
	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) adminAction(r *req, act actions.Action) (bool, error) {
	switch act.(type) {
	case actions.AdminTutors, actions.AdminTutor, actions.AskTutorPrice, actions.ResetTutorPrice, actions.InviteTutor,
		actions.Prices, actions.AskBasePrice, actions.ToggleProduct, actions.AskProduct, actions.FeedbackList,
		actions.ForwardFeedback, actions.LateCancels, actions.Subjects, actions.AskSubject:
	default:
		return false, nil
	}
	// права проверяет core; здесь — только чтобы не задавать вопросы админки не-админу
	if r.role != domain.RoleAdmin {
		return true, domain.ErrNotAllowed
	}
	ctx, u := r.ctx, r.u
	back := msg.Row(msg.Btn("⬅️ Назад", actions.Home{}))
	switch a := act.(type) {
	case actions.AdminTutors:
		ts, err := e.App.Tutors(ctx, u)
		if err != nil {
			return true, err
		}
		var rows [][]msg.Button
		for _, t := range ts {
			rows = append(rows, msg.Row(msg.Btn(t.Name, actions.AdminTutor{TutorID: t.ID})))
		}
		rows = append(rows, msg.Row(msg.Btn("➕ Пригласить репетитора", actions.InviteTutor{})), back)
		text := "👩‍🏫 Репетиторы"
		if len(ts) == 0 {
			text += "\n\nПока никого. Пригласите репетитора ссылкой."
		}
		r.screen(text, rows...)
		return true, nil

	case actions.AdminTutor:
		return true, e.adminTutorCard(r, a.TutorID)

	case actions.AskTutorPrice:
		if err := e.S.SetState(ctx, u.ID, "aprice", map[string]string{"tutor": strconv.FormatInt(a.TutorID, 10)}); err != nil {
			return true, err
		}
		r.screen("Введите цену одного урока этого репетитора в рублях, например 1800.")
		return true, nil

	case actions.ResetTutorPrice:
		if err := e.App.SetTutorPrice(ctx, u, a.TutorID, nil); err != nil {
			return true, err
		}
		return true, e.adminTutorCard(r, a.TutorID)

	case actions.InviteTutor:
		payload, err := e.App.CreateInvite(ctx, u, core.InviteTutor)
		if err != nil {
			return true, err
		}
		r.screen("Отправьте ссылку репетитору. Одноразовая, действует 7 дней:\n\n"+e.BotLink(payload), back)
		return true, nil

	case actions.Prices:
		return true, e.adminPrices(r)

	case actions.AskBasePrice:
		if err := e.S.SetState(ctx, u.ID, "abase", nil); err != nil {
			return true, err
		}
		r.screen("Введите общую цену одного урока в рублях, например 1500.\nУже купленные пакеты и абонементы не изменятся.")
		return true, nil

	case actions.ToggleProduct:
		if err := e.App.ToggleProduct(ctx, u, a.ProductID); err != nil {
			return true, err
		}
		return true, e.adminPrices(r)

	case actions.AskProduct:
		if err := e.S.SetState(ctx, u.ID, "aproduct", nil); err != nil {
			return true, err
		}
		r.screen("Новый тариф одним сообщением:\nНазвание; число уроков; скидка %; срок в днях (0 — бессрочно)\n\nНапример:\nПакет 4 урока; 4; 3; 0\nАбонемент на 2 месяца; 16; 12; 62")
		return true, nil

	case actions.FeedbackList:
		fs, err := e.App.RecentFeedback(ctx, u, 10)
		if err != nil {
			return true, err
		}
		if len(fs) == 0 {
			r.screen("Отзывов пока нет.", back)
			return true, nil
		}
		var b strings.Builder
		b.WriteString("💬 Последние отзывы о пробных:\n")
		var rows [][]msg.Button
		for _, f := range fs {
			mark := "👎"
			if f.Liked {
				mark = "👍"
			}
			fmt.Fprintf(&b, "\n%s %s → %s (%s)", mark, f.StudentName, f.TutorName, f.CreatedAt.In(r.loc()).Format("02.01"))
			if f.Comment != "" {
				fmt.Fprintf(&b, "\n   «%s»", trim(f.Comment, 200))
			}
			if f.Forwarded {
				b.WriteString("\n   ✉️ передано репетитору")
			} else {
				rows = append(rows, msg.Row(msg.Btn(fmt.Sprintf("📨 Сообщить %s про %s", f.TutorName, f.StudentName), actions.ForwardFeedback{FeedbackID: f.ID})))
			}
		}
		rows = append(rows, back)
		r.screen(b.String(), rows...)
		return true, nil

	case actions.ForwardFeedback:
		text, err := e.App.ForwardFeedback(ctx, u, a.FeedbackID)
		if err != nil {
			return true, err
		}
		r.toast(text)
		r.send("✅ " + text)
		return true, nil

	case actions.LateCancels:
		ls, err := e.App.LateCancels(ctx, u, 15)
		if err != nil {
			return true, err
		}
		if len(ls) == 0 {
			r.screen("Поздних отмен и переносов нет.", back)
			return true, nil
		}
		var b strings.Builder
		b.WriteString("⚠️ Поздние отмены и переносы (меньше 12 часов до урока):\n")
		for _, l := range ls {
			fmt.Fprintf(&b, "\n• %s — %s", core.LessonLine(l, r.loc(), true, true), l.Status.Title())
			if l.Reason != "" {
				fmt.Fprintf(&b, "\n   Причина: %s", trim(l.Reason, 150))
			}
		}
		r.screen(b.String(), back)
		return true, nil

	case actions.Subjects:
		subs, err := e.App.Subjects(ctx)
		if err != nil {
			return true, err
		}
		names := make([]string, len(subs))
		for i, s := range subs {
			names[i] = "• " + s.Name
		}
		r.screen("📚 Предметы:\n"+strings.Join(names, "\n"), msg.Row(msg.Btn("➕ Добавить предмет", actions.AskSubject{})), back)
		return true, nil

	case actions.AskSubject:
		if err := e.S.SetState(ctx, u.ID, "asubject", nil); err != nil {
			return true, err
		}
		r.screen("Название нового предмета?")
		return true, nil
	}
	return false, nil
}

func (e *Engine) adminTutorCard(r *req, id int64) error {
	c, err := e.App.AdminTutor(r.ctx, r.u, id)
	if err != nil {
		return err
	}
	t, base := c.Tutor, c.BasePrice
	subs := make([]string, len(t.Subjects))
	for i, s := range t.Subjects {
		subs[i] = s.Name
	}
	price := domain.FormatRub(base) + " (общая)"
	if t.PriceOverride != nil {
		price = domain.FormatRub(*t.PriceOverride) + " (индивидуальная)"
	}
	text := fmt.Sprintf("👩‍🏫 %s\nПредметы: %s\nУрок: %d мин, %s\nОкон в неделю: %d\nУчеников: %d",
		t.Name, strings.Join(subs, ", "), t.LessonMinutes, price, c.Windows, c.Students)
	rows := [][]msg.Button{msg.Row(msg.Btn("💰 Индивидуальная цена", actions.AskTutorPrice{TutorID: id}))}
	if t.PriceOverride != nil {
		rows = append(rows, msg.Row(msg.Btn("↩️ Вернуть общую цену", actions.ResetTutorPrice{TutorID: id})))
	}
	rows = append(rows, msg.Row(msg.Btn("⬅️ К списку", actions.AdminTutors{})))
	r.screen(text, rows...)
	return nil
}

func (e *Engine) adminPrices(r *req) error {
	pl, err := e.App.Prices(r.ctx, r.u)
	if err != nil {
		return err
	}
	base, ps := pl.BasePrice, pl.Products
	var b strings.Builder
	fmt.Fprintf(&b, "💰 Общая цена урока: %s\n\nТарифы (по общей цене):", domain.FormatRub(base))
	rows := [][]msg.Button{msg.Row(msg.Btn("✏️ Изменить общую цену", actions.AskBasePrice{}))}
	for _, p := range ps {
		state := "✅"
		if !p.Active {
			state = "⛔️"
		}
		fmt.Fprintf(&b, "\n%s %s — %d ур., скидка %d%% → %s", state, p.Name, p.Lessons, p.DiscountPct, domain.FormatRub(domain.PriceOf(p, base)))
		if p.ValidDays > 0 {
			fmt.Fprintf(&b, ", %d дн.", p.ValidDays)
		}
		toggle := "Скрыть"
		if !p.Active {
			toggle = "Показать"
		}
		rows = append(rows, msg.Row(msg.Btn(toggle+": "+trim(p.Name, 30), actions.ToggleProduct{ProductID: p.ID})))
	}
	rows = append(rows, msg.Row(msg.Btn("➕ Новый тариф", actions.AskProduct{})), msg.Row(msg.Btn("⬅️ Назад", actions.Home{})))
	r.screen(b.String(), rows...)
	return nil
}

func (e *Engine) adminText(r *req, state string, data map[string]string, text string) (bool, error) {
	switch state {
	case "aprice", "abase", "aproduct", "asubject":
	default:
		return false, nil
	}
	ctx, u := r.ctx, r.u
	switch state {
	case "aprice":
		v, err := domain.ParseRub(text)
		if err != nil {
			return true, uerr(err)
		}
		id := atoi64(data["tutor"])
		if err := e.App.SetTutorPrice(ctx, u, id, &v); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.adminTutorCard(r, id)
	case "abase":
		v, err := domain.ParseRub(text)
		if err != nil {
			return true, uerr(err)
		}
		if err := e.App.SetBasePrice(ctx, u, v); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.adminPrices(r)
	case "aproduct":
		p, err := parseProduct(text)
		if err != nil {
			return true, uerr(err)
		}
		if err := e.App.AddProduct(ctx, u, p); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.adminPrices(r)
	case "asubject":
		if err := e.App.AddSubject(ctx, u, text); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		_, err := e.adminAction(r, actions.Subjects{})
		return true, err
	}
	return true, nil
}

// parseProduct: «Название; уроков; скидка; дней».
func parseProduct(text string) (domain.Product, error) {
	f := strings.Split(text, ";")
	if len(f) != 4 {
		return domain.Product{}, errors.New("нужно 4 поля через «;»: название; уроков; скидка %; дней")
	}
	name := strings.TrimSpace(f[0])
	nums := make([]int, 3)
	for i, s := range f[1:] {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")))
		if err != nil || n < 0 {
			return domain.Product{}, fmt.Errorf("поле %d должно быть числом", i+2)
		}
		nums[i] = n
	}
	// допустимость значений и вид тарифа определяет core.AddProduct
	return domain.Product{Name: name, Lessons: nums[0], DiscountPct: nums[1], ValidDays: nums[2]}, nil
}
