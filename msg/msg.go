// Package msg — транспортонезависимые сообщения и кнопки.
// Ядро и сценарии описывают «что показать», адаптеры TG/VK/MAX — «как отрисовать».
package msg

import (
	"strconv"
	"strings"
)

type Button struct {
	Text   string
	Action string // внутреннее действие (callback); пусто, если это ссылка
	URL    string // внешняя ссылка
	WebApp string // ссылка на Mini App
}

type Message struct {
	Text    string
	Buttons [][]Button
	// Replace — заменить сообщение, на кнопку которого нажали, а не слать новое.
	Replace bool
	// Toast — короткое всплывающее уведомление (ответ на нажатие кнопки).
	Toast string
}

func Text(t string) Message { return Message{Text: t} }

func Btn(text string, action string, args ...any) Button {
	return Button{Text: text, Action: Act(action, args...)}
}

func Link(text, url string) Button { return Button{Text: text, URL: url} }

func Row(bs ...Button) []Button { return bs }

// Act собирает действие «имя:арг1:арг2». Telegram ограничивает callback 64 байтами — держим коротко.
func Act(name string, args ...any) string {
	var b strings.Builder
	b.WriteString(name)
	for _, a := range args {
		b.WriteByte(':')
		switch v := a.(type) {
		case string:
			b.WriteString(v)
		case int:
			b.WriteString(strconv.Itoa(v))
		case int64:
			b.WriteString(strconv.FormatInt(v, 10))
		case bool:
			if v {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		default:
			panic("msg.Act: неподдерживаемый тип аргумента")
		}
	}
	return b.String()
}

// Parsed — разобранное действие.
type Parsed struct {
	Name string
	Args []string
}

func Parse(action string) Parsed {
	parts := strings.Split(action, ":")
	return Parsed{Name: parts[0], Args: parts[1:]}
}

func (p Parsed) Int(i int) int64 {
	if i >= len(p.Args) {
		return 0
	}
	v, _ := strconv.ParseInt(p.Args[i], 10, 64)
	return v
}

func (p Parsed) Str(i int) string {
	if i >= len(p.Args) {
		return ""
	}
	return p.Args[i]
}
