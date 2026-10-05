package ui_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/outbox"
	"github.com/SMbyM/tutoring-bot/store"
	"github.com/SMbyM/tutoring-bot/ui"
)

// Сквозные сценарии «как в Telegram»: пользователи жмут кнопки, движок отвечает,
// уведомления другим пользователям попадают в их «входящие». Нужен TEST_DATABASE_URL.

// inbox — фейковый Telegram-адаптер: забирает записи outbox и раскладывает по чатам.
type inbox struct {
	mu  sync.Mutex
	msg map[string][]msg.Message
	g   *gate
	d   *outbox.Dispatcher
}

func (b *inbox) Provider() string { return "tg" }
func (b *inbox) Handle(_ context.Context, it store.OutboxItem) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch it.Kind {
	case store.OutChannelKick:
		return nil
	case store.OutChannelInvite:
		b.g.links++
	}
	b.msg[it.ChatID] = append(b.msg[it.ChatID], it.Message)
	return nil
}

func (b *inbox) deliver() {
	for {
		n, err := b.d.DrainOnce(context.Background())
		if err != nil {
			panic(err)
		}
		if n == 0 {
			return
		}
	}
}

type gate struct{ links int }

type world struct {
	t    *testing.T
	eng  *ui.Engine
	box  *inbox
	gate *gate
}

type client struct {
	w     *world
	id    string
	first string
	last  []msg.Message
}

func newWorld(t *testing.T, debug bool) *world {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.ResetForTests(ctx); err != nil {
		t.Fatal(err)
	}
	app := core.New(s, core.Options{Debug: debug, ChannelEnabled: true})
	w := &world{t: t, gate: &gate{}}
	w.box = &inbox{msg: map[string][]msg.Message{}, g: w.gate}
	w.box.d = outbox.New(s, w.box)
	w.eng = ui.NewEngine(app, map[string]bool{"100": true}, "", "")
	w.eng.BotLink = func(p string) string { return "https://t.me/test_bot?start=" + p }
	return w
}

func (w *world) user(id, first string) *client { return &client{w: w, id: id, first: first} }

func (c *client) do(in ui.Input) []msg.Message {
	in.Provider, in.ExternalID, in.ChatID, in.FirstName = "tg", c.id, c.id, c.first
	c.last = c.w.eng.Handle(context.Background(), in)
	for _, m := range c.last {
		if strings.Contains(m.Text, "Что-то пошло не так") {
			c.w.t.Fatalf("[%s] внутренняя ошибка после %+v", c.id, in)
		}
	}
	return c.last
}

func (c *client) send(text string) *client { c.do(ui.Input{Text: text}); return c }

// inbox — уведомления, пришедшие пользователю от других (и очистка).
func (c *client) inbox() []msg.Message {
	c.w.box.deliver()
	c.w.box.mu.Lock()
	defer c.w.box.mu.Unlock()
	m := c.w.box.msg[c.id]
	delete(c.w.box.msg, c.id)
	return m
}

func (c *client) peekInbox() []msg.Message {
	c.w.box.deliver()
	c.w.box.mu.Lock()
	defer c.w.box.mu.Unlock()
	return append([]msg.Message(nil), c.w.box.msg[c.id]...)
}

// click нажимает первую кнопку с подстрокой в последнем ответе, иначе — во входящих (свежие первыми).
func (c *client) click(sub string) *client {
	c.w.t.Helper()
	find := func(ms []msg.Message) string {
		for i := len(ms) - 1; i >= 0; i-- {
			for _, row := range ms[i].Buttons {
				for _, b := range row {
					if b.Action != "" && strings.Contains(b.Text, sub) {
						return b.Action
					}
				}
			}
		}
		return ""
	}
	act := find(c.last)
	if act == "" {
		act = find(c.peekInbox())
	}
	if act == "" {
		c.w.t.Fatalf("[%s] нет кнопки %q. Последний ответ: %s", c.id, sub, dump(c.last))
	}
	c.do(ui.Input{Action: act})
	return c
}

// clickFirst нажимает первую кнопку последнего ответа, чьё действие начинается с prefix.
func (c *client) clickFirst(prefix string) *client {
	c.w.t.Helper()
	for _, m := range c.last {
		for _, row := range m.Buttons {
			for _, b := range row {
				if strings.HasPrefix(b.Action, prefix+":") {
					c.do(ui.Input{Action: b.Action})
					return c
				}
			}
		}
	}
	c.w.t.Fatalf("[%s] нет кнопки с действием %q: %s", c.id, prefix, dump(c.last))
	return c
}

// reasonIfAsked — если урок ближе 12 часов, бот просит причину.
func (c *client) reasonIfAsked() {
	for _, m := range c.last {
		if strings.Contains(m.Text, "Напишите коротко причину") {
			c.send("заболел")
			return
		}
	}
}

