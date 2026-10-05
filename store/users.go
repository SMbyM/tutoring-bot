package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store/db"
)

func userFromDB(u db.User) domain.User {
	return domain.User{
		ID:        u.ID,
		Name:      u.Name,
		Role:      domain.Role(u.Role),
		DebugRole: domain.Role(u.DebugRole.String),
		Grade:     int(u.Grade.Int16),
		TZ:        u.Tz,
		ManagedBy: u.ManagedBy.Int64,
		Consent:   u.ConsentAt.Valid,
	}
}

func usersFromDB(us []db.User, err error) ([]domain.User, error) {
	if err != nil {
		return nil, err
	}
	out := make([]domain.User, len(us))
	for i, u := range us {
		out[i] = userFromDB(u)
	}
	return out, nil
}

type Identity struct {
	Provider   string
	ExternalID string
	ChatID     string
	Username   string
}

// UserByIdentity — пользователь по аккаунту мессенджера. ok=false, если не найден.
func (s *Store) UserByIdentity(ctx context.Context, provider, extID string) (domain.User, bool, error) {
	u, err := s.qs().UserByIdentity(ctx, db.UserByIdentityParams{Provider: provider, ExternalID: extID})
	if notFound(err) {
		return domain.User{}, false, nil
	}
	if err != nil {
		return domain.User{}, false, err
	}
	return userFromDB(u), true, nil
}

func (s *Store) CreateUser(ctx context.Context, id Identity, name string, role domain.Role) (domain.User, error) {
	var uid int64
	err := s.Tx(ctx, func(t *Store) error {
		q := t.qs()
		var err error
		if uid, err = q.InsertUser(ctx, db.InsertUserParams{Name: name, Role: string(role)}); err != nil {
			return err
		}
		if err := q.InsertIdentity(ctx, db.InsertIdentityParams{Provider: id.Provider, ExternalID: id.ExternalID,
			UserID: uid, ChatID: id.ChatID, Username: id.Username}); err != nil {
			return err
		}
		return q.InsertSettings(ctx, db.InsertSettingsParams{UserID: uid})
	})
	if err != nil {
		return domain.User{}, err
	}
	return s.UserByID(ctx, uid)
}

// UpdateIdentity обновляет чат и имя пользователя и отмечает последнюю активность в этом мессенджере.
func (s *Store) UpdateIdentity(ctx context.Context, id Identity) error {
	return s.qs().TouchIdentity(ctx, db.TouchIdentityParams{ChatID: id.ChatID, Username: id.Username,
		Provider: id.Provider, ExternalID: id.ExternalID})
}

func (s *Store) UserByID(ctx context.Context, id int64) (domain.User, error) {
	u, err := s.qs().UserByID(ctx, id)
	if notFound(err) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	return userFromDB(u), nil
}

func (s *Store) Identities(ctx context.Context, userID int64) ([]Identity, error) {
	rows, err := s.qs().Identities(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Identity, len(rows))
	for i, r := range rows {
		out[i] = Identity{Provider: r.Provider, ExternalID: r.ExternalID, ChatID: r.ChatID, Username: r.Username}
	}
	return out, nil
}

// PreferredIdentity — аккаунт, через который пользователь заходил последним.
func (s *Store) PreferredIdentity(ctx context.Context, userID int64) (Identity, bool, error) {
	r, err := s.qs().PreferredIdentity(ctx, userID)
	if notFound(err) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	return Identity{Provider: r.Provider, ExternalID: r.ExternalID, ChatID: r.ChatID, Username: r.Username}, true, nil
}

func (s *Store) SetName(ctx context.Context, id int64, name string) error {
	return s.qs().SetName(ctx, db.SetNameParams{ID: id, Name: name})
}

func (s *Store) SetRole(ctx context.Context, id int64, r domain.Role) error {
	return s.qs().SetRole(ctx, db.SetRoleParams{ID: id, Role: string(r)})
}

func (s *Store) SetDebugRole(ctx context.Context, id int64, r domain.Role) error {
	return s.qs().SetDebugRole(ctx, db.SetDebugRoleParams{ID: id, DebugRole: sql.NullString{String: string(r), Valid: r.Valid()}})
}

func (s *Store) SetGrade(ctx context.Context, id int64, grade int) error {
	return s.qs().SetGrade(ctx, db.SetGradeParams{ID: id, Grade: sql.NullInt16{Int16: int16(grade), Valid: true}})
}

func (s *Store) SetConsent(ctx context.Context, id int64) error {
	return s.qs().SetConsent(ctx, id)
}

