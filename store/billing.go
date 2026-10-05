package store

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
)

func (s *Store) BasePrice(ctx context.Context) (int64, error) {
	var v string
	err := s.q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key='lesson_price'`).Scan(&v)
	if notFound(err) {
		return 150000, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func (s *Store) SetBasePrice(ctx context.Context, kopecks int64) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO app_settings(key, value) VALUES ('lesson_price', $1)
		ON CONFLICT (key) DO UPDATE SET value=$1`, strconv.FormatInt(kopecks, 10))
	return err
}

func (s *Store) Products(ctx context.Context, onlyActive bool) ([]domain.Product, error) {
	q := `SELECT id, name, kind, lessons, discount_pct, COALESCE(valid_days,0), active FROM products`
	if onlyActive {
		q += ` WHERE active`
	}
	rows, err := s.q.QueryContext(ctx, q+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Product
	for rows.Next() {
		var p domain.Product
		var kind string
		if err := rows.Scan(&p.ID, &p.Name, &kind, &p.Lessons, &p.DiscountPct, &p.ValidDays, &p.Active); err != nil {
			return nil, err
		}
		p.Kind = domain.ProductKind(kind)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Product(ctx context.Context, id int) (domain.Product, error) {
	ps, err := s.Products(ctx, false)
	if err != nil {
		return domain.Product{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.Product{}, domain.ErrNotFound
}

func (s *Store) AddProduct(ctx context.Context, p domain.Product) error {
	var valid any
	if p.ValidDays > 0 {
		valid = p.ValidDays
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO products(name, kind, lessons, discount_pct, valid_days) VALUES ($1,$2,$3,$4,$5)`,
		p.Name, string(p.Kind), p.Lessons, p.DiscountPct, valid)
	return err
}

func (s *Store) ToggleProduct(ctx context.Context, id int) error {
	_, err := s.q.ExecContext(ctx, `UPDATE products SET active = NOT active WHERE id=$1`, id)
	return err
}

type Payment struct {
	PayerID, StudentID, TutorID int64
	ProductID                   int
	Amount                      int64
}

func (s *Store) InsertPayment(ctx context.Context, p Payment) (int64, error) {
	var id int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO payments(payer_id, student_id, tutor_id, product_id, amount) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		p.PayerID, p.StudentID, p.TutorID, p.ProductID, p.Amount).Scan(&id)
	return id, err
}

type LedgerEntry struct {
	StudentID, TutorID int64
	Delta              int
	UnitPrice          int64
	Reason             string
	PaymentID          int64
	LessonID           int64
	ExpiresAt          *time.Time
}

// AddLedger добавляет запись в журнал. Списание за урок идемпотентно (уникальный индекс по lesson_id).
func (s *Store) AddLedger(ctx context.Context, e LedgerEntry) error {
	nullID := func(v int64) any {
		if v == 0 {
			return nil
		}
		return v
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO ledger(student_id, tutor_id, delta, unit_price, reason, payment_id, lesson_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, e.StudentID, e.TutorID, e.Delta, e.UnitPrice, e.Reason,
		nullID(e.PaymentID), nullID(e.LessonID), e.ExpiresAt)
	return err
}

// Balance — сколько оплаченных уроков осталось у пары ученик–репетитор.
func (s *Store) Balance(ctx context.Context, studentID, tutorID int64) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(delta),0) FROM ledger WHERE student_id=$1 AND tutor_id=$2`, studentID, tutorID).Scan(&n)
	return n, err
}

// ---- отзывы о пробных ----

type Feedback struct {
	ID          int64
	LessonID    int64
	StudentID   int64
	StudentName string
	TutorID     int64
	TutorName   string
	Liked       bool
	Comment     string
	Forwarded   bool
	CreatedAt   time.Time
}

// InsertFeedback — ok=false, если отзыв на этот урок уже оставлен.
func (s *Store) InsertFeedback(ctx context.Context, lessonID, studentID, tutorID int64, liked bool) (int64, bool, error) {
	var id int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO trial_feedback(lesson_id, student_id, tutor_id, liked) VALUES ($1,$2,$3,$4)
		ON CONFLICT (lesson_id) DO NOTHING RETURNING id`, lessonID, studentID, tutorID, liked).Scan(&id)
	if notFound(err) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (s *Store) SetFeedbackComment(ctx context.Context, id int64, comment string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE trial_feedback SET comment=$2 WHERE id=$1`, id, comment)
	return err
}

const feedbackSelect = `SELECT f.id, f.lesson_id, f.student_id, su.name, f.tutor_id, tu.name, f.liked, f.comment, f.forwarded_at IS NOT NULL, f.created_at
	FROM trial_feedback f JOIN users su ON su.id=f.student_id JOIN users tu ON tu.id=f.tutor_id `

func (s *Store) feedbacks(ctx context.Context, where string, args ...any) ([]Feedback, error) {
	rows, err := s.q.QueryContext(ctx, feedbackSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.LessonID, &f.StudentID, &f.StudentName, &f.TutorID, &f.TutorName, &f.Liked, &f.Comment, &f.Forwarded, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Feedback(ctx context.Context, id int64) (Feedback, error) {
	fs, err := s.feedbacks(ctx, `WHERE f.id=$1`, id)
	if err != nil {
		return Feedback{}, err
	}
	if len(fs) == 0 {
		return Feedback{}, domain.ErrNotFound
	}
	return fs[0], nil
}

func (s *Store) RecentFeedback(ctx context.Context, limit int) ([]Feedback, error) {
	return s.feedbacks(ctx, `ORDER BY f.created_at DESC LIMIT $1`, limit)
}

// MarkForwarded — ok=false, если уже пересылали.
func (s *Store) MarkForwarded(ctx context.Context, id int64) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE trial_feedback SET forwarded_at=now() WHERE id=$1 AND forwarded_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// LikedTutor — понравился ли ученику пробный урок у этого репетитора.
func (s *Store) LikedTutor(ctx context.Context, studentID, tutorID int64) (bool, error) {
	var ok bool
	err := s.q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trial_feedback WHERE student_id=$1 AND tutor_id=$2 AND liked)`, studentID, tutorID).Scan(&ok)
	return ok, err
}

// ---- доступ в закрытый канал ----

func (s *Store) AccessFacts(ctx context.Context, studentID int64, now time.Time) (domain.AccessFacts, bool, error) {
	var f domain.AccessFacts
	var granted, lastHeld sql.NullTime
	var active sql.NullBool
	err := s.q.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM trial_feedback WHERE student_id=$1 AND liked),
		(SELECT granted_at FROM channel_access WHERE student_id=$1 AND active),
		(SELECT active FROM channel_access WHERE student_id=$1),
		(SELECT max(starts_at) FROM lessons WHERE student_id=$1 AND status='held'),
		EXISTS(SELECT 1 FROM lessons WHERE student_id=$1 AND status='scheduled' AND starts_at > $2)`, studentID, now).
		Scan(&f.Liked, &granted, &active, &lastHeld, &f.HasUpcoming)
	if granted.Valid {
		f.GrantedAt = &granted.Time
	}
	if lastHeld.Valid {
		f.LastHeld = &lastHeld.Time
	}
	return f, active.Valid && active.Bool, err
}

// GrantAccess открывает доступ. ok=false — доступ уже был открыт (например, параллельным процессом).
func (s *Store) GrantAccess(ctx context.Context, studentID int64) (bool, error) {
	res, err := s.q.ExecContext(ctx, `INSERT INTO channel_access(student_id) VALUES ($1)
		ON CONFLICT (student_id) DO UPDATE SET active=true, granted_at=now(), revoked_at=NULL
		WHERE NOT channel_access.active`, studentID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RevokeAccess закрывает доступ. ok=false — уже закрыт.
func (s *Store) RevokeAccess(ctx context.Context, studentID int64) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE channel_access SET active=false, revoked_at=now() WHERE student_id=$1 AND active`, studentID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// AccessCandidates — ученики, которым понравился пробный, или у кого доступ сейчас открыт.
func (s *Store) AccessCandidates(ctx context.Context) ([]int64, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT student_id FROM trial_feedback WHERE liked
		UNION SELECT student_id FROM channel_access WHERE active`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
