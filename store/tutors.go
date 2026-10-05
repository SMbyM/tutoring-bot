package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
)

type Subject struct {
	ID   int
	Name string
}

type Tutor struct {
	domain.User
	Bio           string
	LessonMinutes int
	PriceOverride *int64
	Active        bool
	Subjects      []Subject
}

func (t Tutor) Duration() time.Duration { return time.Duration(t.LessonMinutes) * time.Minute }

func (s *Store) Subjects(ctx context.Context) ([]Subject, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, name FROM subjects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subject
	for rows.Next() {
		var sub Subject
		if err := rows.Scan(&sub.ID, &sub.Name); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) AddSubject(ctx context.Context, name string) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO subjects(name) VALUES ($1) ON CONFLICT DO NOTHING`, name)
	return err
}

func (s *Store) EnsureTutor(ctx context.Context, userID int64) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO tutors(user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID)
	return err
}

const tutorCols = userCols + `, t.bio, t.lesson_minutes, t.price_override, t.active`

func (s *Store) scanTutors(ctx context.Context, q string, args ...any) ([]Tutor, error) {
	rows, err := s.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []Tutor
	for rows.Next() {
		var t Tutor
		var role, dbg string
		var price sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &role, &dbg, &t.Grade, &t.TZ, &t.ManagedBy, &t.Consent,
			&t.Bio, &t.LessonMinutes, &price, &t.Active); err != nil {
			rows.Close()
			return nil, err
		}
		t.Role, t.DebugRole = domain.Role(role), domain.Role(dbg)
		if price.Valid {
			p := price.Int64
			t.PriceOverride = &p
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		subs, err := s.tutorSubjects(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Subjects = subs
	}
	return out, nil
}

func (s *Store) tutorSubjects(ctx context.Context, tutorID int64) ([]Subject, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT s.id, s.name FROM subjects s JOIN tutor_subjects ts ON ts.subject_id=s.id
		WHERE ts.tutor_id=$1 ORDER BY s.id`, tutorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subject
	for rows.Next() {
		var sub Subject
		if err := rows.Scan(&sub.ID, &sub.Name); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) Tutor(ctx context.Context, id int64) (Tutor, error) {
	ts, err := s.scanTutors(ctx, `SELECT `+tutorCols+` FROM tutors t JOIN users u ON u.id=t.user_id WHERE t.user_id=$1`, id)
	if err != nil {
		return Tutor{}, err
	}
	if len(ts) == 0 {
		return Tutor{}, domain.ErrNotFound
	}
	return ts[0], nil
}

func (s *Store) TutorsBySubject(ctx context.Context, subjectID int) ([]Tutor, error) {
	return s.scanTutors(ctx, `SELECT `+tutorCols+` FROM tutors t JOIN users u ON u.id=t.user_id
		JOIN tutor_subjects ts ON ts.tutor_id=t.user_id WHERE ts.subject_id=$1 AND t.active ORDER BY u.name`, subjectID)
}

func (s *Store) AllTutors(ctx context.Context) ([]Tutor, error) {
	return s.scanTutors(ctx, `SELECT `+tutorCols+` FROM tutors t JOIN users u ON u.id=t.user_id ORDER BY u.name`)
}

func (s *Store) SetTutorBio(ctx context.Context, id int64, bio string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE tutors SET bio=$2 WHERE user_id=$1`, id, bio)
	return err
}

func (s *Store) SetTutorDuration(ctx context.Context, id int64, minutes int) error {
	_, err := s.q.ExecContext(ctx, `UPDATE tutors SET lesson_minutes=$2 WHERE user_id=$1`, id, minutes)
	return err
}

// SetTutorPrice — индивидуальная цена урока; nil сбрасывает к общей.
func (s *Store) SetTutorPrice(ctx context.Context, id int64, kopecks *int64) error {
	var v any
	if kopecks != nil {
		v = *kopecks
	}
	_, err := s.q.ExecContext(ctx, `UPDATE tutors SET price_override=$2 WHERE user_id=$1`, id, v)
	return err
}

func (s *Store) ToggleTutorSubject(ctx context.Context, tutorID int64, subjectID int) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM tutor_subjects WHERE tutor_id=$1 AND subject_id=$2`, tutorID, subjectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = s.q.ExecContext(ctx, `INSERT INTO tutor_subjects(tutor_id, subject_id) VALUES ($1,$2)`, tutorID, subjectID)
	}
	return err
}

// ---- окна и исключения ----

