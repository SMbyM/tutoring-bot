// Package core — сценарии предметной области поверх хранилища.
// Ничего не знает о мессенджерах: всё исходящее (сообщения, ссылки в канал, исключения)
// записывается в outbox, а адаптеры конкретных мессенджеров забирают свои записи и выполняют их.
// Поэтому ядро одинаково работает в процессе бота, в фоновом воркере и в будущем веб-кабинете.
package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

type Options struct {
	Debug            bool
	ChannelEnabled   bool // настроен закрытый канал с материалами
	AccessInactivity time.Duration
	BookingHorizon   time.Duration
	RecurringAhead   time.Duration
	PolicyURL        string
	MiniAppURL       string
}

type App struct {
	S   *store.Store
	Opt Options
	Now func() time.Time
	Log *slog.Logger
}

func New(s *store.Store, opt Options) *App {
	if opt.AccessInactivity == 0 {
		opt.AccessInactivity = 30 * 24 * time.Hour
	}
	if opt.BookingHorizon == 0 {
		opt.BookingHorizon = 14 * 24 * time.Hour
	}
	if opt.RecurringAhead == 0 {
		opt.RecurringAhead = 28 * 24 * time.Hour
	}
	return &App{S: s, Opt: opt, Now: time.Now, Log: slog.Default()}
}

// recipient — куда доставлять пользователю: мессенджер, где он был последним.
// Ребёнку без мессенджера всё уходит управляющему родителю с пометкой имени.
func (a *App) recipient(ctx context.Context, userID int64) (store.Identity, string, error) {
	id, ok, err := a.S.PreferredIdentity(ctx, userID)
	if err != nil || ok {
		return id, "", err
	}
	u, err := a.S.UserByID(ctx, userID)
	if err != nil {
		return id, "", err
	}
	if u.ManagedBy == 0 {
		return id, "", fmt.Errorf("у пользователя %d нет подключённого мессенджера", userID)
	}
	id, ok, err = a.S.PreferredIdentity(ctx, u.ManagedBy)
	if err != nil {
		return id, "", err
	}
	if !ok {
		return id, "", fmt.Errorf("у родителя %d нет подключённого мессенджера", u.ManagedBy)
	}
	return id, "👤 " + u.Name + "\n", nil
}

// notify ставит сообщение пользователю в очередь отправки.
func (a *App) notify(ctx context.Context, userID int64, m msg.Message) {
	a.enqueue(ctx, userID, store.OutMessage, m)
}

func (a *App) enqueue(ctx context.Context, userID int64, kind store.OutboxKind, m msg.Message) {
	id, prefix, err := a.recipient(ctx, userID)
	if err == nil {
		m.Text = prefix + m.Text
		m.Replace, m.Toast = false, ""
		err = a.S.Enqueue(ctx, store.OutboxItem{Provider: id.Provider, Kind: kind, ChatID: id.ChatID, ExternalID: id.ExternalID, Message: m})
	}
	if err != nil {
		a.Log.Warn("уведомление не поставлено в очередь", "user", userID, "kind", kind, "err", err)
	}
}

// notifyMany — без повторов.
func (a *App) notifyMany(ctx context.Context, ids []int64, m msg.Message) {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		a.notify(ctx, id, m)
	}
}

// parentIDs — родители ученика.
func (a *App) parentIDs(ctx context.Context, studentID int64) []int64 {
	ps, err := a.S.ParentsOf(ctx, studentID)
	if err != nil {
		a.Log.Warn("родители", "err", err)
		return nil
	}
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	return ids
}

func (a *App) adminIDs(ctx context.Context) []int64 {
	as, err := a.S.Admins(ctx)
	if err != nil {
		return nil
	}
	ids := make([]int64, len(as))
	for i, u := range as {
		ids[i] = u.ID
	}
	return ids
}

// actorFor — кто пользователь по отношению к уроку.
func (a *App) actorFor(ctx context.Context, u domain.User, l domain.Lesson) (domain.Actor, error) {
	role := u.EffectiveRole(a.Opt.Debug)
	act := domain.Actor{UserID: u.ID, Role: role, IsSelf: u.ID == l.StudentID, IsTutor: u.ID == l.TutorID}
	if role == domain.RoleParent {
		ok, err := a.S.IsParentOf(ctx, u.ID, l.StudentID)
		if err != nil {
			return act, err
		}
		act.IsParent = ok
	}
	return act, nil
}

// canActForStudent — может ли пользователь записывать/оплачивать за ученика.
func (a *App) canActForStudent(ctx context.Context, u domain.User, studentID int64) error {
	if u.ID == studentID || u.EffectiveRole(a.Opt.Debug) == domain.RoleAdmin {
		return nil
	}
	ok, err := a.S.IsParentOf(ctx, u.ID, studentID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotAllowed
	}
	return nil
}

func (a *App) tutorLoc(t store.Tutor) *time.Location { return t.Location() }

// LessonLine — «Математика · вт, 7 октября, 17:00 · Иван».
func LessonLine(l domain.Lesson, loc *time.Location, withTutor, withStudent bool) string {
	s := l.Subject + " · " + domain.FormatDateTime(l.StartsAt, loc)
	if l.Kind == domain.KindTrial {
		s += " · пробный"
	}
	if withTutor {
		s += " · " + l.TutorName
	}
	if withStudent {
		s += " · " + l.StudentName
	}
	return s
}
