package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
)

const lessonSelect = `SELECT l.id, l.student_id, l.tutor_id, l.subject_id, sb.name, su.name, tu.name,
	l.starts_at, l.duration_min, l.kind, l.status, COALESCE(l.recurring_slot_id,0), l.late_cancel, l.cancel_reason
	FROM lessons l JOIN subjects sb ON sb.id=l.subject_id JOIN users su ON su.id=l.student_id JOIN users tu ON tu.id=l.tutor_id `

func scanLesson(sc interface{ Scan(...any) error }) (domain.Lesson, error) {
	var l domain.Lesson
	var mins int
	var kind, status string
	err := sc.Scan(&l.ID, &l.StudentID, &l.TutorID, &l.SubjectID, &l.Subject, &l.StudentName, &l.TutorName,
		&l.StartsAt, &mins, &kind, &status, &l.RecurringID, &l.LateCancel, &l.Reason)
	l.Duration = time.Duration(mins) * time.Minute
	l.Kind, l.Status = domain.LessonKind(kind), domain.LessonStatus(status)
	return l, err
}

func (s *Store) lessons(ctx context.Context, where string, args ...any) ([]domain.Lesson, error) {
	rows, err := s.q.QueryContext(ctx, lessonSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Lesson
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Lesson(ctx context.Context, id int64) (domain.Lesson, error) {
	l, err := scanLesson(s.q.QueryRowContext(ctx, lessonSelect+`WHERE l.id=$1`, id))
	if notFound(err) {
		return l, domain.ErrNotFound
	}
	return l, err
}

// LockTutor берёт транзакционную advisory-блокировку на расписание репетитора,
// чтобы две параллельные записи не заняли одно окно.
func (s *Store) LockTutor(ctx context.Context, tutorID int64) error {
	_, err := s.q.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::bigint)`, tutorID)
	return err
}

// Busy — запланированные уроки репетитора или ученика, пересекающие [from, to). excludeID не учитывается.
func (s *Store) Busy(ctx context.Context, tutorID, studentID int64, from, to time.Time, excludeID int64) ([]domain.Interval, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT starts_at, starts_at + make_interval(mins => duration_min) FROM lessons
		WHERE status='scheduled' AND (tutor_id=$1 OR student_id=$2) AND id<>$5
		AND starts_at < $4 AND starts_at + make_interval(mins => duration_min) > $3`, tutorID, studentID, from, to, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Interval
	for rows.Next() {
		var iv domain.Interval
		if err := rows.Scan(&iv.Start, &iv.End); err != nil {
			return nil, err
		}
		out = append(out, iv)
	}
	return out, rows.Err()
}

func (s *Store) InsertLesson(ctx context.Context, l domain.Lesson) (int64, error) {
	var rec any
	if l.RecurringID != 0 {
		rec = l.RecurringID
	}
	var id int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO lessons(student_id, tutor_id, subject_id, starts_at, duration_min, kind, recurring_slot_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, l.StudentID, l.TutorID, l.SubjectID, l.StartsAt,
		int(l.Duration/time.Minute), string(l.Kind), rec).Scan(&id)
	return id, err
}

func (s *Store) HasTrial(ctx context.Context, studentID, tutorID int64) (bool, error) {
	var ok bool
	err := s.q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lessons WHERE student_id=$1 AND tutor_id=$2 AND kind='trial' AND status<>'cancelled')`,
		studentID, tutorID).Scan(&ok)
	return ok, err
}

func (s *Store) UpcomingForStudent(ctx context.Context, studentID int64, limit int) ([]domain.Lesson, error) {
	return s.lessons(ctx, `WHERE l.student_id=$1 AND l.status='scheduled' AND l.starts_at + make_interval(mins => l.duration_min) > now()
		ORDER BY l.starts_at LIMIT $2`, studentID, limit)
}

func (s *Store) UpcomingForTutor(ctx context.Context, tutorID int64, limit int) ([]domain.Lesson, error) {
	return s.lessons(ctx, `WHERE l.tutor_id=$1 AND l.status='scheduled' AND l.starts_at + make_interval(mins => l.duration_min) > now()
		ORDER BY l.starts_at LIMIT $2`, tutorID, limit)
}

func (s *Store) MoveLesson(ctx context.Context, id int64, start time.Time, late bool, reason string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE lessons SET starts_at=$2, late_cancel = late_cancel OR $3,
		cancel_reason = CASE WHEN $4 <> '' THEN $4 ELSE cancel_reason END, recurring_slot_id=NULL,
		mark_prompted=false WHERE id=$1 AND status='scheduled'`, id, start, late, reason)
	return err
}

