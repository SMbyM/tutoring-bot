package ui

import (
	"strconv"
	"unicode"
	"unicode/utf8"
)

func itoa(n int) string { return strconv.Itoa(n) }

func trim(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	rs := []rune(s)
	return string(rs[:n]) + "…"
}

// lowerFirst — первая буква строчная (по рунам: у кириллицы буква занимает два байта).
func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

func onOff(b bool) string {
	if b {
		return "вкл"
	}
	return "выкл"
}
