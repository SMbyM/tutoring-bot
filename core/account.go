package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

// Identify находит пользователя по аккаунту мессенджера или регистрирует нового.
// isAdmin — аккаунт указан в конфигурации как администраторский.
func (a *App) Identify(ctx context.Context, id store.Identity, isAdmin bool) (domain.User, error) {
	u, ok, err := a.S.UserByIdentity(ctx, id.Provider, id.ExternalID)
	if err != nil {
		return u, err
	}
	if !ok {
		role := domain.RoleNone
		if isAdmin {
			role = domain.RoleAdmin
		}
		return a.S.CreateUser(ctx, id, "", role)
	}
	if err := a.S.UpdateIdentity(ctx, id); err != nil {
		return u, err
	}
	if isAdmin && u.Role != domain.RoleAdmin {
		if err := a.S.SetRole(ctx, u.ID, domain.RoleAdmin); err != nil {
			return u, err
		}
		u.Role = domain.RoleAdmin
	}
	return u, nil
}

// FindUser — пользователь по аккаунту мессенджера без регистрации.
func (a *App) FindUser(ctx context.Context, provider, externalID string) (domain.User, bool, error) {
	return a.S.UserByIdentity(ctx, provider, externalID)
}

func (a *App) AcceptConsent(ctx context.Context, u domain.User) error {
	return a.S.SetConsent(ctx, u.ID)
}

// ChooseRole — при регистрации самому можно стать только учеником или родителем.
// Репетиторов приглашает админ, админы задаются конфигурацией.
func (a *App) ChooseRole(ctx context.Context, u domain.User, role domain.Role) error {
	if u.Role != domain.RoleNone {
		return domain.ErrNotAllowed
	}
	if role != domain.RoleStudent && role != domain.RoleParent {
		return domain.ErrNotAllowed
	}
	return a.S.SetRole(ctx, u.ID, role)
}

func (a *App) SetName(ctx context.Context, u domain.User, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.InputError("имя не может быть пустым")
	}
	if utf8.RuneCountInString(name) > 60 {
		return domain.InputError("слишком длинное имя — до 60 символов")
	}
	return a.S.SetName(ctx, u.ID, name)
}

func (a *App) SetGrade(ctx context.Context, u domain.User, grade int) error {
	if grade < 1 || grade > 11 {
		return domain.InputError("класс — от 1 до 11")
	}
	return a.S.SetGrade(ctx, u.ID, grade)
}

// ---- настройки ----

type SettingKey string

const (
	SettingMorning SettingKey = "m" // утреннее напоминание
	SettingHour    SettingKey = "h" // напоминание за час
)

func (a *App) Settings(ctx context.Context, u domain.User) (store.Settings, error) {
	return a.S.Settings(ctx, u.ID)
}

func (a *App) ToggleSetting(ctx context.Context, u domain.User, key SettingKey) (store.Settings, error) {
	st, err := a.S.Settings(ctx, u.ID)
	if err != nil {
		return st, err
	}
	switch key {
	case SettingMorning:
		st.RemindMorning = !st.RemindMorning
	case SettingHour:
		st.RemindHour = !st.RemindHour
	default:
		return st, domain.ErrNotFound
	}
	return st, a.S.SaveSettings(ctx, u.ID, st)
}

// FinishOnboarding отмечает, что пользователь подтвердил настройки. first — это было впервые.
func (a *App) FinishOnboarding(ctx context.Context, u domain.User) (bool, error) {
	st, err := a.S.Settings(ctx, u.ID)
	if err != nil {
		return false, err
	}
	first := !st.Onboarded
	st.Onboarded = true
	return first, a.S.SaveSettings(ctx, u.ID, st)
}

// ---- приглашения ----

type InviteKind string

const (
	InviteParent InviteKind = "p" // ученик зовёт родителя
	InviteChild  InviteKind = "c" // родитель зовёт ребёнка
	InviteTutor  InviteKind = "t" // админ зовёт репетитора
)

