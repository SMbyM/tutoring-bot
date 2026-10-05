package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SMbyM/tutoring-bot/msg"
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
	_, err = s.q.ExecContext(ctx, `INSERT INTO outbox(provider, kind, chat_id, external_id, payload) VALUES ($1,$2,$3,$4,$5)`,
		it.Provider, string(it.Kind), it.ChatID, it.ExternalID, payload)
	return err
}

// ClaimOutbox берёт пачку готовых к отправке записей провайдера и откладывает их на lease,
// чтобы параллельный диспетчер их не взял. После отправки — MarkSent или MarkRetry.
func (s *Store) ClaimOutbox(ctx context.Context, provider string, limit int, lease time.Duration) ([]OutboxItem, error) {
	rows, err := s.q.QueryContext(ctx, `UPDATE outbox SET next_attempt_at = now() + make_interval(secs => $3), attempts = attempts + 1
		WHERE id IN (SELECT id FROM outbox WHERE provider=$1 AND status='pending' AND next_attempt_at <= now()
			ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING id, provider, kind, chat_id, external_id, payload, attempts`, provider, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var kind string
		var payload []byte
		if err := rows.Scan(&it.ID, &it.Provider, &kind, &it.ChatID, &it.ExternalID, &payload, &it.Attempts); err != nil {
			return nil, err
		}
		it.Kind = OutboxKind(kind)
		_ = json.Unmarshal(payload, &it.Message)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// строки возвращаются в произвольном порядке — отправляем в порядке создания
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (s *Store) MarkSent(ctx context.Context, id int64) error {
	_, err := s.q.ExecContext(ctx, `UPDATE outbox SET status='sent', sent_at=now(), last_error='' WHERE id=$1`, id)
	return err
}

// MarkRetry откладывает запись; после maxAttempts помечает её failed.
func (s *Store) MarkRetry(ctx context.Context, id int64, cause error, delay time.Duration, maxAttempts int) error {
	_, err := s.q.ExecContext(ctx, `UPDATE outbox SET last_error=$2, next_attempt_at = now() + make_interval(secs => $3),
		status = CASE WHEN attempts >= $4 THEN 'failed' ELSE 'pending' END WHERE id=$1`, id, cause.Error(), delay.Seconds(), maxAttempts)
	return err
}

// PendingOutbox — сколько записей ждёт отправки (для мониторинга и тестов).
func (s *Store) PendingOutbox(ctx context.Context) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT count(*) FROM outbox WHERE status='pending'`).Scan(&n)
	return n, err
}

// PurgeOutbox удаляет отправленные записи старше age.
func (s *Store) PurgeOutbox(ctx context.Context, age time.Duration) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM outbox WHERE status='sent' AND sent_at < now() - make_interval(secs => $1)`, age.Seconds())
	return err
}

// PreferredIdentity — аккаунт, через который пользователь заходил последним.
func (s *Store) PreferredIdentity(ctx context.Context, userID int64) (Identity, bool, error) {
	var i Identity
	err := s.q.QueryRowContext(ctx, `SELECT provider, external_id, chat_id, username FROM identities
		WHERE user_id=$1 ORDER BY last_seen_at DESC LIMIT 1`, userID).Scan(&i.Provider, &i.ExternalID, &i.ChatID, &i.Username)
	if notFound(err) {
		return i, false, nil
	}
	return i, err == nil, err
}
