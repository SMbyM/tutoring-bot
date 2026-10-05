// Package core — сценарии предметной области поверх хранилища.
// Ничего не знает о Telegram: исходящие сообщения уходят через порт Notifier,
// работа с закрытым каналом — через порт ChannelGate.
package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
	"github.com/SMbyM/tutoring-bot/internal/msg"
	"github.com/SMbyM/tutoring-bot/internal/store"
)

// Sender — адаптер конкретного мессенджера.
type Sender interface {
	Provider() string
	Send(ctx context.Context, chatID string, m msg.Message) error
}

// ChannelGate — управление доступом в закрытый канал (реализация зависит от мессенджера).
type ChannelGate interface {
	Provider() string
	InviteLink(ctx context.Context, name string) (string, error)
	Kick(ctx context.Context, externalID string) error
}

type Options struct {
	Debug            bool
	AccessInactivity time.Duration
	BookingHorizon   time.Duration
	RecurringAhead   time.Duration
	PolicyURL        string
	MiniAppURL       string
}

type App struct {
	S       *store.Store
	Opt     Options
	Gate    ChannelGate // nil — канал не настроен
	Now     func() time.Time
	Log     *slog.Logger
	senders map[string]Sender
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
	return &App{S: s, Opt: opt, Now: time.Now, Log: slog.Default(), senders: map[string]Sender{}}
}

func (a *App) AddSender(s Sender) { a.senders[s.Provider()] = s }

// Notify доставляет сообщение пользователю в первый доступный мессенджер.
// Ребёнку без мессенджера сообщение уходит управляющему родителю с пометкой.
func (a *App) Notify(ctx context.Context, userID int64, m msg.Message) {
	if err := a.notify(ctx, userID, m, 0); err != nil {
		a.Log.Warn("уведомление не доставлено", "user", userID, "err", err)
	}
}

func (a *App) notify(ctx context.Context, userID int64, m msg.Message, depth int) error {
	ids, err := a.S.Identities(ctx, userID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if s, ok := a.senders[id.Provider]; ok {
			return s.Send(ctx, id.ChatID, m)
		}
	}
	if depth > 0 {
		return fmt.Errorf("нет мессенджера")
	}
	u, err := a.S.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.ManagedBy == 0 {
		return fmt.Errorf("у пользователя нет подключённого мессенджера")
	}
	m.Text = "👤 " + u.Name + "\n" + m.Text
	return a.notify(ctx, u.ManagedBy, m, depth+1)
}

// NotifyMany — без повторов.
func (a *App) NotifyMany(ctx context.Context, ids []int64, m msg.Message) {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		a.Notify(ctx, id, m)
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

// ActorFor — кто пользователь по отношению к уроку.
func (a *App) ActorFor(ctx context.Context, u domain.User, l domain.Lesson) (domain.Actor, error) {
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

// CanActForStudent — может ли пользователь записывать/оплачивать за ученика.
func (a *App) CanActForStudent(ctx context.Context, u domain.User, studentID int64) error {
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
