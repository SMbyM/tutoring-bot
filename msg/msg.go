// Package msg — транспортонезависимые сообщения и кнопки.
// Ядро и сценарии описывают «что показать», адаптеры TG/VK/MAX — «как отрисовать».
package msg

import "github.com/SMbyM/tutoring-bot/actions"

type Button struct {
	Text   string
	Action string // закодированное действие (callback); пусто, если это ссылка
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

// Btn — кнопка с действием. Строку действия собирает только пакет actions.
func Btn(text string, a actions.Action) Button {
	return Button{Text: text, Action: actions.Encode(a)}
}

func Link(text, url string) Button { return Button{Text: text, URL: url} }

func Row(bs ...Button) []Button { return bs }
