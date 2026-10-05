package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

func (e *Engine) adminAction(r *req, p msg.Parsed) (bool, error) {
	switch p.Name {
	case "at", "atu", "atp", "atpr", "ainv", "apr", "abp", "aptg", "apadd", "afb", "fwd", "alc", "asub", "asubadd":
	default:
		return false, nil
	}
	if r.role != domain.RoleAdmin {
		return true, domain.ErrNotAllowed
	}
	ctx, u := r.ctx, r.u
	back := msg.Row(msg.Btn("⬅️ Назад", "home"))
	switch p.Name {
	case "at":
		ts, err := e.S.AllTutors(ctx)
		if err != nil {
			return true, err
		}
		var rows [][]msg.Button
		for _, t := range ts {
			rows = append(rows, msg.Row(msg.Btn(t.Name, "atu", t.ID)))
		}
		rows = append(rows, msg.Row(msg.Btn("➕ Пригласить репетитора", "ainv")), back)
		text := "👩‍🏫 Репетиторы"
		if len(ts) == 0 {
			text += "\n\nПока никого. Пригласите репетитора ссылкой."
		}
		r.screen(text, rows...)
		return true, nil

	case "atu":
		return true, e.adminTutorCard(r, p.Int(0))

	case "atp":
		if err := e.S.SetState(ctx, u.ID, "aprice", map[string]string{"tutor": strconv.FormatInt(p.Int(0), 10)}); err != nil {
			return true, err
		}
		r.screen("Введите цену одного урока этого репетитора в рублях, например 1800.")
		return true, nil

	case "atpr":
		if err := e.S.SetTutorPrice(ctx, p.Int(0), nil); err != nil {
			return true, err
		}
		return true, e.adminTutorCard(r, p.Int(0))

	case "ainv":
		code := newCode()
		if err := e.S.CreateInvite(ctx, storeInvite(code, "tutor", u.ID), 7*24*time.Hour); err != nil {
			return true, err
		}
		r.screen("Отправьте ссылку репетитору. Одноразовая, действует 7 дней:\n\n"+e.BotLink("t_"+code), back)
		return true, nil

	case "apr":
		return true, e.adminPrices(r)

	case "abp":
		if err := e.S.SetState(ctx, u.ID, "abase", nil); err != nil {
			return true, err
		}
		r.screen("Введите общую цену одного урока в рублях, например 1500.\nУже купленные пакеты и абонементы не изменятся.")
		return true, nil

	case "aptg":
		if err := e.S.ToggleProduct(ctx, int(p.Int(0))); err != nil {
			return true, err
		}
		return true, e.adminPrices(r)

	case "apadd":
		if err := e.S.SetState(ctx, u.ID, "aproduct", nil); err != nil {
			return true, err
		}
		r.screen("Новый тариф одним сообщением:\nНазвание; число уроков; скидка %; срок в днях (0 — бессрочно)\n\nНапример:\nПакет 4 урока; 4; 3; 0\nАбонемент на 2 месяца; 16; 12; 62")
		return true, nil

	case "afb":
		fs, err := e.S.RecentFeedback(ctx, 10)
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
				rows = append(rows, msg.Row(msg.Btn(fmt.Sprintf("📨 Сообщить %s про %s", f.TutorName, f.StudentName), "fwd", f.ID)))
			}
		}
		rows = append(rows, back)
		r.screen(b.String(), rows...)
		return true, nil

	case "fwd":
		text, err := e.App.ForwardFeedback(ctx, u, p.Int(0))
		if err != nil {
			return true, err
		}
		r.toast(text)
		r.send("✅ " + text)
		return true, nil

	case "alc":
		ls, err := e.S.LateCancels(ctx, 15)
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

	case "asub":
		subs, err := e.S.Subjects(ctx)
		if err != nil {
			return true, err
		}
		names := make([]string, len(subs))
		for i, s := range subs {
			names[i] = "• " + s.Name
		}
		r.screen("📚 Предметы:\n"+strings.Join(names, "\n"), msg.Row(msg.Btn("➕ Добавить предмет", "asubadd")), back)
		return true, nil

	case "asubadd":
		if err := e.S.SetState(ctx, u.ID, "asubject", nil); err != nil {
			return true, err
		}
		r.screen("Название нового предмета?")
		return true, nil
	}
	return false, nil
}

