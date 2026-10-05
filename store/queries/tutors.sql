-- name: ListSubjects :many
SELECT * FROM subjects ORDER BY id;

-- name: InsertSubject :exec
INSERT INTO subjects (name) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: EnsureTutor :exec
INSERT INTO tutors (user_id) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: TutorProfile :one
SELECT * FROM tutor_profiles WHERE id = $1;

-- name: TutorProfilesBySubject :many
SELECT tp.* FROM tutor_profiles tp
JOIN tutor_subjects ts ON ts.tutor_id = tp.id
WHERE ts.subject_id = $1 AND tp.active
ORDER BY tp.name;

-- name: AllTutorProfiles :many
SELECT * FROM tutor_profiles ORDER BY name;

-- name: TutorSubjects :many
SELECT s.* FROM subjects s JOIN tutor_subjects ts ON ts.subject_id = s.id
WHERE ts.tutor_id = $1 ORDER BY s.id;

-- name: SetTutorBio :exec
UPDATE tutors SET bio = @bio WHERE user_id = @user_id;

-- name: SetTutorDuration :exec
UPDATE tutors SET lesson_minutes = @lesson_minutes WHERE user_id = @user_id;

-- name: SetTutorPrice :exec
UPDATE tutors SET price_override = sqlc.narg(price_override) WHERE user_id = @user_id;

-- name: DeleteTutorSubject :execrows
DELETE FROM tutor_subjects WHERE tutor_id = @tutor_id AND subject_id = @subject_id;

-- name: InsertTutorSubject :exec
INSERT INTO tutor_subjects (tutor_id, subject_id) VALUES (@tutor_id, @subject_id);

-- name: ListWindows :many
SELECT weekday, start_min, end_min FROM tutor_windows WHERE tutor_id = $1 ORDER BY weekday, start_min;

-- name: DeleteWindows :exec
DELETE FROM tutor_windows WHERE tutor_id = $1;

-- name: InsertWindow :exec
INSERT INTO tutor_windows (tutor_id, weekday, start_min, end_min) VALUES (@tutor_id, @weekday, @start_min, @end_min);

-- Даты исключений передаются текстом ГГГГ-ММ-ДД: так они не зависят от часового пояса сессии БД.

-- name: ListExceptions :many
SELECT id, to_char(from_date, 'YYYY-MM-DD')::text AS from_day, to_char(to_date, 'YYYY-MM-DD')::text AS to_day, note
FROM tutor_exceptions WHERE tutor_id = $1 AND to_date >= current_date - 1 ORDER BY from_date;

-- name: InsertException :exec
INSERT INTO tutor_exceptions (tutor_id, from_date, to_date, note)
VALUES (@tutor_id, (@from_day::text)::date, (@to_day::text)::date, @note);

-- name: DeleteException :exec
DELETE FROM tutor_exceptions WHERE tutor_id = @tutor_id AND id = @id;

-- name: ActiveEnrollments :many
SELECT e.id, e.student_id, e.tutor_id, u.name AS tutor_name, e.subject_id, sb.name AS subject
FROM enrollments e JOIN users u ON u.id = e.tutor_id JOIN subjects sb ON sb.id = e.subject_id
WHERE e.student_id = $1 AND e.active ORDER BY e.id;

-- name: TutorStudents :many
SELECT DISTINCT u.* FROM users u JOIN enrollments e ON e.student_id = u.id
WHERE e.tutor_id = $1 AND e.active ORDER BY u.id;

-- name: IsEnrolled :one
SELECT EXISTS (SELECT 1 FROM enrollments
    WHERE student_id = @student_id AND tutor_id = @tutor_id AND subject_id = @subject_id AND active);

-- name: EndOtherEnrollment :one
UPDATE enrollments SET active = false, ended_at = now()
WHERE student_id = @student_id AND subject_id = @subject_id AND active AND tutor_id <> @tutor_id
RETURNING tutor_id;

-- name: CancelFutureRecurringOfTutor :exec
UPDATE lessons SET status = 'cancelled', cancel_reason = 'смена репетитора'
WHERE status = 'scheduled' AND starts_at > now() AND recurring_slot_id IN (
    SELECT rs.id FROM recurring_slots rs
    WHERE rs.student_id = @student_id AND rs.tutor_id = @tutor_id AND rs.subject_id = @subject_id AND rs.active);

-- name: DeactivateRecurringOfTutor :exec
UPDATE recurring_slots SET active = false
WHERE student_id = @student_id AND tutor_id = @tutor_id AND subject_id = @subject_id;

-- name: InsertEnrollment :exec
INSERT INTO enrollments (student_id, tutor_id, subject_id) VALUES (@student_id, @tutor_id, @subject_id)
ON CONFLICT DO NOTHING; -- единственный уникальный индекс — «одно активное закрепление на предмет»
