-- name: UserByIdentity :one
SELECT u.* FROM users u
JOIN identities i ON i.user_id = u.id
WHERE i.provider = @provider AND i.external_id = @external_id;

-- name: UserByID :one
SELECT * FROM users WHERE id = $1;

-- name: InsertUser :one
INSERT INTO users (name, role) VALUES (@name, @role) RETURNING id;

-- name: InsertIdentity :exec
INSERT INTO identities (provider, external_id, user_id, chat_id, username)
VALUES (@provider, @external_id, @user_id, @chat_id, @username);

-- name: TouchIdentity :exec
UPDATE identities SET chat_id = @chat_id, username = @username, last_seen_at = now()
WHERE provider = @provider AND external_id = @external_id;

-- name: Identities :many
SELECT provider, external_id, chat_id, username FROM identities WHERE user_id = $1;

-- name: PreferredIdentity :one
SELECT provider, external_id, chat_id, username FROM identities
WHERE user_id = $1 ORDER BY last_seen_at DESC LIMIT 1;

-- name: SetName :exec
UPDATE users SET name = @name WHERE id = @id;

-- name: SetRole :exec
UPDATE users SET role = @role WHERE id = @id;

-- name: SetDebugRole :exec
UPDATE users SET debug_role = sqlc.narg(debug_role) WHERE id = @id;

-- name: SetGrade :exec
UPDATE users SET grade = @grade WHERE id = @id;

-- name: SetConsent :exec
UPDATE users SET consent_at = now() WHERE id = $1 AND consent_at IS NULL;

-- name: Admins :many
SELECT * FROM users WHERE role = 'admin' ORDER BY id;

-- name: LinkParent :exec
INSERT INTO parent_links (parent_id, student_id) VALUES (@parent_id, @student_id) ON CONFLICT DO NOTHING;

-- name: ParentsOf :many
SELECT u.* FROM users u JOIN parent_links p ON p.parent_id = u.id
WHERE p.student_id = $1 ORDER BY u.id;

-- name: ChildrenOf :many
SELECT u.* FROM users u JOIN parent_links p ON p.student_id = u.id
WHERE p.parent_id = $1 ORDER BY u.id;

-- name: IsParentOf :one
SELECT EXISTS (SELECT 1 FROM parent_links WHERE parent_id = @parent_id AND student_id = @student_id);

-- name: InsertManagedChild :one
INSERT INTO users (name, role, grade, managed_by, consent_at)
VALUES (@name, 'student', @grade, @managed_by, now()) RETURNING id;

-- name: InsertSettings :exec
INSERT INTO user_settings (user_id, onboarded) VALUES (@user_id, @onboarded);

-- name: GetSettings :one
SELECT remind_morning, remind_hour, reschedule_needs_parent, onboarded FROM user_settings WHERE user_id = $1;

-- name: UpsertSettings :exec
INSERT INTO user_settings (user_id, remind_morning, remind_hour, reschedule_needs_parent, onboarded)
VALUES (@user_id, @remind_morning, @remind_hour, @reschedule_needs_parent, @onboarded)
ON CONFLICT (user_id) DO UPDATE SET remind_morning = EXCLUDED.remind_morning, remind_hour = EXCLUDED.remind_hour,
    reschedule_needs_parent = EXCLUDED.reschedule_needs_parent, onboarded = EXCLUDED.onboarded;

-- name: InsertInvite :exec
INSERT INTO invites (code, kind, created_by, target_id, expires_at)
VALUES (@code, @kind, @created_by, sqlc.narg(target_id), @expires_at);

-- name: UseInvite :one
UPDATE invites SET used_at = now()
WHERE code = $1 AND used_at IS NULL AND expires_at > now()
RETURNING code, kind, created_by, target_id;

-- name: GetState :one
SELECT state, data FROM user_states WHERE user_id = $1;

-- name: UpsertState :exec
INSERT INTO user_states (user_id, state, data, updated_at) VALUES (@user_id, @state, @data, now())
ON CONFLICT (user_id) DO UPDATE SET state = EXCLUDED.state, data = EXCLUDED.data, updated_at = now();

-- name: DeleteState :exec
DELETE FROM user_states WHERE user_id = $1;
