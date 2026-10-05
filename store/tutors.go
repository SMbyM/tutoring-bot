package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store/db"
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

func subjectsFromDB(ss []db.Subject, err error) ([]Subject, error) {
	if err != nil {
		return nil, err
	}
	out := make([]Subject, len(ss))
	for i, s := range ss {
		out[i] = Subject{ID: int(s.ID), Name: s.Name}
	}
	return out, nil
}

func (s *Store) Subjects(ctx context.Context) ([]Subject, error) {
	return subjectsFromDB(s.qs().ListSubjects(ctx))
}

func (s *Store) AddSubject(ctx context.Context, name string) error {
	return s.qs().InsertSubject(ctx, name)
}

func (s *Store) EnsureTutor(ctx context.Context, userID int64) error {
	return s.qs().EnsureTutor(ctx, userID)
}

// tutors дополняет профили предметами.
func (s *Store) tutors(ctx context.Context, ps []db.TutorProfile, err error) ([]Tutor, error) {
	if err != nil {
		return nil, err
	}
	out := make([]Tutor, len(ps))
	for i, p := range ps {
		t := Tutor{
			User: domain.User{ID: p.ID, Name: p.Name, Role: domain.Role(p.Role), DebugRole: domain.Role(p.DebugRole.String),
				Grade: int(p.Grade.Int16), TZ: p.Tz, ManagedBy: p.ManagedBy.Int64, Consent: p.ConsentAt.Valid},
			Bio: p.Bio, LessonMinutes: int(p.LessonMinutes), Active: p.Active,
		}
		if p.PriceOverride.Valid {
			v := p.PriceOverride.Int64
			t.PriceOverride = &v
		}
		if t.Subjects, err = subjectsFromDB(s.qs().TutorSubjects(ctx, p.ID)); err != nil {
			return nil, err
		}
		out[i] = t
	}
	return out, nil
}

func (s *Store) Tutor(ctx context.Context, id int64) (Tutor, error) {
	p, err := s.qs().TutorProfile(ctx, id)
	if notFound(err) {
		return Tutor{}, domain.ErrNotFound
	}
	ts, err := s.tutors(ctx, []db.TutorProfile{p}, err)
	if err != nil {
		return Tutor{}, err
	}
	return ts[0], nil
}

func (s *Store) TutorsBySubject(ctx context.Context, subjectID int) ([]Tutor, error) {
	ps, err := s.qs().TutorProfilesBySubject(ctx, int32(subjectID))
	return s.tutors(ctx, ps, err)
}

func (s *Store) AllTutors(ctx context.Context) ([]Tutor, error) {
	ps, err := s.qs().AllTutorProfiles(ctx)
	return s.tutors(ctx, ps, err)
}

func (s *Store) SetTutorBio(ctx context.Context, id int64, bio string) error {
	return s.qs().SetTutorBio(ctx, db.SetTutorBioParams{UserID: id, Bio: bio})
}

func (s *Store) SetTutorDuration(ctx context.Context, id int64, minutes int) error {
	return s.qs().SetTutorDuration(ctx, db.SetTutorDurationParams{UserID: id, LessonMinutes: int32(minutes)})
}

// SetTutorPrice — индивидуальная цена урока; nil сбрасывает к общей.
func (s *Store) SetTutorPrice(ctx context.Context, id int64, kopecks *int64) error {
	p := db.SetTutorPriceParams{UserID: id}
	if kopecks != nil {
		p.PriceOverride = sql.NullInt64{Int64: *kopecks, Valid: true}
	}
	return s.qs().SetTutorPrice(ctx, p)
}

func (s *Store) ToggleTutorSubject(ctx context.Context, tutorID int64, subjectID int) error {
	n, err := s.qs().DeleteTutorSubject(ctx, db.DeleteTutorSubjectParams{TutorID: tutorID, SubjectID: int32(subjectID)})
	if err != nil || n > 0 {
		return err
	}
	return s.qs().InsertTutorSubject(ctx, db.InsertTutorSubjectParams{TutorID: tutorID, SubjectID: int32(subjectID)})
}

// ---- окна и исключения ----

