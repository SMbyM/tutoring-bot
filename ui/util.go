package ui

import (
	"strconv"
	"time"
	"unicode/utf8"
)

func itoa(n int) string { return strconv.Itoa(n) }

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func unix(s string) time.Time {
	v := atoi64(s)
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

func trim(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	rs := []rune(s)
	return string(rs[:n]) + "…"
}

func dayKey(t time.Time, loc *time.Location) string { return t.In(loc).Format("20060102") }

func onOff(b bool) string {
	if b {
		return "вкл"
	}
	return "выкл"
}