func (e *Engine) adminTutorCard(r *req, id int64) error {
	t, err := e.S.Tutor(r.ctx, id)
	if err != nil {
		return err
	}
	base, err := e.S.BasePrice(r.ctx)
	if err != nil {
		return err
	}
	subs := make([]string, len(t.Subjects))
	for i, s := range t.Subjects {
		subs[i] = s.Name
	}
	price := domain.FormatRub(base) + " (общая)"
	if t.PriceOverride != nil {
		price = domain.FormatRub(*t.PriceOverride) + " (индивидуальная)"
	}
	ws, _ := e.S.Windows(r.ctx, id)
	sts, _ := e.S.TutorStudents(r.ctx, id)
	text := fmt.Sprintf("👩‍🏫 %s\nПредметы: %s\nУрок: %d мин, %s\nОкон в неделю: %d\nУчеников: %d",
		t.Name, strings.Join(subs, ", "), t.LessonMinutes, price, len(ws), len(sts))
	rows := [][]msg.Button{msg.Row(msg.Btn("💰 Индивидуальная цена", "atp", id))}
	if t.PriceOverride != nil {
		rows = append(rows, msg.Row(msg.Btn("↩️ Вернуть общую цену", "atpr", id)))
	}
	rows = append(rows, msg.Row(msg.Btn("⬅️ К списку", "at")))
	r.screen(text, rows...)
	return nil
}

func (e *Engine) adminPrices(r *req) error {
	base, err := e.S.BasePrice(r.ctx)
	if err != nil {
		return err
	}
	ps, err := e.S.Products(r.ctx, false)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "💰 Общая цена урока: %s\n\nТарифы (по общей цене):", domain.FormatRub(base))
	rows := [][]msg.Button{msg.Row(msg.Btn("✏️ Изменить общую цену", "abp"))}
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
		rows = append(rows, msg.Row(msg.Btn(toggle+": "+trim(p.Name, 30), "aptg", p.ID)))
	}
	rows = append(rows, msg.Row(msg.Btn("➕ Новый тариф", "apadd")), msg.Row(msg.Btn("⬅️ Назад", "home")))
	r.screen(b.String(), rows...)
	return nil
}

func (e *Engine) adminText(r *req, state string, data map[string]string, text string) (bool, error) {
	switch state {
	case "aprice", "abase", "aproduct", "asubject":
	default:
		return false, nil
	}
	if r.role != domain.RoleAdmin {
		return true, domain.ErrNotAllowed
	}
	ctx, u := r.ctx, r.u
	switch state {
	case "aprice":
		v, err := domain.ParseRub(text)
		if err != nil {
			return true, uerr(err)
		}
		id := atoi64(data["tutor"])
		if err := e.S.SetTutorPrice(ctx, id, &v); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.adminTutorCard(r, id)
	case "abase":
		v, err := domain.ParseRub(text)
		if err != nil {
			return true, uerr(err)
		}
		if err := e.S.SetBasePrice(ctx, v); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.adminPrices(r)
	case "aproduct":
		p, err := parseProduct(text)
		if err != nil {
			return true, uerr(err)
		}
		if err := e.S.AddProduct(ctx, p); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		return true, e.adminPrices(r)
	case "asubject":
		if err := e.S.AddSubject(ctx, trim(text, 50)); err != nil {
			return true, err
		}
		_ = e.S.ClearState(ctx, u.ID)
		_, err := e.adminAction(r, msg.Parse("asub"))
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
	p := domain.Product{Name: name, Lessons: nums[0], DiscountPct: nums[1], ValidDays: nums[2]}
	if name == "" || p.Lessons < 1 || p.Lessons > 200 || p.DiscountPct > 90 {
		return p, errors.New("проверьте название, число уроков (1–200) и скидку (0–90%)")
	}
	switch {
	case p.ValidDays > 0:
		p.Kind = domain.ProductSubscription
	case p.Lessons == 1:
		p.Kind = domain.ProductSingle
	default:
		p.Kind = domain.ProductPack
	}
	return p, nil
}
