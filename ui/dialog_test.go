package ui_test

import (
	"context"
	"testing"
)

// pendingKind — какой вопрос сейчас ждёт ответа у пользователя ("" — никакой).
func (c *client) pendingKind() string {
	c.w.t.Helper()
	ctx := context.Background()
	u, ok, err := c.w.eng.App.FindUser(ctx, "tg", c.id)
	if err != nil || !ok {
		c.w.t.Fatalf("пользователь %s не найден: %v", c.id, err)
	}
	kind, _, _, err := c.w.eng.S.State(ctx, u.ID)
	if err != nil {
		c.w.t.Fatal(err)
	}
	return kind
}

func TestDialogClearsAfterAnswerKeepsAfterBadInput(t *testing.T) {
	w := newWorld(t, true)
	admin := w.user("100", "Ольга")
	admin.send("/start").click("Согласен").click("Оставить").click("Всё оставить")

	admin.click("Цены и тарифы").click("Изменить общую цену")
	if k := admin.pendingKind(); k != "abase" {
		t.Fatalf("ожидался вопрос о цене, а не %q", k)
	}
	admin.send("тысяча")
	admin.sees("не похоже на сумму")
	if k := admin.pendingKind(); k != "abase" {
		t.Fatalf("после неверного ввода вопрос должен остаться, а не %q", k)
	}
	admin.send("1700")
	admin.sees("1 700 ₽")
	if k := admin.pendingKind(); k != "" {
		t.Fatalf("после ответа вопрос должен сняться, а остался %q", k)
	}
	// следующий текст уже не трактуется как цена
	admin.send("2000")
	admin.sees("главное меню")

	// вопрос, ответ на который отклонил core (ошибка ввода): остаётся
	admin.click("Цены и тарифы").click("Новый тариф")
	admin.send("Пакет; 0; 0; 0")
	admin.sees("число уроков")
	if k := admin.pendingKind(); k != "aproduct" {
		t.Fatalf("ошибка ввода от core тоже оставляет вопрос, а не %q", k)
	}
	// уход в меню снимает вопрос
	admin.send("/menu")
	if k := admin.pendingKind(); k != "" {
		t.Fatalf("/menu должен снимать вопрос, остался %q", k)
	}
}

func TestDialogChainChildNameThenGrade(t *testing.T) {
	w := newWorld(t, true)
	mom := w.user("400", "Мария")
	register(mom, "Родитель", "Мария")
	mom.click("Ребёнок без Telegram")
	mom.send("Маша")
	if k := mom.pendingKind(); k != "kidgrade" {
		t.Fatalf("после имени должен идти вопрос о классе, а не %q", k)
	}
	mom.send("пятый")
	mom.sees("Выберите класс кнопкой")
	mom.click("5")
	mom.sees("Маша добавлен")
	if k := mom.pendingKind(); k != "" {
		t.Fatalf("после выбора класса вопрос снимается, остался %q", k)
	}
}