func (c *client) sees(sub string) {
	c.w.t.Helper()
	for _, m := range c.last {
		if strings.Contains(m.Text, sub) {
			return
		}
		for _, row := range m.Buttons {
			for _, b := range row {
				if strings.Contains(b.Text, sub) {
					return
				}
			}
		}
	}
	c.w.t.Fatalf("[%s] ожидали %q, а получили: %s", c.id, sub, dump(c.last))
}

func hasInbox(ms []msg.Message, sub string) bool {
	for _, m := range ms {
		if strings.Contains(m.Text, sub) {
			return true
		}
	}
	return false
}

func dump(ms []msg.Message) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString("\n---\n" + m.Text)
		for _, row := range m.Buttons {
			b.WriteString("\n[")
			for _, bt := range row {
				b.WriteString(bt.Text + " (" + bt.Action + ") ")
			}
			b.WriteString("]")
		}
	}
	return b.String()
}

func linkPayload(t *testing.T, ms []msg.Message) string {
	t.Helper()
	for _, m := range ms {
		if i := strings.Index(m.Text, "?start="); i >= 0 {
			return strings.Fields(m.Text[i+len("?start="):])[0]
		}
	}
	t.Fatalf("нет ссылки-приглашения: %s", dump(ms))
	return ""
}

// register проводит пользователя через согласие, имя и онбординг настроек.
func register(c *client, role string, name string) {
	c.send("/start")
	c.sees("Согласен")
	c.click("Согласен")
	if role != "" {
		c.click(role)
	}
	c.sees("Как к вам обращаться")
	c.send(name)
	if role == "Ученик" {
		c.sees("В каком вы классе")
		c.click("7")
	}
	c.sees("настройки по умолчанию")
	c.click("Всё оставить")
}

func TestEndToEnd(t *testing.T) {
	w := newWorld(t, true)

	// администратор из конфигурации
	admin := w.user("100", "Ольга")
	admin.send("/start").click("Согласен").click("Оставить «Ольга»")
	admin.click("Всё оставить")
	admin.sees("Репетиторы")

	// приглашение репетитора
	admin.click("Пригласить репетитора")
	code := linkPayload(t, admin.last)
	tutor := w.user("200", "Анна")
	tutor.send("/start " + code)
	tutor.sees("приглашены в школу как репетитор")
	tutor.click("Согласен")
	tutor.send("Анна Сергеевна")
	tutor.click("Всё оставить")
	tutor.sees("Окна расписания")
	// повторно ссылка не работает
	w.user("999", "X").send("/start " + code).sees("недействительно")

	// профиль и окна
	tutor.click("Профиль").click("Математика")
	tutor.sees("✅ Математика")
	tutor.click("Назад").click("Окна расписания").click("Задать текстом")
	tutor.send("пн 25:00-26:00")
	tutor.sees("некорректно")
	tutor.send("пн, вт, ср, чт, пт, сб, вс 10:00-20:00")
	tutor.sees("Расписание сохранено")

	// ученик и родитель
	kid := w.user("300", "Петя")
	register(kid, "Ученик", "Петя")
	kid.sees("Записаться на урок")
	kid.click("Привязать родителя")
	pcode := linkPayload(t, kid.last)
	mom := w.user("400", "Мария")
	mom.send("/start " + pcode)
	mom.sees("Вы привязаны как родитель: Петя")
	mom.click("Согласен")
	mom.send("Мария")
	mom.click("Всё оставить")
	mom.sees("Петя")
	if !hasInbox(kid.inbox(), "Родитель привязан") {
		t.Error("ученик должен узнать о привязке родителя")
	}

	// пробный урок
	kid.send("/menu").click("Записаться на урок").click("Математика").click("Анна")
	kid.sees("Пробный урок (бесплатно)")
	kid.click("Пробный урок").clickFirst("bd").clickFirst("bt")
	kid.sees("Записали на пробный урок")
	if !hasInbox(tutor.inbox(), "пробный") || !hasInbox(mom.inbox(), "пробный") {
		t.Error("репетитор и родитель должны получить уведомление о записи")
	}

	// репетитор в debug «проматывает» урок и отмечает его
	tutor.send("/debug")
	tutor.sees("Тестовый режим")
	tutor.click("Завершить ближайший урок")
	tutor.sees("теперь в прошлом")
	if !hasInbox(tutor.peekInbox(), "Как прошёл урок") {
		t.Fatal("репетитора должны спросить про урок")
	}
	tutor.click("Состоялся")
	tutor.sees("урок состоялся")
	tutor.inbox()

	// отзыв ученика → доступ в канал, уведомления родителю и админу
	kid.click("Понравился")
	kid.sees("Заниматься у этого репетитора")
	if kidMsgs := kid.peekInbox(); w.gate.links != 1 || !hasInbox(kidMsgs, "закрытый канал") {
		t.Error("после отзыва «понравился» должна прийти ссылка в канал")
	}
	if !hasInbox(mom.inbox(), "понравился") {
		t.Error("родитель видит отзыв")
	}
	if len(tutor.inbox()) != 0 {
		t.Error("репетитор не получает отзыв без решения админа")
	}
	kid.click("Добавить комментарий")
	kid.send("Объясняет понятно")
	kid.sees("комментарий передан")

	admin.click("Сообщить репетитору")
	admin.sees("Передано репетитору")
	if !hasInbox(tutor.inbox(), "понравился пробный урок") {
		t.Error("админ переслал итог репетитору")
	}

	// закрепление, постоянное время (записывает родитель)
	kid.click("Заниматься у этого репетитора")
	kid.sees("Постоянное время")
	mom.send("/menu").click("Петя").click("Записать").click("Математика").click("Анна")
	mom.click("Постоянное время").clickFirst("bd").clickFirst("bt")
	mom.sees("Постоянное время: каждую неделю")
	if !hasInbox(kid.inbox(), "Постоянное расписание") {
		t.Error("ученик должен узнать о постоянном расписании")
	}

	// оплата пакетом (заглушка)
	mom.send("/menu").click("Петя").click("Оплата").click("Оплатить — Анна").click("Пакет 8")
	mom.sees("Оплаченных уроков: 8")

	// перенос учеником — через подтверждение родителя (режим по умолчанию)
	kid.send("/menu").click("Мои уроки").clickFirst("lo")
	kid.click("Перенести").clickFirst("rd").clickFirst("rt")
	kid.reasonIfAsked()
	kid.sees("Запрос отправлен родителю")
	mom.click("Разрешить")
	mom.sees("изменение применено")
	if !hasInbox(tutor.inbox(), "Урок перенесён") {
		t.Error("репетитор узнаёт о переносе")
	}

	// родитель разрешает ребёнку менять уроки самому
	mom.send("/menu").click("Петя")
	mom.sees("с моего подтверждения")
	mom.click("изменить режим")
	mom.sees("свободно")
	kid.send("/menu").click("Мои уроки").clickFirst("lo").click("Отменить").click("Да, отменить")
	kid.reasonIfAsked()
	kid.sees("Урок отменён")

	// админ: цены, тариф, поздние отмены
	admin.send("/menu").click("Цены и тарифы").click("Изменить общую цену")
	admin.send("1800")
	admin.sees("1 800 ₽")
	admin.click("Новый тариф")
	admin.send("Пакет 4; 4; 3; 0")
	admin.sees("Пакет 4")
	admin.send("/menu").click("Репетиторы").click("Анна").click("Индивидуальная цена")
	admin.send("2000")
	admin.sees("2 000 ₽ (индивидуальная)")

	// права: ученик не может открыть админку
	kid.do(ui.Input{Action: "apr"})
	kid.sees("нет прав")

	// настройки
	kid.send("/menu").click("Настройки").click("Утром в день урока")
	kid.sees("Утром в день урока: выкл")
}

