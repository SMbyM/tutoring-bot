package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ProductKind string

const (
	ProductSingle       ProductKind = "single"
	ProductPack         ProductKind = "pack"
	ProductSubscription ProductKind = "subscription"
)

type Product struct {
	ID          int
	Name        string
	Kind        ProductKind
	Lessons     int
	DiscountPct int
	ValidDays   int // 0 — бессрочно
	Active      bool
}

// UnitPrice — цена урока для репетитора: индивидуальная, если задана, иначе общая. Копейки.
func UnitPrice(base int64, override *int64) int64 {
	if override != nil {
		return *override
	}
	return base
}

// PriceOf — стоимость продукта в копейках с учётом скидки, округление вниз до рубля.
func PriceOf(p Product, unit int64) int64 {
	total := unit * int64(p.Lessons) * int64(100-p.DiscountPct) / 100
	return total / 100 * 100
}

// DiscountedUnit — цена одного урока внутри продукта (фиксируется в журнале при покупке).
func DiscountedUnit(p Product, unit int64) int64 {
	if p.Lessons == 0 {
		return 0
	}
	return PriceOf(p, unit) / int64(p.Lessons)
}

// ExpiresAt — срок действия купленных уроков (только абонемент).
func ExpiresAt(p Product, now time.Time) *time.Time {
	if p.ValidDays <= 0 {
		return nil
	}
	t := now.AddDate(0, 0, p.ValidDays)
	return &t
}

// FormatRub: 150000 → «1 500 ₽».
func FormatRub(kopecks int64) string {
	rub := kopecks / 100
	s := strconv.FormatInt(rub, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(c)
	}
	if k := kopecks % 100; k != 0 {
		return fmt.Sprintf("%s,%02d ₽", b.String(), k)
	}
	return b.String() + " ₽"
}

// ParseRub: «1500», «1 500», «1500.50» → копейки.
func ParseRub(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "₽")), " ", "")
	s = strings.ReplaceAll(s, ",", ".")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e7 {
		return 0, fmt.Errorf("не похоже на сумму в рублях: %q", s)
	}
	return int64(f*100 + 0.5), nil
}

// Access — правило доступа в закрытый канал.
//
// Доступ есть, если ученику понравился хотя бы один пробный урок И ученик активен:
// доступ выдан недавно, был проведённый урок за последние inactivity, или есть запланированный урок.
type AccessFacts struct {
	Liked       bool
	GrantedAt   *time.Time // nil — доступа никогда не было или он отозван
	LastHeld    *time.Time
	HasUpcoming bool
}

func AccessEligible(f AccessFacts, now time.Time, inactivity time.Duration) bool {
	if !f.Liked {
		return false
	}
	if f.HasUpcoming {
		return true
	}
	cutoff := now.Add(-inactivity)
	if f.LastHeld != nil && f.LastHeld.After(cutoff) {
		return true
	}
	return f.GrantedAt != nil && f.GrantedAt.After(cutoff)
}