func (s *Store) Windows(ctx context.Context, tutorID int64) ([]domain.Window, error) {
	rows, err := s.qs().ListWindows(ctx, tutorID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Window, len(rows))
	for i, r := range rows {
		out[i] = domain.Window{Weekday: time.Weekday(r.Weekday), StartMin: int(r.StartMin), EndMin: int(r.EndMin)}
	}
	return out, nil
}

func (s *Store) ReplaceWindows(ctx context.Context, tutorID int64, ws []domain.Window) error {
	if err := domain.ValidateWindows(ws); err != nil {
		return err
	}
	return s.Tx(ctx, func(t *Store) error {
		q := t.qs()
		if err := q.DeleteWindows(ctx, tutorID); err != nil {
			return err
		}
		for _, w := range ws {
			if err := q.InsertWindow(ctx, db.InsertWindowParams{TutorID: tutorID, Weekday: int16(w.Weekday),
				StartMin: int32(w.StartMin), EndMin: int32(w.EndMin)}); err != nil {
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
	rows, err := s.qs().ListExceptions(ctx, tutorID)
	if err != nil {
		return nil, err
	}
	out := make([]ExceptionRow, len(rows))
	for i, r := range rows {
		from, _ := time.ParseInLocation("2006-01-02", r.FromDay, loc)
		to, _ := time.ParseInLocation("2006-01-02", r.ToDay, loc)
		out[i] = ExceptionRow{ID: r.ID, From: from, To: to, Note: r.Note}
	}
	return out, nil
}

func (s *Store) AddException(ctx context.Context, tutorID int64, from, to time.Time, note string) error {
	return s.qs().InsertException(ctx, db.InsertExceptionParams{TutorID: tutorID,
		FromDay: from.Format("2006-01-02"), ToDay: to.Format("2006-01-02"), Note: note})
}

func (s *Store) DeleteException(ctx context.Context, tutorID, id int64) error {
	return s.qs().DeleteException(ctx, db.DeleteExceptionParams{TutorID: tutorID, ID: id})
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
	rows, err := s.qs().ActiveEnrollments(ctx, studentID)
	if err != nil {
		return nil, err
	}
	out := make([]Enrollment, len(rows))
	for i, r := range rows {
		out[i] = Enrollment{ID: r.ID, StudentID: r.StudentID, TutorID: r.TutorID, TutorName: r.TutorName,
			SubjectID: int(r.SubjectID), Subject: r.Subject}
	}
	return out, nil
}

func (s *Store) TutorStudents(ctx context.Context, tutorID int64) ([]domain.User, error) {
	return usersFromDB(s.qs().TutorStudents(ctx, tutorID))
}

func (s *Store) IsEnrolled(ctx context.Context, studentID, tutorID int64, subjectID int) (bool, error) {
	return s.qs().IsEnrolled(ctx, db.IsEnrolledParams{StudentID: studentID, TutorID: tutorID, SubjectID: int32(subjectID)})
}

// Enroll закрепляет ученика за репетитором по предмету. Прежний репетитор по этому предмету
// открепляется: его постоянные слоты закрываются, будущие уроки по ним отменяются. История остаётся.
// Возвращает id прежнего репетитора (0, если не было).
func (s *Store) Enroll(ctx context.Context, studentID, tutorID int64, subjectID int) (int64, error) {
	var prev int64
	err := s.Tx(ctx, func(t *Store) error {
		q := t.qs()
		sid := int32(subjectID)
		var err error
		prev, err = q.EndOtherEnrollment(ctx, db.EndOtherEnrollmentParams{StudentID: studentID, SubjectID: sid, TutorID: tutorID})
		if err != nil && !notFound(err) {
			return err
		}
		if prev != 0 {
			if err := q.CancelFutureRecurringOfTutor(ctx, db.CancelFutureRecurringOfTutorParams{
				StudentID: studentID, TutorID: prev, SubjectID: sid}); err != nil {
				return err
			}
			if err := q.DeactivateRecurringOfTutor(ctx, db.DeactivateRecurringOfTutorParams{
				StudentID: studentID, TutorID: prev, SubjectID: sid}); err != nil {
				return err
			}
		}
		return q.InsertEnrollment(ctx, db.InsertEnrollmentParams{StudentID: studentID, TutorID: tutorID, SubjectID: sid})
	})
	return prev, err
}
