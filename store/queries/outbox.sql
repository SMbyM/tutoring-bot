-- name: EnqueueOutbox :exec
INSERT INTO outbox (provider, kind, chat_id, external_id, payload)
VALUES (@provider, @kind, @chat_id, @external_id, @payload);

-- Берём пачку готовых записей провайдера и откладываем их на lease, чтобы параллельный
-- диспетчер их не взял (FOR UPDATE SKIP LOCKED). После отправки — MarkOutboxSent или MarkOutboxRetry.
-- name: ClaimOutbox :many
UPDATE outbox SET next_attempt_at = now() + make_interval(secs => @lease_seconds::float8), attempts = attempts + 1
WHERE id IN (
    SELECT o.id FROM outbox o
    WHERE o.provider = @provider AND o.status = 'pending' AND o.next_attempt_at <= now()
    ORDER BY o.id LIMIT @max_rows
    FOR UPDATE SKIP LOCKED)
RETURNING id, provider, kind, chat_id, external_id, payload, attempts;

-- name: MarkOutboxSent :exec
UPDATE outbox SET status = 'sent', sent_at = now(), last_error = '' WHERE id = $1;

-- name: MarkOutboxRetry :exec
UPDATE outbox SET last_error = @last_error,
    next_attempt_at = now() + make_interval(secs => @delay_seconds::float8),
    status = CASE WHEN attempts >= @max_attempts::int THEN 'failed' ELSE 'pending' END
WHERE id = @id;

-- name: PendingOutbox :one
SELECT count(*)::int FROM outbox WHERE status = 'pending';

-- name: PurgeOutbox :exec
DELETE FROM outbox WHERE status = 'sent' AND sent_at < now() - make_interval(secs => @age_seconds::float8);
