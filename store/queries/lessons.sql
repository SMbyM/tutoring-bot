-- name: LessonByID :one
SELECT * FROM lesson_details WHERE id = $1;

-- Транзакционная блокировка расписания репетитора: две параллельные записи не займут одно окно.
-- name: LockTutor :exec
SELECT pg_advisory_xact_lock(@tutor_id::bigint);

-- name: BusyIntervals :many
SELECT starts_at, ends_at FROM lesson_details
WHERE status = 'scheduled' AND (tutor_id = @tutor_id OR student_id = @student_id) AND id <> @exclude_id
  AND starts_at < @to_time AND ends_at > @from_time;

-- name: InsertLesson :one
INSERT INTO lessons (student_id, tutor_id, subject_id, starts_at, duration_min, kind, recurring_slot_id)
VALUES (@student_id, @tutor_id, @subject_id, @starts_at, @duration_min, @kind, sqlc.narg(recurring_slot_id))
RETURNING id;

-- name: HasTrial :one
SELECT EXISTS (SELECT 1 FROM lessons
    WHERE student_id = @student_id AND tutor_id = @tutor_id AND kind = 'trial' AND status <> 'cancelled');

-- name: UpcomingForStudent :many
SELECT * FROM lesson_details
WHERE student_id = @student_id AND status = 'scheduled' AND ends_at > now()
ORDER BY starts_at LIMIT @max_rows;

-- name: UpcomingForTutor :many
SELECT * FROM lesson_details
WHERE tutor_id = @tutor_id AND status = 'scheduled' AND ends_at > now()
ORDER BY starts_at LIMIT @max_rows;

-- name: MoveLesson :exec
UPDATE lessons SET starts_at = @starts_at, late_cancel = late_cancel OR @late::boolean,
    cancel_reason = CASE WHEN @reason::text <> '' THEN @reason::text ELSE cancel_reason END,
    recurring_slot_id = NULL, mark_prompted = false
WHERE id = @id AND status = 'scheduled';

-- name: CancelLesson :execrows
UPDATE lessons SET status = 'cancelled', cancelled_by = @cancelled_by, cancel_reason = @cancel_reason, late_cancel = @late_cancel
WHERE id = @id AND status = 'scheduled';

-- name: SetLessonStatus :execrows
UPDATE lessons SET status = @status, marked_auto = @marked_auto WHERE id = @id AND status = 'scheduled';

-- name: LessonsToPrompt :many
SELECT * FROM lesson_details
WHERE status = 'scheduled' AND NOT mark_prompted AND ends_at <= @now
ORDER BY starts_at LIMIT 100;

-- name: ClaimPrompt :execrows
UPDATE lessons SET mark_prompted = true WHERE id = $1 AND NOT mark_prompted AND status = 'scheduled';

-- name: LessonsEndedBefore :many
SELECT * FROM lesson_details
WHERE status = 'scheduled' AND ends_at <= @before
ORDER BY starts_at LIMIT 100;

-- name: LessonsStartingBetween :many
SELECT * FROM lesson_details
WHERE status = 'scheduled' AND starts_at >= @from_time AND starts_at < @to_time
ORDER BY starts_at;

-- name: LogReminder :execrows
INSERT INTO reminder_log (lesson_id, user_id, kind) VALUES (@lesson_id, @user_id, @kind) ON CONFLICT DO NOTHING;

-- name: LateCancels :many
SELECT * FROM lesson_details WHERE late_cancel ORDER BY starts_at DESC LIMIT $1;

-- name: DebugShiftLesson :exec
UPDATE lessons SET starts_at = @starts_at, mark_prompted = false WHERE id = @id AND status = 'scheduled';

-- ---- постоянные слоты ----

-- name: InsertRecurring :one
INSERT INTO recurring_slots (student_id, tutor_id, subject_id, weekday, start_min, duration_min)
VALUES (@student_id, @tutor_id, @subject_id, @weekday, @start_min, @duration_min) RETURNING id;

-- name: ActiveRecurring :many
SELECT * FROM recurring_slots WHERE active ORDER BY id;

-- name: RecurringByID :one
SELECT * FROM recurring_slots WHERE id = $1;

-- name: DeactivateRecurring :exec
UPDATE recurring_slots SET active = false WHERE id = $1;

-- name: CancelFutureRecurringLessons :execrows
UPDATE lessons SET status = 'cancelled', cancelled_by = @cancelled_by, cancel_reason = @cancel_reason
WHERE recurring_slot_id = @slot_id AND status = 'scheduled' AND starts_at > now();

-- name: InsertRecurringLesson :execrows
INSERT INTO lessons (student_id, tutor_id, subject_id, starts_at, duration_min, kind, recurring_slot_id)
VALUES (@student_id, @tutor_id, @subject_id, @starts_at, @duration_min, 'regular', @recurring_slot_id)
ON CONFLICT DO NOTHING;

-- ---- запросы на перенос/отмену ----

-- name: InsertChangeRequest :one
INSERT INTO change_requests (lesson_id, requested_by, kind, new_starts_at, reason)
VALUES (@lesson_id, @requested_by, @kind, sqlc.narg(new_starts_at), @reason) RETURNING id;

-- name: ChangeRequestByID :one
SELECT * FROM change_requests WHERE id = $1;

-- name: DecideChangeRequest :execrows
UPDATE change_requests SET status = @status, decided_by = @decided_by WHERE id = @id AND status = 'pending';
