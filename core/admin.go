package core

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store"
)

// Администрирование школы. Все операции — только для админа.

func (a *App) requireAdmin(u domain.User) error {
	if u.EffectiveRole(a.Opt.Debug) != domain.RoleAdmin {
		return domain.ErrNotAllowed
	}
	return nil
}

func (a *App) Tutors(ctx context.Context, admin domain.User) ([]store.Tutor, error) {
	if err := a.requireAdmin(admin); err != nil {
		return nil, err
	}
	return a.S.AllTutors(ctx)
}

type AdminTutorCard struct {
	Tutor     store.Tutor
	BasePrice int64
	Windows   int
	Students  int
}

func (a *App) AdminTutor(ctx context.Context, admin domain.User, tutorID int64) (AdminTutorCard, error) {
	if err := a.requireAdmin(admin); err != nil {
		return AdminTutorCard{}, err
	}
	t, err := a.S.Tutor(ctx, tutorID)
	if err != nil {
		return AdminTutorCard{}, err
	}
	base, err := a.S.BasePrice(ctx)
	if err != nil {
		return AdminTutorCard{}, err
	}
	ws, err := a.S.Windows(ctx, tutorID)
	if err != nil {
		return AdminTutorCard{}, err
	}
	sts, err := a.S.TutorStudents(ctx, tutorID)
	if err != nil {
		return AdminTutorCard{}, err
	}
	return AdminTutorCard{Tutor: t, BasePrice: base, Windows: len(ws), Students: len(sts)}, nil
}

// SetTutorPrice — индивидуальная цена урока репетитора; nil возвращает общую.
func (a *App) SetTutorPrice(ctx context.Context, admin domain.User, tutorID int64, kopecks *int64) error {
	if err := a.requireAdmin(admin); err != nil {
		return err
	}
	if kopecks != nil && *kopecks < 0 {
		return domain.InputError("цена не может быть отрицательной")
	}
	return a.S.SetTutorPrice(ctx, tutorID, kopecks)
}

// SetBasePrice — общая цена урока. Уже купленные уроки не меняются (цена зафиксирована в журнале).
func (a *App) SetBasePrice(ctx context.Context, admin domain.User, kopecks int64) error {
	if err := a.requireAdmin(admin); err != nil {
		return err
	}
	if kopecks < 0 {
		return domain.InputError("цена не может быть отрицательной")
	}
	return a.S.SetBasePrice(ctx, kopecks)
}

type PriceList struct {
	BasePrice int64
	Products  []domain.Product // все, включая скрытые
}

func (a *App) Prices(ctx context.Context, admin domain.User) (PriceList, error) {
	if err := a.requireAdmin(admin); err != nil {
		return PriceList{}, err
	}
	base, err := a.S.BasePrice(ctx)
	if err != nil {
		return PriceList{}, err
	}
	ps, err := a.S.Products(ctx, false)
	if err != nil {
		return PriceList{}, err
	}
	return PriceList{BasePrice: base, Products: ps}, nil
}

func (a *App) AddProduct(ctx context.Context, admin domain.User, p domain.Product) error {
	if err := a.requireAdmin(admin); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" || p.Lessons < 1 || p.Lessons > 200 || p.DiscountPct < 0 || p.DiscountPct > 90 || p.ValidDays < 0 {
		return domain.InputError("проверьте название, число уроков (1–200) и скидку (0–90%)")
	}
	switch {
	case p.ValidDays > 0:
		p.Kind = domain.ProductSubscription
	case p.Lessons == 1:
		p.Kind = domain.ProductSingle
	default:
		p.Kind = domain.ProductPack
	}
	return a.S.AddProduct(ctx, p)
}

func (a *App) ToggleProduct(ctx context.Context, admin domain.User, id int) error {
	if err := a.requireAdmin(admin); err != nil {
		return err
	}
	return a.S.ToggleProduct(ctx, id)
}

func (a *App) AddSubject(ctx context.Context, admin domain.User, name string) error {
	if err := a.requireAdmin(admin); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 50 {
		return domain.InputError("название предмета — от 1 до 50 символов")
	}
	return a.S.AddSubject(ctx, name)
}

func (a *App) RecentFeedback(ctx context.Context, admin domain.User, limit int) ([]store.Feedback, error) {
	if err := a.requireAdmin(admin); err != nil {
		return nil, err
	}
	return a.S.RecentFeedback(ctx, limit)
}

func (a *App) LateCancels(ctx context.Context, admin domain.User, limit int) ([]domain.Lesson, error) {
	if err := a.requireAdmin(admin); err != nil {
		return nil, err
	}
	return a.S.LateCancels(ctx, limit)
}
