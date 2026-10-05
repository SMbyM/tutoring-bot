package core

import (
	"context"
	"fmt"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store"
)

// TutorUnitPrice — цена урока у репетитора (общая или индивидуальная), копейки.
func (a *App) TutorUnitPrice(ctx context.Context, tutorID int64) (int64, error) {
	base, err := a.S.BasePrice(ctx)
	if err != nil {
		return 0, err
	}
	t, err := a.S.Tutor(ctx, tutorID)
	if err != nil {
		return 0, err
	}
	return domain.UnitPrice(base, t.PriceOverride), nil
}

// Purchase — ЗАГЛУШКА оплаты: записывает платёж и начисляет уроки в журнал, деньги не списываются.
// Цена урока фиксируется в журнале на момент покупки.
func (a *App) Purchase(ctx context.Context, payer domain.User, studentID, tutorID int64, productID int) (string, error) {
	if err := a.CanActForStudent(ctx, payer, studentID); err != nil {
		return "", err
	}
	p, err := a.S.Product(ctx, productID)
	if err != nil {
		return "", err
	}
	if !p.Active {
		return "", domain.ErrNotFound
	}
	unit, err := a.TutorUnitPrice(ctx, tutorID)
	if err != nil {
		return "", err
	}
	amount := domain.PriceOf(p, unit)
	err = a.S.Tx(ctx, func(s *store.Store) error {
		payID, err := s.InsertPayment(ctx, store.Payment{PayerID: payer.ID, StudentID: studentID, TutorID: tutorID, ProductID: p.ID, Amount: amount})
		if err != nil {
			return err
		}
		return s.AddLedger(ctx, store.LedgerEntry{StudentID: studentID, TutorID: tutorID, Delta: p.Lessons,
			UnitPrice: domain.DiscountedUnit(p, unit), Reason: "purchase", PaymentID: payID, ExpiresAt: domain.ExpiresAt(p, a.Now())})
	})
	if err != nil {
		return "", err
	}
	bal, err := a.S.Balance(ctx, studentID, tutorID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Оплата записана (тестовый режим, деньги не списаны): %s — %s.\nОплаченных уроков: %d",
		p.Name, domain.FormatRub(amount), bal), nil
}