var inviteStoreKind = map[InviteKind]string{InviteParent: "parent_link", InviteChild: "child_link", InviteTutor: "tutor"}

const InviteTTL = 7 * 24 * time.Hour

// CreateInvite создаёт одноразовое приглашение и возвращает payload для ссылки (например, «p_ab12cd…»).
func (a *App) CreateInvite(ctx context.Context, u domain.User, kind InviteKind) (string, error) {
	need := map[InviteKind]domain.Role{InviteParent: domain.RoleStudent, InviteChild: domain.RoleParent, InviteTutor: domain.RoleAdmin}[kind]
	if need == domain.RoleNone || u.EffectiveRole(a.Opt.Debug) != need {
		return "", domain.ErrNotAllowed
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b)
	if err := a.S.CreateInvite(ctx, store.Invite{Code: code, Kind: inviteStoreKind[kind], CreatedBy: u.ID}, InviteTTL); err != nil {
		return "", err
	}
	return string(kind) + "_" + code, nil
}

// AcceptInvite применяет приглашение из ссылки и возвращает текст для пользователя.
// Уведомление пригласившему уходит только после успешной транзакции.
func (a *App) AcceptInvite(ctx context.Context, u domain.User, payload string) (string, error) {
	prefix, code, ok := strings.Cut(payload, "_")
	if !ok {
		return "", domain.ErrInviteInvalid
	}
	var text, notifyText string
	var notifyID int64
	err := a.S.Tx(ctx, func(s *store.Store) error {
		inv, err := s.UseInvite(ctx, code)
		if err != nil {
			return err
		}
		becomeRole := func(role domain.Role, wrongRole string) error {
			if u.Role == role || (role == domain.RoleTutor && u.Role == domain.RoleAdmin) {
				return nil
			}
			if u.Role != domain.RoleNone {
				return domain.InputError(wrongRole)
			}
			return s.SetRole(ctx, u.ID, role)
		}
		switch {
		case prefix == string(InviteTutor) && inv.Kind == "tutor":
			if err := becomeRole(domain.RoleTutor, "вы уже зарегистрированы с другой ролью — напишите администратору"); err != nil {
				return err
			}
			if err := s.EnsureTutor(ctx, u.ID); err != nil {
				return err
			}
			text = "👋 Вы приглашены в школу как репетитор."
		case prefix == string(InviteParent) && inv.Kind == "parent_link":
			if err := becomeRole(domain.RoleParent, "эта ссылка для родителя, а вы зарегистрированы с другой ролью"); err != nil {
				return err
			}
			if err := s.Link(ctx, u.ID, inv.CreatedBy); err != nil {
				return err
			}
			child, err := s.UserByID(ctx, inv.CreatedBy)
			if err != nil {
				return err
			}
			text = "🔗 Вы привязаны как родитель: " + child.Name
			notifyID, notifyText = inv.CreatedBy, "🔗 Родитель привязан к вашему аккаунту."
		case prefix == string(InviteChild) && inv.Kind == "child_link":
			if err := becomeRole(domain.RoleStudent, "эта ссылка для ученика, а вы зарегистрированы с другой ролью"); err != nil {
				return err
			}
			if err := s.Link(ctx, inv.CreatedBy, u.ID); err != nil {
				return err
			}
			text = "🔗 Ваш аккаунт привязан к родителю."
			notifyID, notifyText = inv.CreatedBy, "🔗 Ребёнок привязан к вашему аккаунту."
		default:
			return domain.ErrInviteInvalid
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if notifyID != 0 {
		a.notify(ctx, notifyID, msg.Text(notifyText))
	}
	return text, nil
}

// ---- родитель и дети ----

func (a *App) Children(ctx context.Context, parent domain.User) ([]domain.User, error) {
	return a.S.ChildrenOf(ctx, parent.ID)
}

type ChildCard struct {
	Kid           domain.User
	NeedsApproval bool // перенос/отмена ребёнком — только с подтверждения родителя
}

func (a *App) Child(ctx context.Context, parent domain.User, kidID int64) (ChildCard, error) {
	if err := a.requireParentOf(ctx, parent, kidID); err != nil {
		return ChildCard{}, err
	}
	kid, err := a.S.UserByID(ctx, kidID)
	if err != nil {
		return ChildCard{}, err
	}
	st, err := a.S.Settings(ctx, kidID)
	if err != nil {
		return ChildCard{}, err
	}
	return ChildCard{Kid: kid, NeedsApproval: st.RescheduleNeedsParent}, nil
}

// ToggleChildApproval переключает режим «перенос только с моего подтверждения». Только родитель.
func (a *App) ToggleChildApproval(ctx context.Context, parent domain.User, kidID int64) error {
	if err := a.requireParentOf(ctx, parent, kidID); err != nil {
		return err
	}
	st, err := a.S.Settings(ctx, kidID)
	if err != nil {
		return err
	}
	st.RescheduleNeedsParent = !st.RescheduleNeedsParent
	return a.S.SaveSettings(ctx, kidID, st)
}

// AddManagedChild — ребёнок без мессенджера, всё за него делает родитель.
func (a *App) AddManagedChild(ctx context.Context, parent domain.User, name string, grade int) (int64, error) {
	if parent.EffectiveRole(a.Opt.Debug) != domain.RoleParent {
		return 0, domain.ErrNotAllowed
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return 0, domain.InputError("имя ребёнка — от 1 до 60 символов")
	}
	if grade < 1 || grade > 11 {
		return 0, domain.InputError("класс — от 1 до 11")
	}
	return a.S.CreateManagedChild(ctx, parent.ID, name, grade)
}

func (a *App) requireParentOf(ctx context.Context, parent domain.User, kidID int64) error {
	ok, err := a.S.IsParentOf(ctx, parent.ID, kidID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotAllowed
	}
	return nil
}

// ---- тестовый режим ----

// SetDebugRole — примерить роль (только debug, только админ или репетитор). RoleNone — вернуть свою.
func (a *App) SetDebugRole(ctx context.Context, u domain.User, role domain.Role) error {
	if !u.CanSwitchRoles(a.Opt.Debug) {
		return domain.ErrNotAllowed
	}
	if !role.Valid() {
		role = domain.RoleNone
	}
	if err := a.S.SetDebugRole(ctx, u.ID, role); err != nil {
		return err
	}
	if role == domain.RoleTutor {
		return a.S.EnsureTutor(ctx, u.ID)
	}
	return nil
}

// DebugFinishNearest сдвигает ближайший урок пользователя в прошлое и запускает фоновые задачи,
// чтобы сразу проверить отметку урока, отзыв и доступ в канал. ok=false — уроков нет.
func (a *App) DebugFinishNearest(ctx context.Context, u domain.User) (domain.Lesson, bool, error) {
	if !u.CanSwitchRoles(a.Opt.Debug) {
		return domain.Lesson{}, false, domain.ErrNotAllowed
	}
	ls, err := a.S.UpcomingForStudent(ctx, u.ID, 1)
	if err != nil {
		return domain.Lesson{}, false, err
	}
	if len(ls) == 0 {
		if ls, err = a.S.UpcomingForTutor(ctx, u.ID, 1); err != nil {
			return domain.Lesson{}, false, err
		}
	}
	if len(ls) == 0 {
		return domain.Lesson{}, false, nil
	}
	l := ls[0]
	if err := a.S.DebugShiftLesson(ctx, l.ID, a.Now().Add(-l.Duration-time.Minute)); err != nil {
		return l, false, err
	}
	a.Tick(ctx)
	return l, true, nil
}

// DebugRunJobs — все фоновые задачи сразу (только debug).
func (a *App) DebugRunJobs(ctx context.Context, u domain.User) error {
	if !u.CanSwitchRoles(a.Opt.Debug) {
		return domain.ErrNotAllowed
	}
	a.Tick(ctx)
	a.DailyTick(ctx)
	return nil
}