func (s *Store) CancelLesson(ctx context.Context, id, by int64, reason string, late bool) error {
	res, err := s.q.ExecContext(ctx, `UPDATE lessons SET status='cancelled', cancelled_by=$2, cancel_reason=$3, late_cancel=$4
		WHERE id=$1 AND status='scheduled'`, id, by, reason, late)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotScheduled
	}
	return nil
}

// SetStatus переводит урок из «запланирован» в итоговый статус. ok=false, если урок уже отмечен.
func (s *Store) SetStatus(ctx context.Context, id int64, st domain.LessonStatus, auto bool) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE lessons SET status=$2, marked_auto=$3 WHERE id=$1 AND status='scheduled'`, id, string(st), auto)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// LessonsToPrompt — закончившиеся, но не отмеченные уроки, по которым ещё не спрашивали репетитора.
func (s *Store) LessonsToPrompt(ctx context.Context, now time.Time) ([]domain.Lesson, error) {
	return s.lessons(ctx, `WHERE l.status='scheduled' AND NOT l.mark_prompted
		AND l.starts_at + make_interval(mins => l.duration_min) <= $1 ORDER BY l.starts_at LIMIT 100`, now)
}

func (s *Store) MarkPrompted(ctx context.Context, id int64) error {
	_, err := s.q.ExecContext(ctx, `UPDATE lessons SET mark_prompted=true WHERE id=$1`, id)
	return err
}

func (s *Store) LessonsToAutoHold(ctx context.Context, now time.Time) ([]domain.Lesson, error) {
	return s.lessons(ctx, `WHERE l.status='scheduled' AND l.starts_at + make_interval(mins => l.duration_min) <= $1
		ORDER BY l.starts_at LIMIT 100`, now.Add(-domain.AutoHeldAfter))
}

func (s *Store) LessonsStartingBetween(ctx context.Context, from, to time.Time) ([]domain.Lesson, error) {
	return s.lessons(ctx, `WHERE l.status='scheduled' AND l.starts_at >= $1 AND l.starts_at < $2 ORDER BY l.starts_at`, from, to)
}

// TryLogReminder возвращает true, если напоминание этого вида этому пользователю ещё не отправлялось.
func (s *Store) TryLogReminder(ctx context.Context, lessonID, userID int64, kind string) (bool, error) {
	res, err := s.q.ExecContext(ctx, `INSERT INTO reminder_log(lesson_id, user_id, kind) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
		lessonID, userID, kind)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// LateCancels — последние поздние отмены/переносы (для админа).
func (s *Store) LateCancels(ctx context.Context, limit int) ([]domain.Lesson, error) {
	return s.lessons(ctx, `WHERE l.late_cancel ORDER BY l.starts_at DESC LIMIT $1`, limit)
}

// ---- постоянные слоты ----

type Recurring struct {
	ID        int64
	StudentID int64
	TutorID   int64
	SubjectID int
	Weekday   time.Weekday
	StartMin  int
	Duration  time.Duration
}

func (s *Store) InsertRecurring(ctx context.Context, r Recurring) (int64, error) {
	var id int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO recurring_slots(student_id, tutor_id, subject_id, weekday, start_min, duration_min)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, r.StudentID, r.TutorID, r.SubjectID, int(r.Weekday), r.StartMin,
		int(r.Duration/time.Minute)).Scan(&id)
	return id, err
}

func (s *Store) ActiveRecurring(ctx context.Context) ([]Recurring, error) {
	return s.recurring(ctx, `WHERE active`)
}

