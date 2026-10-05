// Package domain — чистые правила предметной области без зависимостей от БД и мессенджеров.
package domain

import (
	"errors"
	"time"
)

type Role string

const (
	RoleNone    Role = ""
	RoleStudent Role = "student"
	RoleParent  Role = "parent"
	RoleTutor   Role = "tutor"
	RoleAdmin   Role = "admin"
)

func (r Role) Valid() bool {
	switch r {
	case RoleStudent, RoleParent, RoleTutor, RoleAdmin:
		return true
	}
	return false
}

func (r Role) Title() string {
	switch r {
	case RoleStudent:
		return "ученик"
	case RoleParent:
		return "родитель"
	case RoleTutor:
		return "репетитор"
	case RoleAdmin:
		return "администратор"
	}
	return "без роли"
}

type LessonKind string

const (
	KindTrial   LessonKind = "trial"
	KindRegular LessonKind = "regular"
)

type LessonStatus string

const (
	StatusScheduled LessonStatus = "scheduled"
	StatusHeld      LessonStatus = "held"
	StatusNoShow    LessonStatus = "no_show"
	StatusCancelled LessonStatus = "cancelled"
)

func (s LessonStatus) Title() string {
	switch s {
	case StatusScheduled:
		return "запланирован"
	case StatusHeld:
		return "состоялся"
	case StatusNoShow:
		return "ученик не пришёл"
	case StatusCancelled:
		return "отменён"
	}
	return string(s)
}

type User struct {
	ID        int64
	Name      string
	Role      Role
	DebugRole Role
	Grade     int
	TZ        string
	ManagedBy int64 // 0 — самостоятельный пользователь
	Consent   bool
}

// EffectiveRole учитывает подмену роли в debug-режиме.
func (u User) EffectiveRole(debug bool) Role {
	if debug && u.DebugRole.Valid() {
		return u.DebugRole
	}
	return u.Role
}

// CanSwitchRoles — в debug-режиме переключать роли могут админы и репетиторы.
func (u User) CanSwitchRoles(debug bool) bool {
	return debug && (u.Role == RoleAdmin || u.Role == RoleTutor)
}

func (u User) Location() *time.Location {
	return LoadLocation(u.TZ)
}

// LoadLocation возвращает пояс, по умолчанию Новосибирск.
func LoadLocation(name string) *time.Location {
	if name == "" {
		name = DefaultTZ
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc, err = time.LoadLocation(DefaultTZ)
		if err != nil {
			return time.FixedZone("NOVT", 7*3600)
		}
	}
	return loc
}

const DefaultTZ = "Asia/Novosibirsk"

type Lesson struct {
	ID          int64
	StudentID   int64
	TutorID     int64
	SubjectID   int
	Subject     string
	StudentName string
	TutorName   string
	StartsAt    time.Time
	Duration    time.Duration
	Kind        LessonKind
	Status      LessonStatus
	RecurringID int64
	LateCancel  bool
	Reason      string
}

func (l Lesson) EndsAt() time.Time { return l.StartsAt.Add(l.Duration) }

var (
	ErrSlotTaken       = errors.New("это время уже занято")
	ErrTrialUsed       = errors.New("пробный урок у этого репетитора уже был")
	ErrNotAllowed      = errors.New("нет прав на это действие")
	ErrNotFound        = errors.New("не найдено")
	ErrInPast          = errors.New("нельзя записаться на прошедшее время")
	ErrNotScheduled    = errors.New("урок уже не в статусе «запланирован»")
	ErrReasonRequired  = errors.New("для поздней отмены нужна причина")
	ErrNotEnrolled     = errors.New("сначала нужен понравившийся пробный урок у этого репетитора")
	ErrOutsideWindows  = errors.New("это время вне окон репетитора")
	ErrInviteInvalid   = errors.New("приглашение недействительно или устарело")
	ErrNoChildSelected = errors.New("сначала выберите ребёнка")
)
