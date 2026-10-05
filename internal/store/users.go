package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
)

const userCols = `u.id, u.name, u.role, COALESCE(u.debug_role,''), COALESCE(u.grade,0), u.tz, COALESCE(u.managed_by,0), u.consent_at IS NOT NULL`

func scanUser(sc interface{ Scan(...any) error }) (domain.User, error) {
	var u domain.User
	var role, dbg string
	err := sc.Scan(&u.ID, &u.Name, &role, &dbg, &u.Grade, &u.TZ, &u.ManagedBy, &u.Consent)
	u.Role, u.DebugRole = domain.Role(role), domain.Role(dbg)
	return u, err
}

func (s *Store) queryUsers(ctx context.Context, q string, args ...any) ([]domain.User, error) {
	rows, err := s.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type Identity struct {
	Provider   string
	ExternalID string
	ChatID     string
	Username   string
}

// UserByIdentity — пользователь по аккаунту мессенджера. ok=false, если не найден.
func (s *Store) UserByIdentity(ctx context.Context, provider, extID string) (domain.User, bool, error) {
	u, err := scanUser(s.q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users u
		JOIN identities i ON i.user_id = u.id WHERE i.provider=$1 AND i.external_id=$2`, provider, extID))
	if notFound(err) {
		return u, false, nil
	}
	return u, err == nil, err
}

func (s *Store) CreateUser(ctx context.Context, id Identity, name string, role domain.Role) (domain.User, error) {
	var uid int64
	err := s.Tx(ctx, func(t *Store) error {
		if err := t.q.QueryRowContext(ctx, `INSERT INTO users(name, role) VALUES ($1,$2) RETURNING id`, name, string(role)).Scan(&uid); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(ctx, `INSERT INTO identities(provider, external_id, user_id, chat_id, username) VALUES ($1,$2,$3,$4,$5)`,
			id.Provider, id.ExternalID, uid, id.ChatID, id.Username); err != nil {
			return err
		}
		_, err := t.q.ExecContext(ctx, `INSERT INTO user_settings(user_id) VALUES ($1)`, uid)
		return err
	})
	if err != nil {
		return domain.User{}, err
	}
	return s.UserByID(ctx, uid)
}

func (s *Store) UpdateIdentity(ctx context.Context, id Identity) error {
	_, err := s.q.ExecContext(ctx, `UPDATE identities SET chat_id=$3, username=$4 WHERE provider=$1 AND external_id=$2`,
		id.Provider, id.ExternalID, id.ChatID, id.Username)
	return err
}

func (s *Store) UserByID(ctx context.Context, id int64) (domain.User, error) {
	u, err := scanUser(s.q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users u WHERE u.id=$1`, id))
	if notFound(err) {
		return u, domain.ErrNotFound
	}
	return u, err
}

func (s *Store) Identities(ctx context.Context, userID int64) ([]Identity, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT provider, external_id, chat_id, username FROM identities WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		var i Identity
		if err := rows.Scan(&i.Provider, &i.ExternalID, &i.ChatID, &i.Username); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) SetName(ctx context.Context, id int64, name string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE users SET name=$2 WHERE id=$1`, id, name)
	return err
}

func (s *Store) SetRole(ctx context.Context, id int64, r domain.Role) error {
	_, err := s.q.ExecContext(ctx, `UPDATE users SET role=$2 WHERE id=$1`, id, string(r))
	return err
}

func (s *Store) SetDebugRole(ctx context.Context, id int64, r domain.Role) error {
	var v any
	if r.Valid() {
		v = string(r)
	}
	_, err := s.q.ExecContext(ctx, `UPDATE users SET debug_role=$2 WHERE id=$1`, id, v)
	return err
}

func (s *Store) SetGrade(ctx context.Context, id int64, grade int) error {
	_, err := s.q.ExecContext(ctx, `UPDATE users SET grade=$2 WHERE id=$1`, id, grade)
	return err
}

func (s *Store) SetConsent(ctx context.Context, id int64) error {
	_, err := s.q.ExecContext(ctx, `UPDATE users SET consent_at=now() WHERE id=$1 AND consent_at IS NULL`, id)
	return err
}

func (s *Store) Admins(ctx context.Context) ([]domain.User, error) {
	return s.queryUsers(ctx, `SELECT `+userCols+` FROM users u WHERE u.role='admin' ORDER BY u.id`)
}

// ---- родители и дети ----

func (s *Store) Link(ctx context.Context, parentID, studentID int64) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO parent_links(parent_id, student_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, parentID, studentID)
	return err
}

func (s *Store) ParentsOf(ctx context.Context, studentID int64) ([]domain.User, error) {
	return s.queryUsers(ctx, `SELECT `+userCols+` FROM users u JOIN parent_links p ON p.parent_id=u.id
		WHERE p.student_id=$1 ORDER BY u.id`, studentID)
}

func (s *Store) ChildrenOf(ctx context.Context, parentID int64) ([]domain.User, error) {
	return s.queryUsers(ctx, `SELECT `+userCols+` FROM users u JOIN parent_links p ON p.student_id=u.id
		WHERE p.parent_id=$1 ORDER BY u.id`, parentID)
}

func (s *Store) IsParentOf(ctx context.Context, parentID, studentID int64) (bool, error) {
	var ok bool
	err := s.q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM parent_links WHERE parent_id=$1 AND student_id=$2)`, parentID, studentID).Scan(&ok)
	return ok, err
}