func TestManagedChildAndChildInvite(t *testing.T) {
	w := newWorld(t, true)
	mom := w.user("400", "Мария")
	register(mom, "Родитель", "Мария")
	mom.click("Ребёнок без Telegram")
	mom.send("Маша")
	mom.click("5")
	mom.sees("Маша добавлен")
	mom.send("/menu").sees("Маша")

	mom.click("Ребёнок с Telegram")
	code := linkPayload(t, mom.last)
	kid := w.user("500", "Ваня")
	kid.send("/start " + code)
	kid.sees("привязан к родителю")
	kid.click("Согласен")
	kid.send("Ваня")
	kid.click("8")
	kid.click("Всё оставить")
	if !hasInbox(mom.inbox(), "Ребёнок привязан") {
		t.Error("родитель узнаёт о привязке")
	}
	mom.send("/menu").sees("Ваня")
}

func TestProductionHidesDebug(t *testing.T) {
	w := newWorld(t, false)
	admin := w.user("100", "Ольга")
	admin.send("/start").click("Согласен").click("Оставить").click("Всё оставить")
	for _, m := range admin.last {
		for _, row := range m.Buttons {
			for _, b := range row {
				if strings.Contains(b.Text, "Тест") {
					t.Error("в production нет кнопки тестового режима")
				}
			}
		}
	}
	admin.send("/debug")
	admin.sees("недоступен")
	admin.do(ui.Input{Action: "dbr:student"})
	admin.sees("нет прав")
}

// Данные кнопки приходят от клиента: мусор и подделки не должны ронять бота или давать доступ.
func TestForgedButtons(t *testing.T) {
	w := newWorld(t, true)
	kid := w.user("300", "Петя")
	register(kid, "Ученик", "Петя")
	for _, data := range []string{"mk:1:delete", "bk:1:1:1:zzz", "lo:abc", "nope", "rd:1:2026-01-01", strings.Repeat("x", 100)} {
		out := kid.do(ui.Input{Action: data})
		toast := false
		for _, m := range out {
			if m.Toast == "Кнопка устарела" {
				toast = true
			}
		}
		if !toast {
			t.Errorf("кнопка %q: ожидалось «Кнопка устарела», получено %s", data, dump(out))
		}
	}
	// корректная по форме, но чужая кнопка: права проверяет core
	kid.do(ui.Input{Action: "ls:999999"})
	kid.sees("нет прав")
}
