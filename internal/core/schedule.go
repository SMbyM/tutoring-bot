package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
	"github.com/SMbyM/tutoring-bot/internal/msg"
	"github.com/SMbyM/tutoring-bot/internal/store"
)

// FreeSlots — свободные окна репетитора для ученика на горизонте записи.
func (a *App) FreeSlots(ctx context.Context, tutorID, studentID int64, excludeLesson int64) ([]time.Time, store.Tutor, error) {
	t, err := a.S.Tutor(ctx, tutorID)
	if err != nil {
		return nil, t, err
	}
	loc := t.Location()
	now := a.Now()
	from := now.Add(domain.MinBookingLead)
	to := now.Add(a.Opt.BookingHorizon)
	ws, err := a.S.Windows(ctx, tutorID)
	if err != nil {
		return nil, t, err
	}
	ex, err := exceptions(ctx, a.S, tutorID, loc)
	if err != nil {
		return nil, t, err
	}
	busy, err := a.S.Busy(ctx, tutorID, studentID, from, to.Add(t.Duration()), excludeLesson)
	if err != nil {
		return nil, t, err
	}
	return domain.FreeSlots(ws, ex, busy, loc, from, to, t.Duration()), t, nil
}

// exceptions читает исключения через переданный store: внутри транзакции важно
// не брать второе соединение из пула (иначе под нагрузкой — взаимоблокировка).
func exceptions(ctx context.Context, s *store.Store, tutorID int64, loc *time.Location) ([]domain.Exception, error) {
	rows, err := s.Exceptions(ctx, tutorID, loc)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Exception, len(rows))
	for i, r := range rows {
		out[i] = domain.Exception{From: r.From, To: r.To}
	}
	return out, nil
}

// checkSlot проверяет окно и занятость. Вызывать внутри транзакции после LockTutor.
func (a *App) checkSlot(ctx context.Context, s *store.Store, t store.Tutor, studentID int64, start time.Time, exclude int64) error {
	if start.Before(a.Now().Add(domain.MinBookingLead)) {
		return domain.ErrInPast
	}
	ws, err := s.Windows(ctx, t.ID)
	if err != nil {
		return err
	}
	ex, err := exceptions(ctx, s, t.ID, t.Location())
	if err != nil {
		return err
	}
	if !domain.FitsWindows(ws, ex, t.Location(), start, t.Duration()) {
		return domain.ErrOutsideWindows
	}
	busy, err := s.Busy(ctx, t.ID, studentID, start, start.Add(t.Duration()), exclude)
	if err != nil {
		return err
	}
	if len(busy) > 0 {
		return domain.ErrSlotTaken
	}
	return nil
}

// BookTrial — пробный урок: один на пару ученик–репетитор, бесплатный.
func (a *App) BookTrial(ctx context.Context, actor domain.User, studentID, tutorID int64, subjectID int, start time.Time) (domain.Lesson, error) {
	return a.book(ctx, actor, studentID, tutorID, subjectID, start, domain.KindTrial)
}

// BookOnce — разовый урок у закреплённого репетитора.
func (a *App) BookOnce(ctx context.Context, actor domain.User, studentID, tutorID int64, subjectID int, start time.Time) (domain.Lesson, error) {
	return a.book(ctx, actor, studentID, tutorID, subjectID, start, domain.KindRegular)
}

func (a *App) book(ctx context.Context, actor domain.User, studentID, tutorID int64, subjectID int, start time.Time, kind domain.LessonKind) (domain.Lesson, error) {
	if err := a.CanActForStudent(ctx, actor, studentID); err != nil {
		return domain.Lesson{}, err
	}
	var id int64
	err := a.S.Tx(ctx, func(s *store.Store) error {
		if err := s.LockTutor(ctx, tutorID); err != nil {
			return err
		}
		t, err := s.Tutor(ctx, tutorID)
		if err != nil {
			return err
		}
		if kind == domain.KindTrial {
			used, err := s.HasTrial(ctx, studentID, tutorID)
			if err != nil {
				return err
			}
			if used {
				return domain.ErrTrialUsed
			}
		} else {
			ok, err := s.IsEnrolled(ctx, studentID, tutorID, subjectID)
			if err != nil {
				return err
			}
			if !ok {
				return domain.ErrNotEnrolled
			}
		}
		if err := a.checkSlot(ctx, s, t, studentID, start, 0); err != nil {
			return err
		}
		id, err = s.InsertLesson(ctx, domain.Lesson{StudentID: studentID, TutorID: tutorID, SubjectID: subjectID,
			StartsAt: start, Duration: t.Duration(), Kind: kind})
		return err
	})
	if err != nil {
		return domain.Lesson{}, err
	}
	l, err := a.S.Lesson(ctx, id)
	if err != nil {
		return l, err
	}
	a.announceBooking(ctx, actor, l, "")
	return l, nil
}

