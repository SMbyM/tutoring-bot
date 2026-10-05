package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Window — недельное окно репетитора в его часовом поясе, минуты от полуночи.
type Window struct {
	Weekday  time.Weekday
	StartMin int
	EndMin   int
}

// Exception — период, когда репетитор недоступен (даты включительно).
type Exception struct {
	From time.Time // полночь даты в поясе репетитора
	To   time.Time
}

type Interval struct {
	Start, End time.Time
}

func (a Interval) Overlaps(b Interval) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

func dateOf(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func inException(day time.Time, ex []Exception) bool {
	for _, e := range ex {
		if !day.Before(e.From) && !day.After(e.To) {
			return true
		}
	}
	return false
}

// FreeSlots режет окна на слоты длиной dur и выкидывает занятые, прошедшие и попавшие в исключения.
// Слоты начинаются от начала окна с шагом dur. Результат отсортирован.
func FreeSlots(windows []Window, ex []Exception, busy []Interval, loc *time.Location,
	from, to time.Time, dur time.Duration) []time.Time {
	var out []time.Time
	if dur <= 0 {
		return nil
	}
	durMin := int(dur / time.Minute)
	for day := dateOf(from, loc); !day.After(to); day = day.AddDate(0, 0, 1) {
		if inException(day, ex) {
			continue
		}
		for _, w := range windows {
			if w.Weekday != day.Weekday() {
				continue
			}
			for m := w.StartMin; m+durMin <= w.EndMin; m += durMin {
				start := time.Date(day.Year(), day.Month(), day.Day(), 0, m, 0, 0, loc)
				if start.Before(from) || start.After(to) {
					continue
				}
				slot := Interval{start, start.Add(dur)}
				taken := false
				for _, b := range busy {
					if slot.Overlaps(b) {
						taken = true
						break
					}
				}
				if !taken {
					out = append(out, start)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return dedupTimes(out)
}

func dedupTimes(ts []time.Time) []time.Time {
	if len(ts) < 2 {
		return ts
	}
	out := ts[:1]
	for _, t := range ts[1:] {
		if !t.Equal(out[len(out)-1]) {
			out = append(out, t)
		}
	}
	return out
}

// FitsWindows проверяет, что интервал целиком лежит в одном окне и не попадает в исключение.
func FitsWindows(windows []Window, ex []Exception, loc *time.Location, start time.Time, dur time.Duration) bool {
	s := start.In(loc)
	day := dateOf(s, loc)
	if inException(day, ex) {
		return false
	}
	startMin := s.Hour()*60 + s.Minute()
	endMin := startMin + int(dur/time.Minute)
	for _, w := range windows {
		if w.Weekday == s.Weekday() && startMin >= w.StartMin && endMin <= w.EndMin {
			return true
		}
	}
	return false
}

// Occurrences — даты постоянного слота (weekday, startMin в поясе loc) в полуинтервале [from, to).
func Occurrences(wd time.Weekday, startMin int, loc *time.Location, from, to time.Time) []time.Time {
	var out []time.Time
	for day := dateOf(from, loc); day.Before(to); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != wd {
			continue
		}
		t := time.Date(day.Year(), day.Month(), day.Day(), 0, startMin, 0, 0, loc)
		if !t.Before(from) && t.Before(to) {
			out = append(out, t)
		}
	}
	return out
}

var weekdayShort = []string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
var weekdayFull = []string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}

func WeekdayShort(d time.Weekday) string { return weekdayShort[d] }
func WeekdayFull(d time.Weekday) string  { return weekdayFull[d] }

func parseWeekday(s string) (time.Weekday, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, w := range weekdayShort {
		if s == w || strings.HasPrefix(weekdayFull[i], s) && len(s) >= 2 {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

func parseHM(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("время %q: нужен формат ЧЧ:ММ", s)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 24 || m < 0 || m > 59 || h == 24 && m != 0 {
		return 0, fmt.Errorf("время %q некорректно", s)
	}
	return h*60 + m, nil
}

// ParseWindows разбирает текст вида
//
//	пн 15:00-19:00
//	ср, пт 10:00-12:00
//
// Пустые строки пропускаются. Пересекающиеся окна одного дня — ошибка.
func ParseWindows(text string) ([]Window, error) {
	var out []Window
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sp := strings.LastIndex(line, " ")
		if sp < 0 {
			return nil, fmt.Errorf("строка %d: ожидалось «пн 15:00-19:00»", n+1)
		}
		daysPart, timePart := line[:sp], line[sp+1:]
		rng := strings.Split(timePart, "-")
		if len(rng) != 2 {
			return nil, fmt.Errorf("строка %d: интервал пишется как 15:00-19:00", n+1)
		}
		s, err := parseHM(rng[0])
		if err != nil {
			return nil, fmt.Errorf("строка %d: %w", n+1, err)
		}
		e, err := parseHM(rng[1])
		if err != nil {
			return nil, fmt.Errorf("строка %d: %w", n+1, err)
		}
		if e <= s {
			return nil, fmt.Errorf("строка %d: конец раньше начала", n+1)
		}
		days := strings.FieldsFunc(daysPart, func(r rune) bool { return r == ',' || r == ' ' })
		if len(days) == 0 {
			return nil, fmt.Errorf("строка %d: не указан день недели", n+1)
		}
		for _, d := range days {
			wd, ok := parseWeekday(d)
			if !ok {
				return nil, fmt.Errorf("строка %d: непонятный день %q", n+1, d)
			}
			out = append(out, Window{wd, s, e})
		}
	}
	if err := ValidateWindows(out); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateWindows проверяет границы и отсутствие пересечений внутри дня.
func ValidateWindows(ws []Window) error {
	sorted := append([]Window(nil), ws...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Weekday != sorted[j].Weekday {
			return sorted[i].Weekday < sorted[j].Weekday
		}
		return sorted[i].StartMin < sorted[j].StartMin
	})
	for i, w := range sorted {
		if w.Weekday < 0 || w.Weekday > 6 || w.StartMin < 0 || w.EndMin > 24*60 || w.EndMin <= w.StartMin {
			return fmt.Errorf("некорректное окно %s", FormatWindow(w))
		}
		if i > 0 && sorted[i-1].Weekday == w.Weekday && sorted[i-1].EndMin > w.StartMin {
			return fmt.Errorf("окна пересекаются: %s и %s", FormatWindow(sorted[i-1]), FormatWindow(w))
		}
	}
	return nil
}

func hm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

func FormatWindow(w Window) string {
	return fmt.Sprintf("%s %s-%s", WeekdayShort(w.Weekday), hm(w.StartMin), hm(w.EndMin))
}

// FormatWindows — по строке на окно, неделя с понедельника.
func FormatWindows(ws []Window) string {
	sorted := append([]Window(nil), ws...)
	order := func(d time.Weekday) int { return (int(d) + 6) % 7 }
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Weekday != sorted[j].Weekday {
			return order(sorted[i].Weekday) < order(sorted[j].Weekday)
		}
		return sorted[i].StartMin < sorted[j].StartMin
	})
	lines := make([]string, len(sorted))
	for i, w := range sorted {
		lines[i] = FormatWindow(w)
	}
	return strings.Join(lines, "\n")
}

var monthsGen = []string{"", "января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

// FormatDateTime: «вт, 7 октября, 17:00».
func FormatDateTime(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return fmt.Sprintf("%s, %d %s, %02d:%02d", WeekdayShort(t.Weekday()), t.Day(), monthsGen[t.Month()], t.Hour(), t.Minute())
}

// FormatDay: «вт, 7 октября».
func FormatDay(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return fmt.Sprintf("%s, %d %s", WeekdayShort(t.Weekday()), t.Day(), monthsGen[t.Month()])
}

func FormatTime(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
}
