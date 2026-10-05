-- Представления для выборок с соединениями. С ними sqlc отдаёт одну модель
-- (LessonDetail, TutorProfile, FeedbackDetail) на все запросы, а не новый тип на каждый.

CREATE VIEW lesson_details AS
SELECT l.*,
       sb.name AS subject,
       su.name AS student_name,
       tu.name AS tutor_name,
       (l.starts_at + make_interval(mins => l.duration_min))::timestamptz AS ends_at
FROM lessons l
JOIN subjects sb ON sb.id = l.subject_id
JOIN users su ON su.id = l.student_id
JOIN users tu ON tu.id = l.tutor_id;

CREATE VIEW tutor_profiles AS
SELECT u.id, u.name, u.role, u.debug_role, u.grade, u.tz, u.managed_by, u.consent_at,
       t.bio, t.lesson_minutes, t.price_override, t.active
FROM tutors t
JOIN users u ON u.id = t.user_id;

CREATE VIEW feedback_details AS
SELECT f.*,
       su.name AS student_name,
       tu.name AS tutor_name
FROM trial_feedback f
JOIN users su ON su.id = f.student_id
JOIN users tu ON tu.id = f.tutor_id;
