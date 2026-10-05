package domain

import (
	"testing"
	"time"
)

var nsk = LoadLocation(DefaultTZ)

func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, nsk)
}

func TestIsLate(t *testing.T) {
	start := at(2026, 10, 6, 17, 0)
	cases := []struct {
		now  time.Time
		late bool
	}{
		{start.Add(-13 * time.Hour), false},
		{start.Add(-12 * time.Hour), false},
		{start.Add(-12*time.Hour + time.Minute), true},
		{start.Add(-time.Hour), true},
	}
	for _, c := range cases {
		if got := IsLate(c.now, start); got != c.late {
			t.Errorf("IsLate(%v) = %v, want %v", c.now, got, c.late)
		}
	}
}

func TestDecideChange(t *testing.T) {
	cases := []struct {
		name        string
		a           Actor
		needsParent bool
		hasParents  bool
		want        ChangeDecision
	}{
		{"родитель применяет сразу", Actor{IsParent: true, Role: RoleParent}, true, true, ChangeApply},
		{"репетитор применяет сразу", Actor{IsTutor: true, Role: RoleTutor}, true, true, ChangeApply},
		{"ученик по умолчанию ждёт родителя", Actor{IsSelf: true, Role: RoleStudent}, true, true, ChangeNeedsApproval},
		{"ученик без родителей применяет", Actor{IsSelf: true, Role: RoleStudent}, true, false, ChangeApply},
		{"родитель разрешил свободный режим", Actor{IsSelf: true, Role: RoleStudent}, false, true, ChangeApply},
		{"посторонний", Actor{Role: RoleStudent}, false, false, ChangeForbidden},
		{"админ", Actor{Role: RoleAdmin}, true, true, ChangeApply},
	}
	for _, c := range cases {
		if got := DecideChange(c.a, c.needsParent, c.hasParents); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFreeSlots(t *testing.T) {
	// вт 7 окт 2026, окно 15:00-19:00, урок 60 мин, 16:00 занято
	windows := []Window{{time.Tuesday, 15 * 60, 19 * 60}}
	busy := []Interval{{at(2026, 10, 6, 16, 0), at(2026, 10, 6, 17, 0)}}
	from := at(2026, 10, 6, 0, 0)
	to := at(2026, 10, 6, 23, 59)
	got := FreeSlots(windows, nil, busy, nsk, from, to, time.Hour)
	want := []time.Time{at(2026, 10, 6, 15, 0), at(2026, 10, 6, 17, 0), at(2026, 10, 6, 18, 0)}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("slot %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFreeSlotsSkipsPastAndExceptions(t *testing.T) {
	windows := []Window{{time.Tuesday, 15 * 60, 17 * 60}, {time.Wednesday, 10 * 60, 11 * 60}}
	ex := []Exception{{at(2026, 10, 7, 0, 0), at(2026, 10, 7, 0, 0)}} // среда — отпуск
	from := at(2026, 10, 6, 15, 30)                                   // 15:00 уже прошло
	to := at(2026, 10, 7, 23, 0)
	got := FreeSlots(windows, ex, nil, nsk, from, to, time.Hour)
	if len(got) != 1 || !got[0].Equal(at(2026, 10, 6, 16, 0)) {
		t.Fatalf("got %v, want only 16:00 tue", got)
	}
}

func TestFreeSlotsUnevenWindow(t *testing.T) {
	// окно 90 минут, урок 60 — влезает только один
	windows := []Window{{time.Tuesday, 15 * 60, 16*60 + 30}}
	got := FreeSlots(windows, nil, nil, nsk, at(2026, 10, 6, 0, 0), at(2026, 10, 6, 23, 0), time.Hour)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestFitsWindows(t *testing.T) {
	windows := []Window{{time.Tuesday, 15 * 60, 19 * 60}}
	if !FitsWindows(windows, nil, nsk, at(2026, 10, 6, 18, 0), time.Hour) {
		t.Error("18:00-19:00 должно влезать")
	}
	if FitsWindows(windows, nil, nsk, at(2026, 10, 6, 18, 30), time.Hour) {
		t.Error("18:30-19:30 не должно влезать")
	}
	if FitsWindows(windows, nil, nsk, at(2026, 10, 7, 15, 0), time.Hour) {
		t.Error("среда не в окнах")
	}
}

func TestOccurrences(t *testing.T) {
	got := Occurrences(time.Tuesday, 17*60, nsk, at(2026, 10, 6, 17, 0), at(2026, 10, 27, 17, 0))
	if len(got) != 3 { // 6, 13, 20; 27 не входит (полуинтервал)
		t.Fatalf("got %v", got)
	}
	if !got[2].Equal(at(2026, 10, 20, 17, 0)) {
		t.Errorf("last = %v", got[2])
	}
}

func TestParseWindows(t *testing.T) {
	ws, err := ParseWindows("пн 15:00-19:00\nср, пт 10:00-12:00\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 3 {
		t.Fatalf("got %d windows", len(ws))
	}
	if got := FormatWindows(ws); got != "пн 15:00-19:00\nср 10:00-12:00\nпт 10:00-12:00" {
		t.Errorf("format: %q", got)
	}
	for _, bad := range []string{"пн 19:00-15:00", "xx 10:00-11:00", "пн 10-11", "пн 10:00-12:00\nпн 11:00-13:00"} {
		if _, err := ParseWindows(bad); err == nil {
			t.Errorf("%q: ожидалась ошибка", bad)
		}
	}
}

func TestPricing(t *testing.T) {
	pack := Product{Lessons: 8, DiscountPct: 5}
	if got := PriceOf(pack, 150000); got != 1140000 { // 8*1500*0.95 = 11400
		t.Errorf("pack price = %d", got)
	}
	if got := DiscountedUnit(pack, 150000); got != 142500 {
		t.Errorf("unit = %d", got)
	}
	over := int64(200000)
	if UnitPrice(150000, &over) != 200000 || UnitPrice(150000, nil) != 150000 {
		t.Error("UnitPrice override")
	}
	if FormatRub(1140000) != "11 400 ₽" || FormatRub(150050) != "1 500,50 ₽" {
		t.Errorf("FormatRub: %q %q", FormatRub(1140000), FormatRub(150050))
	}
	if v, err := ParseRub("1 500"); err != nil || v != 150000 {
		t.Errorf("ParseRub = %d, %v", v, err)
	}
}

func TestAccessEligible(t *testing.T) {
	now := at(2026, 10, 6, 12, 0)
	month := 30 * 24 * time.Hour
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-5 * 24 * time.Hour)
	cases := []struct {
		name string
		f    AccessFacts
		want bool
	}{
		{"не понравился ни один", AccessFacts{Liked: false, HasUpcoming: true}, false},
		{"понравился, есть урок впереди", AccessFacts{Liked: true, HasUpcoming: true}, true},
		{"понравился, недавно занимался", AccessFacts{Liked: true, LastHeld: &recent}, true},
		{"понравился, давно не занимался", AccessFacts{Liked: true, LastHeld: &old, GrantedAt: &old}, false},
		{"только что выдали доступ", AccessFacts{Liked: true, GrantedAt: &recent}, true},
	}
	for _, c := range cases {
		if got := AccessEligible(c.f, now, month); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestEffectiveRole(t *testing.T) {
	u := User{Role: RoleTutor, DebugRole: RoleStudent}
	if u.EffectiveRole(false) != RoleTutor {
		t.Error("в проде подмена роли не действует")
	}
	if u.EffectiveRole(true) != RoleStudent {
		t.Error("в debug роль подменяется")
	}
	if !u.CanSwitchRoles(true) || u.CanSwitchRoles(false) {
		t.Error("переключать роли может репетитор только в debug")
	}
	if (User{Role: RoleStudent}).CanSwitchRoles(true) {
		t.Error("ученик не переключает роли")
	}
}
