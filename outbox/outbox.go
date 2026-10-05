// Package outbox — доставка исходящих сообщений из таблицы outbox.
// Ядро только пишет намерения в БД; каждый адаптер мессенджера запускает свой Dispatcher.
// Несколько экземпляров одного адаптера безопасны: записи берутся через FOR UPDATE SKIP LOCKED.
package outbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SMbyM/tutoring-bot/store"
)

// Handler выполняет записи своего мессенджера.
type Handler interface {
	Provider() string
	Handle(ctx context.Context, it store.OutboxItem) error
}

// Permanent помечает ошибку, которую бессмысленно повторять (например, пользователь заблокировал бота).
func Permanent(err error) error { return permanentErr{err} }

type permanentErr struct{ error }

func (p permanentErr) Unwrap() error { return p.error }

// RetryAfter — ошибка с подсказкой, когда повторить (лимиты мессенджера).
type RetryAfter struct {
	Err   error
	After time.Duration
}

func (r RetryAfter) Error() string { return r.Err.Error() }
func (r RetryAfter) Unwrap() error { return r.Err }

const (
	MaxAttempts = 8
	batch       = 50
	lease       = time.Minute
)

type Dispatcher struct {
	S        *store.Store
	H        Handler
	Interval time.Duration
	Log      *slog.Logger
}

func New(s *store.Store, h Handler) *Dispatcher {
	return &Dispatcher{S: s, H: h, Interval: time.Second, Log: slog.Default()}
}

// Run обрабатывает очередь до отмены ctx.
func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		// если пачка была полной — сразу берём следующую
		for {
			n, err := d.DrainOnce(ctx)
			if err != nil {
				d.Log.Error("outbox", "provider", d.H.Provider(), "err", err)
			}
			if n < batch || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// DrainOnce обрабатывает одну пачку и возвращает число взятых записей.
func (d *Dispatcher) DrainOnce(ctx context.Context) (int, error) {
	items, err := d.S.ClaimOutbox(ctx, d.H.Provider(), batch, lease)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		err := d.H.Handle(ctx, it)
		if err == nil {
			if err := d.S.MarkSent(ctx, it.ID); err != nil {
				return len(items), err
			}
			continue
		}
		max, delay := MaxAttempts, backoff(it.Attempts)
		var p permanentErr
		var ra RetryAfter
		switch {
		case errors.As(err, &p):
			max = 0
		case errors.As(err, &ra):
			delay = ra.After
		}
		d.Log.Warn("outbox: не отправлено", "id", it.ID, "kind", it.Kind, "attempt", it.Attempts, "err", err)
		if err := d.S.MarkRetry(ctx, it.ID, err, delay, max); err != nil {
			return len(items), err
		}
	}
	return len(items), nil
}

// backoff: 5с, 10с, 20с … но не больше часа.
func backoff(attempt int) time.Duration {
	d := 5 * time.Second << max(attempt-1, 0)
	if d > time.Hour || d <= 0 {
		return time.Hour
	}
	return d
}
