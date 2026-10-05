package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store/db"
)

func lessonFromDB(l db.LessonDetail) domain.Lesson {
	return domain.Lesson{
		ID: l.ID, StudentID: l.StudentID, TutorID: l.TutorID, SubjectID: int(l.SubjectID),
		Subject: l.Subject, StudentName: l.StudentName, TutorName: l.TutorName,
		StartsAt: l.StartsAt, Duration: time.Duration(l.DurationMin) * time.Minute,
		Kind: domain.LessonKind(l.Kind), Status: domain.LessonStatus(l.Status),
		RecurringID: l.RecurringSlotID.Int64, LateCancel: l.LateCancel, Reason: l.CancelReason,
	}
}

func lessonsFromDB(ls []db.LessonDetail, err error) ([]domain.Lesson, error) {
	if err != nil {
		return nil, err
	}
	out := make([]domain.Lesson, len(ls))
	for i, l := range ls {
		out[i] = lessonFromDB(l)
	}
	return out, nil
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func minutes(d time.Duration) int32 { return int32(d / time.Minute) }

func (s *Store) Lesson(ctx context.Context, id int64) (domain.Lesson, error) {
	l, err := s.qs().LessonByID(ctx, id)
	if notFound(err) {
		return domain.Lesson{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Lesson{}, err
	}
	return lessonFromDB(l), nil
}

// LockTutor берёт транзакционную advisory-блокировку на расписание репетитора,
// чтобы две параллельные записи не заняли одно окно.
func (s *Store) LockTutor(ctx context.Context, tutorID int64) error {
	return s.qs().LockTutor(ctx, tutorID)
}

// Busy — запланированные уроки репетитора или ученика, пересекающие [from, to). excludeID не учитывается.
func (s *Store) Busy(ctx context.Context, tutorID, studentID int64, from, to time.Time, excludeID int64) ([]domain.Interval, error) {
	rows, err := s.qs().BusyIntervals(ctx, db.BusyIntervalsParams{TutorID: tutorID, StudentID: studentID,
		ExcludeID: excludeID, FromTime: from, ToTime: to})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Interval, len(rows))
	for i, r := range rows {
		out[i] = domain.Interval{Start: r.StartsAt, End: r.EndsAt}
	}
	return out, nil
}

func (s *Store) InsertLesson(ctx context.Context, l domain.Lesson) (int64, error) {
	return s.qs().InsertLesson(ctx, db.InsertLessonParams{StudentID: l.StudentID, TutorID: l.TutorID, SubjectID: int32(l.SubjectID),
		StartsAt: l.StartsAt, DurationMin: minutes(l.Duration), Kind: string(l.Kind), RecurringSlotID: nullID(l.RecurringID)})
}

func (s *Store) HasTrial(ctx context.Context, studentID, tutorID int64) (bool, error) {
	return s.qs().HasTrial(ctx, db.HasTrialParams{StudentID: studentID, TutorID: tutorID})
}

func (s *Store) UpcomingForStudent(ctx context.Context, studentID int64, limit int) ([]domain.Lesson, error) {
	return lessonsFromDB(s.qs().UpcomingForStudent(ctx, db.UpcomingForStudentParams{StudentID: studentID, MaxRows: int32(limit)}))
}

func (s *Store) UpcomingForTutor(ctx context.Context, tutorID int64, limit int) ([]domain.Lesson, error) {
	return lessonsFromDB(s.qs().UpcomingForTutor(ctx, db.UpcomingForTutorParams{TutorID: tutorID, MaxRows: int32(limit)}))
}

func (s *Store) MoveLesson(ctx context.Context, id int64, start time.Time, late bool, reason string) error {
	return s.qs().MoveLesson(ctx, db.MoveLessonParams{ID: id, StartsAt: start, Late: late, Reason: reason})
}

func (s *Store) CancelLesson(ctx context.Context, id, by int64, reason string, late bool) error {
	n, err := s.qs().CancelLesson(ctx, db.CancelLessonParams{ID: id, CancelledBy: nullID(by), CancelReason: reason, LateCancel: late})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotScheduled
	}
	return nil
}

// SetStatus переводит урок из «запланирован» в итоговый статус. ok=false, если урок уже отмечен.
func (s *Store) SetStatus(ctx context.Context, id int64, st domain.LessonStatus, auto bool) (bool, error) {
	n, err := s.qs().SetLessonStatus(ctx, db.SetLessonStatusParams{ID: id, Status: string(st), MarkedAuto: auto})
	return n == 1, err
}

// LessonsToPrompt — закончившиеся, но не отмеченные уроки, по которым ещё не спрашивали репетитора.
func (s *Store) LessonsToPrompt(ctx context.Context, now time.Time) ([]domain.Lesson, error) {
	return lessonsFromDB(s.qs().LessonsToPrompt(ctx, now))
}

// ClaimPrompt атомарно помечает, что репетитора спросили про урок. ok=false — уже спросил другой процесс.
func (s *Store) ClaimPrompt(ctx context.Context, id int64) (bool, error) {
	n, err := s.qs().ClaimPrompt(ctx, id)
	return n == 1, err
}

func (s *Store) LessonsToAutoHold(ctx context.Context, now time.Time) ([]domain.Lesson, error) {
	return lessonsFromDB(s.qs().LessonsEndedBefore(ctx, now.Add(-domain.AutoHeldAfter)))
}