func (s *Store) Windows(ctx context.Context, tutorID int64) ([]domain.Window, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT weekday, start_min, end_min FROM tutor_windows WHERE tutor_id=$1 ORDER BY weekday, start_min`, tutorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Window
	for rows.Next() {
		var w domain.Window
		var wd int
		if err := rows.Scan(&wd, &w.StartMin, &w.EndMin); err != nil {
			return nil, err
		}
		w.Weekday = time.Weekday(wd)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceWindows(ctx context.Context, tutorID int64, ws []domain.Window) error {
	if err := domain.ValidateWindows(ws); err != nil {
		return err
	}
	return s.Tx(ctx, func(t *Store) error {
		if _, err := t.q.ExecContext(ctx, `DELETE FROM tutor_windows WHERE tutor_id=$1`, tutorID); err != nil {
			return err
		}
		for _, w := range ws {
			if _, err := t.q.ExecContext(ctx, `INSERT INTO tutor_windows(tutor_id, weekday, start_min, end_min) VALUES ($1,$2,$3,$4)`,
				tutorID, int(w.Weekday), w.StartMin, w.EndMin); err != nil {
				return err
			}
		}
		return nil
	})
}

type ExceptionRow struct {
	ID   int64
	From time.Time
	To   time.Time
	Note string
}

// Exceptions возвращает исключения, переведённые в полночь по поясу loc.
func (s *Store) Exceptions(ctx context.Context, tutorID int64, loc *time.Location) ([]ExceptionRow, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, to_char(from_date,'YYYY-MM-DD'), to_char(to_date,'YYYY-MM-DD'), note
		FROM tutor_exceptions WHERE tutor_id=$1 AND to_date >= current_date - 1 ORDER BY from_date`, tutorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExceptionRow
	for rows.Next() {
		var e ExceptionRow
		var f, t string
		if err := rows.Scan(&e.ID, &f, &t, &e.Note); err != nil {
			return nil, err
		}
		e.From, _ = time.ParseInLocation("2006-01-02", f, loc)
		e.To, _ = time.ParseInLocation("2006-01-02", t, loc)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AddException(ctx context.Context, tutorID int64, from, to time.Time, note string) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO tutor_exceptions(tutor_id, from_date, to_date, note) VALUES ($1,$2,$3,$4)`,
		tutorID, from.Format("2006-01-02"), to.Format("2006-01-02"), note)
	return err
}

func (s *Store) DeleteException(ctx context.Context, tutorID, id int64) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM tutor_exceptions WHERE tutor_id=$1 AND id=$2`, tutorID, id)
	return err
}

// ---- закрепление ученика за репетитором ----

type Enrollment struct {
	ID        int64
	StudentID int64
	TutorID   int64
	TutorName string
	SubjectID int
	Subject   string
}

func (s *Store) ActiveEnrollments(ctx context.Context, studentID int64) ([]Enrollment, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT e.id, e.student_id, e.tutor_id, u.name, e.subject_id, sb.name
		FROM enrollments e JOIN users u ON u.id=e.tutor_id JOIN subjects sb ON sb.id=e.subject_id
		WHERE e.student_id=$1 AND e.active ORDER BY e.id`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Enrollment
	for rows.Next() {
		var e Enrollment
		if err := rows.Scan(&e.ID, &e.StudentID, &e.TutorID, &e.TutorName, &e.SubjectID, &e.Subject); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) TutorStudents(ctx context.Context, tutorID int64) ([]domain.User, error) {
	return s.queryUsers(ctx, `SELECT DISTINCT `+userCols+` FROM users u JOIN enrollments e ON e.student_id=u.id
		WHERE e.tutor_id=$1 AND e.active ORDER BY u.id`, tutorID)
}

func (s *Store) IsEnrolled(ctx context.Context, studentID, tutorID int64, subjectID int) (bool, error) {
	var ok bool
	err := s.q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM enrollments WHERE student_id=$1 AND tutor_id=$2 AND subject_id=$3 AND active)`,
		studentID, tutorID, subjectID).Scan(&ok)
	return ok, err
}

// Enroll закрепляет ученика за репетитором по предмету. Прежний репетитор по этому предмету
// открепляется: его постоянные слоты закрываются, будущие уроки по ним отменяются. История остаётся.
// Возвращает id прежнего репетитора (0, если не было).
func (s *Store) Enroll(ctx context.Context, studentID, tutorID int64, subjectID int) (int64, error) {
	var prev int64
	err := s.Tx(ctx, func(t *Store) error {
		err := t.q.QueryRowContext(ctx, `UPDATE enrollments SET active=false, ended_at=now()
			WHERE student_id=$1 AND subject_id=$2 AND active AND tutor_id<>$3 RETURNING tutor_id`, studentID, subjectID, tutorID).Scan(&prev)
		if err != nil && !notFound(err) {
			return err
		}
		if prev != 0 {
			if _, err := t.q.ExecContext(ctx, `UPDATE lessons SET status='cancelled', cancel_reason='смена репетитора'
				WHERE status='scheduled' AND starts_at > now() AND recurring_slot_id IN (
					SELECT id FROM recurring_slots WHERE student_id=$1 AND tutor_id=$2 AND subject_id=$3 AND active)`,
				studentID, prev, subjectID); err != nil {
				return err
			}
			if _, err := t.q.ExecContext(ctx, `UPDATE recurring_slots SET active=false
				WHERE student_id=$1 AND tutor_id=$2 AND subject_id=$3`, studentID, prev, subjectID); err != nil {
				return err
			}
		}
		_, err = t.q.ExecContext(ctx, `INSERT INTO enrollments(student_id, tutor_id, subject_id) VALUES ($1,$2,$3)
			ON CONFLICT (student_id, subject_id) WHERE active DO NOTHING`, studentID, tutorID, subjectID)
		return err
	})
	return prev, err
}