func (s *Store) Admins(ctx context.Context) ([]domain.User, error) {
	return usersFromDB(s.qs().Admins(ctx))
}

// ---- родители и дети ----

func (s *Store) Link(ctx context.Context, parentID, studentID int64) error {
	return s.qs().LinkParent(ctx, db.LinkParentParams{ParentID: parentID, StudentID: studentID})
}

func (s *Store) ParentsOf(ctx context.Context, studentID int64) ([]domain.User, error) {
	return usersFromDB(s.qs().ParentsOf(ctx, studentID))
}

func (s *Store) ChildrenOf(ctx context.Context, parentID int64) ([]domain.User, error) {
	return usersFromDB(s.qs().ChildrenOf(ctx, parentID))
}

func (s *Store) IsParentOf(ctx context.Context, parentID, studentID int64) (bool, error) {
	return s.qs().IsParentOf(ctx, db.IsParentOfParams{ParentID: parentID, StudentID: studentID})
}

// CreateManagedChild — ребёнок без мессенджера, которым управляет родитель.
func (s *Store) CreateManagedChild(ctx context.Context, parentID int64, name string, grade int) (int64, error) {
	var id int64
	err := s.Tx(ctx, func(t *Store) error {
		q := t.qs()
		var err error
		id, err = q.InsertManagedChild(ctx, db.InsertManagedChildParams{Name: name,
			Grade: sql.NullInt16{Int16: int16(grade), Valid: true}, ManagedBy: sql.NullInt64{Int64: parentID, Valid: true}})
		if err != nil {
			return err
		}
		if err := q.InsertSettings(ctx, db.InsertSettingsParams{UserID: id, Onboarded: true}); err != nil {
			return err
		}
		return t.Link(ctx, parentID, id)
	})
	return id, err
}

// ---- настройки ----

type Settings struct {
	RemindMorning         bool
	RemindHour            bool
	RescheduleNeedsParent bool
	Onboarded             bool
}

func (s *Store) Settings(ctx context.Context, userID int64) (Settings, error) {
	r, err := s.qs().GetSettings(ctx, userID)
	if notFound(err) {
		return Settings{RemindMorning: true, RemindHour: true, RescheduleNeedsParent: true}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{RemindMorning: r.RemindMorning, RemindHour: r.RemindHour,
		RescheduleNeedsParent: r.RescheduleNeedsParent, Onboarded: r.Onboarded}, nil
}

func (s *Store) SaveSettings(ctx context.Context, userID int64, st Settings) error {
	return s.qs().UpsertSettings(ctx, db.UpsertSettingsParams{UserID: userID, RemindMorning: st.RemindMorning,
		RemindHour: st.RemindHour, RescheduleNeedsParent: st.RescheduleNeedsParent, Onboarded: st.Onboarded})
}

// ---- приглашения ----

type Invite struct {
	Code      string
	Kind      string
	CreatedBy int64
	TargetID  int64
}

func (s *Store) CreateInvite(ctx context.Context, inv Invite, ttl time.Duration) error {
	return s.qs().InsertInvite(ctx, db.InsertInviteParams{Code: inv.Code, Kind: inv.Kind, CreatedBy: inv.CreatedBy,
		TargetID: sql.NullInt64{Int64: inv.TargetID, Valid: inv.TargetID != 0}, ExpiresAt: time.Now().Add(ttl)})
}

// UseInvite помечает приглашение использованным и возвращает его. Повторное использование — ошибка.
func (s *Store) UseInvite(ctx context.Context, code string) (Invite, error) {
	r, err := s.qs().UseInvite(ctx, code)
	if notFound(err) {
		return Invite{}, domain.ErrInviteInvalid
	}
	if err != nil {
		return Invite{}, err
	}
	return Invite{Code: r.Code, Kind: r.Kind, CreatedBy: r.CreatedBy, TargetID: r.TargetID.Int64}, nil
}

// ---- диалоговое состояние ----

// State — незаданный вопрос пользователю: вид вопроса и его данные в JSON. ok=false — вопроса нет.
func (s *Store) State(ctx context.Context, userID int64) (string, json.RawMessage, bool, error) {
	r, err := s.qs().GetState(ctx, userID)
	if notFound(err) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	return r.State, r.Data, true, nil
}

func (s *Store) SetState(ctx context.Context, userID int64, state string, data json.RawMessage) error {
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	return s.qs().UpsertState(ctx, db.UpsertStateParams{UserID: userID, State: state, Data: data})
}

func (s *Store) ClearState(ctx context.Context, userID int64) error {
	return s.qs().DeleteState(ctx, userID)
}
