-- name: GetSetting :one
SELECT value FROM app_settings WHERE key = $1;

-- name: UpsertSetting :exec
INSERT INTO app_settings (key, value) VALUES (@key, @value) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- name: ListProducts :many
SELECT * FROM products WHERE active OR NOT @only_active::boolean ORDER BY id;

-- name: ProductByID :one
SELECT * FROM products WHERE id = $1;

-- name: InsertProduct :exec
INSERT INTO products (name, kind, lessons, discount_pct, valid_days)
VALUES (@name, @kind, @lessons, @discount_pct, sqlc.narg(valid_days));

-- name: ToggleProduct :exec
UPDATE products SET active = NOT active WHERE id = $1;

-- name: InsertPayment :one
INSERT INTO payments (payer_id, student_id, tutor_id, product_id, amount)
VALUES (@payer_id, @student_id, @tutor_id, @product_id, @amount) RETURNING id;

-- Списание за урок идемпотентно: уникальный индекс по lesson_id для reason = 'lesson'.
-- name: InsertLedger :exec
INSERT INTO ledger (student_id, tutor_id, delta, unit_price, reason, payment_id, lesson_id, expires_at)
VALUES (@student_id, @tutor_id, @delta, @unit_price, @reason, sqlc.narg(payment_id), sqlc.narg(lesson_id), sqlc.narg(expires_at))
ON CONFLICT DO NOTHING;

-- name: Balance :one
SELECT COALESCE(SUM(delta), 0)::int AS balance FROM ledger WHERE student_id = @student_id AND tutor_id = @tutor_id;

-- ---- отзывы о пробных ----

-- name: InsertFeedback :one
INSERT INTO trial_feedback (lesson_id, student_id, tutor_id, liked) VALUES (@lesson_id, @student_id, @tutor_id, @liked)
ON CONFLICT (lesson_id) DO NOTHING RETURNING id;

-- name: SetFeedbackComment :exec
UPDATE trial_feedback SET comment = @comment WHERE id = @id;

-- name: FeedbackByID :one
SELECT * FROM feedback_details WHERE id = $1;

-- name: RecentFeedback :many
SELECT * FROM feedback_details ORDER BY created_at DESC LIMIT $1;

-- name: MarkForwarded :execrows
UPDATE trial_feedback SET forwarded_at = now() WHERE id = $1 AND forwarded_at IS NULL;

-- name: LikedTutor :one
SELECT EXISTS (SELECT 1 FROM trial_feedback WHERE student_id = @student_id AND tutor_id = @tutor_id AND liked);

-- ---- доступ в закрытый канал ----

-- name: AccessFacts :one
SELECT
    EXISTS (SELECT 1 FROM trial_feedback f WHERE f.student_id = @student_id AND f.liked) AS liked,
    EXISTS (SELECT 1 FROM channel_access c WHERE c.student_id = @student_id AND c.active) AS active,
    COALESCE((SELECT c.granted_at FROM channel_access c WHERE c.student_id = @student_id AND c.active), 'epoch'::timestamptz)::timestamptz AS granted_at,
    COALESCE((SELECT max(l.starts_at) FROM lessons l WHERE l.student_id = @student_id AND l.status = 'held'), 'epoch'::timestamptz)::timestamptz AS last_held,
    EXISTS (SELECT 1 FROM lessons l WHERE l.student_id = @student_id AND l.status = 'scheduled' AND l.starts_at > @now) AS has_upcoming;

-- name: GrantAccess :execrows
INSERT INTO channel_access (student_id) VALUES ($1)
ON CONFLICT (student_id) DO UPDATE SET active = true, granted_at = now(), revoked_at = NULL
WHERE NOT channel_access.active;

-- name: RevokeAccess :execrows
UPDATE channel_access SET active = false, revoked_at = now() WHERE student_id = $1 AND active;

-- name: AccessCandidates :many
SELECT student_id FROM trial_feedback WHERE liked
UNION
SELECT student_id FROM channel_access WHERE active;