func (s *Store) LessonsStartingBetween(ctx context.Context, from, to time.Time) ([]domain.Lesson, error) {
	return lessonsFromDB(s.qs().LessonsStartingBetween(ctx, db.LessonsStartingBetweenParams{FromTime: from, ToTime: to}))
}

// TryLogReminder возвращает true, если напоминание этого вида этому пользователю ещё не отправлялось.
func (s *Store) TryLogReminder(ctx context.Context, lessonID, userID int64, kind string) (bool, error) {
	n, err := s.qs().LogReminder(ctx, db.LogReminderParams{LessonID: lessonID, UserID: userID, Kind: kind})
	return n == 1, err
}

// LateCancels — последние поздние отмены/переносы (для админа).
func (s *Store) LateCancels(ctx context.Context, limit int) ([]domain.Lesson, error) {
	return lessonsFromDB(s.qs().LateCancels(ctx, int32(limit)))
}

// DebugShiftLesson двигает урок в прошлое (только для debug-режима: проверить отметки и отзывы без ожидания).
func (s *Store) DebugShiftLesson(ctx context.Context, id int64, start time.Time) error {
	return s.qs().DebugShiftLesson(ctx, db.DebugShiftLessonParams{ID: id, StartsAt: start})
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

func recurringFromDB(r db.RecurringSlot) Recurring {
	return Recurring{ID: r.ID, StudentID: r.StudentID, TutorID: r.TutorID, SubjectID: int(r.SubjectID),
		Weekday: time.Weekday(r.Weekday), StartMin: int(r.StartMin), Duration: time.Duration(r.DurationMin) * time.Minute}
}

func (s *Store) InsertRecurring(ctx context.Context, r Recurring) (int64, error) {
	return s.qs().InsertRecurring(ctx, db.InsertRecurringParams{StudentID: r.StudentID, TutorID: r.TutorID,
		SubjectID: int32(r.SubjectID), Weekday: int16(r.Weekday), StartMin: int32(r.StartMin), DurationMin: minutes(r.Duration)})
}

func (s *Store) ActiveRecurring(ctx context.Context) ([]Recurring, error) {
	rows, err := s.qs().ActiveRecurring(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Recurring, len(rows))
	for i, r := range rows {
		out[i] = recurringFromDB(r)
	}
	return out, nil
}

func (s *Store) RecurringByID(ctx context.Context, id int64) (Recurring, error) {
	r, err := s.qs().RecurringByID(ctx, id)
	if notFound(err) {
		return Recurring{}, domain.ErrNotFound
	}
	if err != nil {
		return Recurring{}, err
	}
	return recurringFromDB(r), nil
}

// StopRecurring закрывает постоянный слот и отменяет его будущие уроки. Возвращает число отменённых.
func (s *Store) StopRecurring(ctx context.Context, id, by int64, reason string) (int, error) {
	var n int64
	err := s.Tx(ctx, func(t *Store) error {
		q := t.qs()
		if err := q.DeactivateRecurring(ctx, id); err != nil {
			return err
		}
		var err error
		n, err = q.CancelFutureRecurringLessons(ctx, db.CancelFutureRecurringLessonsParams{
			SlotID: nullID(id), CancelledBy: nullID(by), CancelReason: reason})
		return err
	})
	return int(n), err
}

// InsertRecurringLesson создаёт урок постоянного слота, если такого ещё нет. ok=false — уже был.
func (s *Store) InsertRecurringLesson(ctx context.Context, l domain.Lesson) (bool, error) {
	n, err := s.qs().InsertRecurringLesson(ctx, db.InsertRecurringLessonParams{StudentID: l.StudentID, TutorID: l.TutorID,
		SubjectID: int32(l.SubjectID), StartsAt: l.StartsAt, DurationMin: minutes(l.Duration), RecurringSlotID: nullID(l.RecurringID)})
	return n == 1, err
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
	p := db.InsertChangeRequestParams{LessonID: r.LessonID, RequestedBy: r.RequestedBy, Kind: r.Kind, Reason: r.Reason}
	if r.NewStart != nil {
		p.NewStartsAt = sql.NullTime{Time: *r.NewStart, Valid: true}
	}
	return s.qs().InsertChangeRequest(ctx, p)
}

func (s *Store) ChangeRequest(ctx context.Context, id int64) (ChangeRequest, error) {
	r, err := s.qs().ChangeRequestByID(ctx, id)
	if notFound(err) {
		return ChangeRequest{}, domain.ErrNotFound
	}
	if err != nil {
		return ChangeRequest{}, err
	}
	out := ChangeRequest{ID: r.ID, LessonID: r.LessonID, RequestedBy: r.RequestedBy, Kind: r.Kind, Reason: r.Reason, Status: r.Status}
	if r.NewStartsAt.Valid {
		t := r.NewStartsAt.Time
		out.NewStart = &t
	}
	return out, nil
}

// DecideChangeRequest — ok=false, если запрос уже решён (например, вторым родителем).
func (s *Store) DecideChangeRequest(ctx context.Context, id int64, approved bool, by int64) (bool, error) {
	st := "declined"
	if approved {
		st = "approved"
	}
	n, err := s.qs().DecideChangeRequest(ctx, db.DecideChangeRequestParams{ID: id, Status: st, DecidedBy: nullID(by)})
	return n == 1, err
}
