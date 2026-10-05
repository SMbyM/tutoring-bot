package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/outbox"
	"github.com/SMbyM/tutoring-bot/store"
)

type handler struct {
	mu   sync.Mutex
	seen map[string]int
	fail func(it store.OutboxItem) error
}

func (h *handler) Provider() string { return "tg" }
func (h *handler) Handle(_ context.Context, it store.OutboxItem) error {
	if h.fail != nil {
		if err := h.fail(it); err != nil {
			return err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen[it.Message.Text]++
	return nil
}

func open(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	s, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.ResetForTests(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func enqueue(t *testing.T, s *store.Store, provider, text string) {
	t.Helper()
	if err := s.Enqueue(context.Background(), store.OutboxItem{Provider: provider, Kind: store.OutMessage, ChatID: "1", Message: msg.Text(text)}); err != nil {
		t.Fatal(err)
	}
}

// Два экземпляра бота разбирают одну очередь: каждое сообщение доставлено ровно один раз.
func TestParallelDispatchersDeliverOnce(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	const n = 300
	for i := 0; i < n; i++ {
		enqueue(t, s, "tg", fmt.Sprint(i))
	}
	enqueue(t, s, "vk", "чужое") // запись другого мессенджера не трогаем
	h := &handler{seen: map[string]int{}}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := outbox.New(s, h)
			for {
				k, err := d.DrainOnce(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if k == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(h.seen) != n {
		t.Fatalf("доставлено %d разных сообщений из %d", len(h.seen), n)
	}
	for text, c := range h.seen {
		if c != 1 {
			t.Errorf("сообщение %s доставлено %d раз", text, c)
		}
	}
	if left, _ := s.PendingOutbox(ctx); left != 1 {
		t.Errorf("в очереди должна остаться только запись VK, осталось %d", left)
	}
}

func TestRetryAndPermanentFailure(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	enqueue(t, s, "tg", "сеть моргнула")
	enqueue(t, s, "tg", "бот заблокирован")
	h := &handler{seen: map[string]int{}, fail: func(it store.OutboxItem) error {
		switch it.Message.Text {
		case "сеть моргнула":
			if it.Attempts == 1 {
				return errors.New("timeout")
			}
		case "бот заблокирован":
			return outbox.Permanent(errors.New("forbidden"))
		}
		return nil
	}}
	d := outbox.New(s, h)
	if _, err := d.DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.seen) != 0 {
		t.Fatal("обе записи должны были упасть на первой попытке")
	}
	// постоянная ошибка не повторяется, временная — ждёт ретрая
	if left, _ := s.PendingOutbox(ctx); left != 1 {
		t.Fatalf("ожидалась 1 запись на повтор, в очереди %d", left)
	}
	// повтор ещё не наступил — второй проход ничего не берёт
	if k, _ := d.DrainOnce(ctx); k != 0 {
		t.Errorf("взято %d записей до истечения задержки", k)
	}
}