// CreateManagedChild — ребёнок без мессенджера, которым управляет родитель.
func (s *Store) CreateManagedChild(ctx context.Context, parentID int64, name string, grade int) (int64, error) {
	var id int64
	err := s.Tx(ctx, func(t *Store) error {
		if err := t.q.QueryRowContext(ctx, `INSERT INTO users(name, role, grade, managed_by, consent_at)
			VALUES ($1,'student',$2,$3, now()) RETURNING id`, name, grade, parentID).Scan(&id); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(ctx, `INSERT INTO user_settings(user_id, onboarded) VALUES ($1, true)`, id); err != nil {
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
	var st Settings
	err := s.q.QueryRowContext(ctx, `SELECT remind_morning, remind_hour, reschedule_needs_parent, onboarded
		FROM user_settings WHERE user_id=$1`, userID).Scan(&st.RemindMorning, &st.RemindHour, &st.RescheduleNeedsParent, &st.Onboarded)
	if notFound(err) {
		return Settings{true, true, true, false}, nil
	}
	return st, err
}

func (s *Store) SaveSettings(ctx context.Context, userID int64, st Settings) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO user_settings(user_id, remind_morning, remind_hour, reschedule_needs_parent, onboarded)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (user_id) DO UPDATE SET remind_morning=$2, remind_hour=$3,
		reschedule_needs_parent=$4, onboarded=$5`, userID, st.RemindMorning, st.RemindHour, st.RescheduleNeedsParent, st.Onboarded)
	return err
}

// ---- приглашения ----

type Invite struct {
	Code      string
	Kind      string
	CreatedBy int64
	TargetID  int64
}

func (s *Store) CreateInvite(ctx context.Context, inv Invite, ttl time.Duration) error {
	var target any
	if inv.TargetID != 0 {
		target = inv.TargetID
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO invites(code, kind, created_by, target_id, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		inv.Code, inv.Kind, inv.CreatedBy, target, time.Now().Add(ttl))
	return err
}

// UseInvite помечает приглашение использованным и возвращает его. Повторное использование — ошибка.
func (s *Store) UseInvite(ctx context.Context, code string) (Invite, error) {
	var inv Invite
	var target sql.NullInt64
	err := s.q.QueryRowContext(ctx, `UPDATE invites SET used_at=now()
		WHERE code=$1 AND used_at IS NULL AND expires_at > now()
		RETURNING code, kind, created_by, target_id`, code).Scan(&inv.Code, &inv.Kind, &inv.CreatedBy, &target)
	if notFound(err) {
		return inv, domain.ErrInviteInvalid
	}
	inv.TargetID = target.Int64
	return inv, err
}

// ---- диалоговое состояние ----

func (s *Store) State(ctx context.Context, userID int64) (string, map[string]string, error) {
	var state string
	var raw []byte
	err := s.q.QueryRowContext(ctx, `SELECT state, data FROM user_states WHERE user_id=$1`, userID).Scan(&state, &raw)
	if notFound(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	data := map[string]string{}
	_ = json.Unmarshal(raw, &data)
	return state, data, nil
}

func (s *Store) SetState(ctx context.Context, userID int64, state string, data map[string]string) error {
	if data == nil {
		data = map[string]string{}
	}
	raw, _ := json.Marshal(data)
	_, err := s.q.ExecContext(ctx, `INSERT INTO user_states(user_id, state, data, updated_at) VALUES ($1,$2,$3, now())
		ON CONFLICT (user_id) DO UPDATE SET state=$2, data=$3, updated_at=now()`, userID, state, raw)
	return err
}

func (s *Store) ClearState(ctx context.Context, userID int64) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM user_states WHERE user_id=$1`, userID)
	return err
}
