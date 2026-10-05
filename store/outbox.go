package store

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store/db"
)

type OutboxKind string

const (
	OutMessage       OutboxKind = "message"
	OutChannelInvite OutboxKind = "channel_invite" // создать одноразовую ссылку и отправить её сообщением Message
	OutChannelKick   OutboxKind = "channel_kick"   // исключить ExternalID из канала
)

type OutboxItem struct {
	ID         int64
	Provider   string
	Kind       OutboxKind
	ChatID     string
	ExternalID string
	Message    msg.Message
	Attempts   int
}

func (s *Store) Enqueue(ctx context.Context, it OutboxItem) error {
	payload, err := json.Marshal(it.Message)
	if err != nil {
		return err
	}
	return s.qs().EnqueueOutbox(ctx, db.EnqueueOutboxParams{Provider: it.Provider, Kind: string(it.Kind),
		ChatID: it.ChatID, ExternalID: it.ExternalID, Payload: payload})
}

// ClaimOutbox берёт пачку готовых к отправке записей провайдера и откладывает их на lease,
// чтобы параллельный диспетчер их не взял. После отправки — MarkSent или MarkRetry.
func (s *Store) ClaimOutbox(ctx context.Context, provider string, limit int, lease time.Duration) ([]OutboxItem, error) {
	rows, err := s.qs().ClaimOutbox(ctx, db.ClaimOutboxParams{Provider: provider, MaxRows: int32(limit), LeaseSeconds: lease.Seconds()})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxItem, len(rows))
	for i, r := range rows {
		out[i] = OutboxItem{ID: r.ID, Provider: r.Provider, Kind: OutboxKind(r.Kind), ChatID: r.ChatID,
			ExternalID: r.ExternalID, Attempts: int(r.Attempts)}
		_ = json.Unmarshal(r.Payload, &out[i].Message)
	}
	// UPDATE … RETURNING не гарантирует порядок — отправляем в порядке создания
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

func (s *Store) MarkSent(ctx context.Context, id int64) error {
	return s.qs().MarkOutboxSent(ctx, id)
}

// MarkRetry откладывает запись; после maxAttempts помечает её failed.
func (s *Store) MarkRetry(ctx context.Context, id int64, cause error, delay time.Duration, maxAttempts int) error {
	return s.qs().MarkOutboxRetry(ctx, db.MarkOutboxRetryParams{ID: id, LastError: cause.Error(),
		DelaySeconds: delay.Seconds(), MaxAttempts: int32(maxAttempts)})
}

// PendingOutbox — сколько записей ждёт отправки (для мониторинга и тестов).
func (s *Store) PendingOutbox(ctx context.Context) (int, error) {
	n, err := s.qs().PendingOutbox(ctx)
	return int(n), err
}

// PurgeOutbox удаляет отправленные записи старше age.
func (s *Store) PurgeOutbox(ctx context.Context, age time.Duration) error {
	return s.qs().PurgeOutbox(ctx, age.Seconds())
}
