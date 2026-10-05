package core

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store"
)

// Профиль и расписание репетитора. Все операции — только для самого репетитора
// (в debug — и для того, кто примерил роль репетитора).

func (a *App) requireTutor(ctx context.Context, u domain.User) error {
	if u.EffectiveRole(a.Opt.Debug) != domain.RoleTutor {
		return domain.ErrNotAllowed
	}
	return a.S.EnsureTutor(ctx, u.ID)
}

type Schedule struct {
	Tutor      store.Tutor
	Windows    []domain.Window
	Exceptions []store.ExceptionRow
}

func (a *App) MySchedule(ctx context.Context, u domain.User) (Schedule, error) {
	if err := a.requireTutor(ctx, u); err != nil {
		return Schedule{}, err
	}
	t, err := a.S.Tutor(ctx, u.ID)
	if err != nil {
		return Schedule{}, err
	}
	ws, err := a.S.Windows(ctx, u.ID)
	if err != nil {
		return Schedule{}, err
	}
	ex, err := a.S.Exceptions(ctx, u.ID, t.Location())
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{Tutor: t, Windows: ws, Exceptions: ex}, nil
}

// SetWindows заменяет недельные окна. Уже записанные уроки не меняются.
func (a *App) SetWindows(ctx context.Context, u domain.User, ws []domain.Window) error {
	if err := a.requireTutor(ctx, u); err != nil {
		return err
	}
	if err := domain.ValidateWindows(ws); err != nil {
		return domain.InputError(err.Error())
	}
	return a.S.ReplaceWindows(ctx, u.ID, ws)
}

func (a *App) AddException(ctx context.Context, u domain.User, from, to time.Time, note string) error {
	if err := a.requireTutor(ctx, u); err != nil {
		return err
	}
	if to.Before(from) {
		return domain.InputError("дата окончания раньше даты начала")
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 100 {
		note = string([]rune(note)[:100])
	}
	return a.S.AddException(ctx, u.ID, from, to, note)
}

func (a *App) DeleteException(ctx context.Context, u domain.User, id int64) error {
	if err := a.requireTutor(ctx, u); err != nil {
		return err
	}
	return a.S.DeleteException(ctx, u.ID, id)
}

func (a *App) SetLessonMinutes(ctx context.Context, u domain.User, minutes int) error {
	if err := a.requireTutor(ctx, u); err != nil {
		return err
	}
	if minutes < 15 || minutes > 240 {
		return domain.InputError("длительность урока — от 15 до 240 минут")
	}
	return a.S.SetTutorDuration(ctx, u.ID, minutes)
}

func (a *App) SetBio(ctx context.Context, u domain.User, bio string) error {
	if err := a.requireTutor(ctx, u); err != nil {
		return err
	}
	bio = strings.TrimSpace(bio)
	if utf8.RuneCountInString(bio) > 800 {
		return domain.InputError("слишком длинно — до 800 символов")
	}
	return a.S.SetTutorBio(ctx, u.ID, bio)
}

func (a *App) ToggleSubject(ctx context.Context, u domain.User, subjectID int) error {
	if err := a.requireTutor(ctx, u); err != nil {
		return err
	}
	return a.S.ToggleTutorSubject(ctx, u.ID, subjectID)
}

type StudentBalance struct {
	Student domain.User
	Balance int
}

// MyStudents — закреплённые ученики репетитора с остатком оплаченных уроков.
func (a *App) MyStudents(ctx context.Context, u domain.User) ([]StudentBalance, error) {
	if err := a.requireTutor(ctx, u); err != nil {
		return nil, err
	}
	sts, err := a.S.TutorStudents(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make([]StudentBalance, len(sts))
	for i, s := range sts {
		bal, err := a.S.Balance(ctx, s.ID, u.ID)
		if err != nil {
			return nil, err
		}
		out[i] = StudentBalance{s, bal}
	}
	return out, nil
}

// TutorLessons — ближайшие уроки репетитора.
func (a *App) TutorLessons(ctx context.Context, u domain.User, limit int) ([]domain.Lesson, error) {
	if err := a.requireTutor(ctx, u); err != nil {
		return nil, err
	}
	return a.S.UpcomingForTutor(ctx, u.ID, limit)
}