func (s *Store) RecurringByID(ctx context.Context, id int64) (Recurring, error) {
	rs, err := s.recurring(ctx, `WHERE id=$1`, id)
	if err != nil {
		return Recurring{}, err
	}
	if len(rs) == 0 {
		return Recurring{}, domain.ErrNotFound
	}
	return rs[0], nil
}

func (s *Store) recurring(ctx context.Context, where string, args ...any) ([]Recurring, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, student_id, tutor_id, subject_id, weekday, start_min, duration_min FROM recurring_slots `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recurring
	for rows.Next() {
		var r Recurring
		var wd, mins int
		if err := rows.Scan(&r.ID, &r.StudentID, &r.TutorID, &r.SubjectID, &wd, &r.StartMin, &mins); err != nil {
			return nil, err
		}
		r.Weekday, r.Duration = time.Weekday(wd), time.Duration(mins)*time.Minute
		out = append(out, r)
	}
	return out, rows.Err()
}

// StopRecurring закрывает постоянный слот и отменяет его будущие уроки. Возвращает число отменённых.
func (s *Store) StopRecurring(ctx context.Context, id, by int64, reason string) (int, error) {
	var n int64
	err := s.Tx(ctx, func(t *Store) error {
		if _, err := t.q.ExecContext(ctx, `UPDATE recurring_slots SET active=false WHERE id=$1`, id); err != nil {
			return err
		}
		res, err := t.q.ExecContext(ctx, `UPDATE lessons SET status='cancelled', cancelled_by=$2, cancel_reason=$3
			WHERE recurring_slot_id=$1 AND status='scheduled' AND starts_at > now()`, id, by, reason)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// InsertRecurringLesson создаёт урок постоянного слота, если такого ещё нет. ok=false — уже был.
func (s *Store) InsertRecurringLesson(ctx context.Context, l domain.Lesson) (bool, error) {
	res, err := s.q.ExecContext(ctx, `INSERT INTO lessons(student_id, tutor_id, subject_id, starts_at, duration_min, kind, recurring_slot_id)
		VALUES ($1,$2,$3,$4,$5,'regular',$6) ON CONFLICT DO NOTHING`, l.StudentID, l.TutorID, l.SubjectID, l.StartsAt,
		int(l.Duration/time.Minute), l.RecurringID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- запросы на перенос/отмену ----

type ChangeRequest struct {
	ID          int64
	LessonID    int64
	RequestedBy int64
	Kind        string // reschedule | cancel
	NewStart    *time.Time
	Reason      string
	Status      string
}

func (s *Store) InsertChangeRequest(ctx context.Context, r ChangeRequest) (int64, error) {
	var id int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO change_requests(lesson_id, requested_by, kind, new_starts_at, reason)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`, r.LessonID, r.RequestedBy, r.Kind, r.NewStart, r.Reason).Scan(&id)
	return id, err
}

func (s *Store) ChangeRequest(ctx context.Context, id int64) (ChangeRequest, error) {
	var r ChangeRequest
	var ns sql.NullTime
	err := s.q.QueryRowContext(ctx, `SELECT id, lesson_id, requested_by, kind, new_starts_at, reason, status FROM change_requests WHERE id=$1`, id).
		Scan(&r.ID, &r.LessonID, &r.RequestedBy, &r.Kind, &ns, &r.Reason, &r.Status)
	if notFound(err) {
		return r, domain.ErrNotFound
	}
	if ns.Valid {
		r.NewStart = &ns.Time
	}
	return r, err
}

// DecideChangeRequest — ok=false, если запрос уже решён (например, вторым родителем).
func (s *Store) DecideChangeRequest(ctx context.Context, id int64, approved bool, by int64) (bool, error) {
	st := "declined"
	if approved {
		st = "approved"
	}
	res, err := s.q.ExecContext(ctx, `UPDATE change_requests SET status=$2, decided_by=$3 WHERE id=$1 AND status='pending'`, id, st, by)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DebugShiftLesson двигает урок в прошлое (только для debug-режима: проверить отметки и отзывы без ожидания).
func (s *Store) DebugShiftLesson(ctx context.Context, id int64, start time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE lessons SET starts_at=$2, mark_prompted=false WHERE id=$1 AND status='scheduled'`, id, start)
	return err
}