func (a *App) announceBooking(ctx context.Context, actor domain.User, l domain.Lesson, extra string) {
	t, err := a.S.Tutor(ctx, l.TutorID)
	if err != nil {
		return
	}
	what := "Новая запись"
	if l.Kind == domain.KindTrial {
		what = "Новая запись на пробный урок"
	}
	text := fmt.Sprintf("📅 %s: %s, %s\n%s%s", what, l.StudentName, l.Subject, domain.FormatDateTime(l.StartsAt, t.Location()), extra)
	recipients := append([]int64{l.TutorID}, a.parentIDs(ctx, l.StudentID)...)
	recipients = append(recipients, l.StudentID)
	var others []int64
	for _, id := range recipients {
		if id != actor.ID {
			others = append(others, id)
		}
	}
	a.NotifyMany(ctx, others, msg.Text(text))
}

// BookRecurring — постоянный слот (каждую неделю в это же время), уроки создаются на RecurringAhead вперёд.
func (a *App) BookRecurring(ctx context.Context, actor domain.User, studentID, tutorID int64, subjectID int, start time.Time) (int64, int, error) {
	if err := a.CanActForStudent(ctx, actor, studentID); err != nil {
		return 0, 0, err
	}
	var slotID int64
	var created int
	err := a.S.Tx(ctx, func(s *store.Store) error {
		if err := s.LockTutor(ctx, tutorID); err != nil {
			return err
		}
		t, err := s.Tutor(ctx, tutorID)
		if err != nil {
			return err
		}
		ok, err := s.IsEnrolled(ctx, studentID, tutorID, subjectID)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotEnrolled
		}
		if err := a.checkSlot(ctx, s, t, studentID, start, 0); err != nil {
			return err
		}
		ls := start.In(t.Location())
		slotID, err = s.InsertRecurring(ctx, store.Recurring{StudentID: studentID, TutorID: tutorID, SubjectID: subjectID,
			Weekday: ls.Weekday(), StartMin: ls.Hour()*60 + ls.Minute(), Duration: t.Duration()})
		if err != nil {
			return err
		}
		r, err := s.RecurringByID(ctx, slotID)
		if err != nil {
			return err
		}
		created, err = a.materialize(ctx, s, r, t, start)
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	if created > 0 {
		t, _ := a.S.Tutor(ctx, tutorID)
		st, _ := a.S.UserByID(ctx, studentID)
		text := fmt.Sprintf("🔁 Постоянное расписание: %s, каждую неделю — %s, %s",
			st.Name, domain.WeekdayFull(start.In(t.Location()).Weekday()), domain.FormatTime(start, t.Location()))
		var others []int64
		for _, id := range append([]int64{tutorID, studentID}, a.parentIDs(ctx, studentID)...) {
			if id != actor.ID {
				others = append(others, id)
			}
		}
		a.NotifyMany(ctx, others, msg.Text(text))
	}
	return slotID, created, nil
}

// materialize создаёт уроки слота от from до now+RecurringAhead, пропуская занятые/вне окон даты.
func (a *App) materialize(ctx context.Context, s *store.Store, r store.Recurring, t store.Tutor, from time.Time) (int, error) {
	to := a.Now().Add(a.Opt.RecurringAhead)
	created := 0
	for _, start := range domain.Occurrences(r.Weekday, r.StartMin, t.Location(), from, to) {
		err := a.checkSlot(ctx, s, t, r.StudentID, start, 0)
		if errors.Is(err, domain.ErrSlotTaken) || errors.Is(err, domain.ErrOutsideWindows) || errors.Is(err, domain.ErrInPast) {
			continue // отпуск репетитора, разовый урок на это время и т.п.
		}
		if err != nil {
			return created, err
		}
		ok, err := s.InsertRecurringLesson(ctx, domain.Lesson{StudentID: r.StudentID, TutorID: r.TutorID, SubjectID: r.SubjectID,
			StartsAt: start, Duration: r.Duration, RecurringID: r.ID})
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	return created, nil
}

// ExtendRecurring — фоновая задача: досоздаёт уроки постоянных слотов на горизонт вперёд.
func (a *App) ExtendRecurring(ctx context.Context) error {
	slots, err := a.S.ActiveRecurring(ctx)
	if err != nil {
		return err
	}
	for _, r := range slots {
		err := a.S.Tx(ctx, func(s *store.Store) error {
			if err := s.LockTutor(ctx, r.TutorID); err != nil {
				return err
			}
			t, err := s.Tutor(ctx, r.TutorID)
			if err != nil {
				return err
			}
			_, err = a.materialize(ctx, s, r, t, a.Now())
			return err
		})
		if err != nil {
			a.Log.Warn("продление постоянного слота", "slot", r.ID, "err", err)
		}
	}
	return nil
}
