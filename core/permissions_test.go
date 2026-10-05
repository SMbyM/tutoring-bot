package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
)

// Права проверяет core: любой адаптер (Telegram, Mini App, будущий сайт) получает один и тот же отказ.
func TestPermissionsEnforcedInCore(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.app
	ws := []domain.Window{{Weekday: time.Monday, StartMin: 600, EndMin: 700}}
	price := int64(100000)
	slots, _, err := a.FreeSlots(ctx, e.kid, e.tutor.ID, e.kid.ID, 0)
	must(t, err)
	trial, err := bookLesson(a, ctx, core.BookTrial, e.kid, e.kid.ID, e.tutor.ID, e.math, slots[0])
	must(t, err)

	cases := []struct {
		name string
		call func() error
	}{
		{"ученик не меняет окна репетитора", func() error { return a.SetWindows(ctx, e.kid, ws) }},
		{"родитель не меняет длительность урока", func() error { return a.SetLessonMinutes(ctx, e.mom, 60) }},
		{"репетитор не меняет общую цену", func() error { return a.SetBasePrice(ctx, e.tutor, price) }},
		{"ученик не ставит цену репетитору", func() error { return a.SetTutorPrice(ctx, e.kid, e.tutor.ID, &price) }},
		{"репетитор не добавляет тариф", func() error { return a.AddProduct(ctx, e.tutor, domain.Product{Name: "x", Lessons: 1}) }},
		{"ученик не приглашает репетиторов", func() error { _, err := a.CreateInvite(ctx, e.kid, core.InviteTutor); return err }},
		{"родитель не приглашает родителя", func() error { _, err := a.CreateInvite(ctx, e.mom, core.InviteParent); return err }},
		{"нельзя самому стать репетитором", func() error { return a.ChooseRole(ctx, domain.User{ID: e.kid.ID}, domain.RoleTutor) }},
		{"чужой ребёнок: режим переноса", func() error { return a.ToggleChildApproval(ctx, e.mom, e.kid2.ID) }},
		{"чужой ребёнок: карточка", func() error { _, err := a.Child(ctx, e.mom, e.kid2.ID); return err }},
		{"чужие свободные окна", func() error { _, _, err := a.FreeSlots(ctx, e.kid2, e.tutor.ID, e.kid.ID, 0); return err }},
		{"чужой урок", func() error { _, err := a.Lesson(ctx, e.kid2, trial.ID); return err }},
		{"чужие уроки ученика", func() error { _, err := a.StudentLessons(ctx, e.kid2, e.kid.ID, 5); return err }},
		{"чужой баланс", func() error { _, err := a.Balances(ctx, e.kid2, e.kid.ID); return err }},
		{"ученик не видит отзывы", func() error { _, err := a.RecentFeedback(ctx, e.kid, 5); return err }},
		{"ученик не видит учеников репетитора", func() error { _, err := a.MyStudents(ctx, e.kid); return err }},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, domain.ErrNotAllowed) {
			t.Errorf("%s: ожидался отказ в правах, получено %v", c.name, err)
		}
	}

	// а свои данные — можно
	must(t, a.SetWindows(ctx, e.tutor, ws))
	must(t, a.SetBasePrice(ctx, e.admin, price))
	if _, err := a.Lesson(ctx, e.mom, trial.ID); err != nil {
		t.Errorf("родитель видит урок своего ребёнка: %v", err)
	}
	if _, err := a.Lesson(ctx, e.tutor, trial.ID); err != nil {
		t.Errorf("репетитор видит свой урок: %v", err)
	}

	// debug-переключение ролей недоступно в production
	prod := core.New(e.s, core.Options{Debug: false})
	if err := prod.SetDebugRole(ctx, e.tutor, domain.RoleStudent); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("в production роли не переключаются: %v", err)
	}
}

func TestInputValidatedInCore(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var ie domain.InputError
	for name, err := range map[string]error{
		"пересекающиеся окна": e.app.SetWindows(ctx, e.tutor, []domain.Window{{Weekday: 1, StartMin: 600, EndMin: 700}, {Weekday: 1, StartMin: 650, EndMin: 800}}),
		"урок 5 минут":        e.app.SetLessonMinutes(ctx, e.tutor, 5),
		"пустое имя":          e.app.SetName(ctx, e.kid, "   "),
		"12 класс":            e.app.SetGrade(ctx, e.kid, 12),
		"тариф на 0 уроков":   e.app.AddProduct(ctx, e.admin, domain.Product{Name: "x", Lessons: 0}),
		"отрицательная цена":  e.app.SetBasePrice(ctx, e.admin, -1),
	} {
		if !errors.As(err, &ie) {
			t.Errorf("%s: ожидалась ошибка ввода, получено %v", name, err)
		}
	}
}
