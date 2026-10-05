package store

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store/db"
)

const settingLessonPrice = "lesson_price"

func (s *Store) BasePrice(ctx context.Context) (int64, error) {
	v, err := s.qs().GetSetting(ctx, settingLessonPrice)
	if notFound(err) {
		return 150000, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func (s *Store) SetBasePrice(ctx context.Context, kopecks int64) error {
	return s.qs().UpsertSetting(ctx, db.UpsertSettingParams{Key: settingLessonPrice, Value: strconv.FormatInt(kopecks, 10)})
}

func productFromDB(p db.Product) domain.Product {
	return domain.Product{ID: int(p.ID), Name: p.Name, Kind: domain.ProductKind(p.Kind), Lessons: int(p.Lessons),
		DiscountPct: int(p.DiscountPct), ValidDays: int(p.ValidDays.Int32), Active: p.Active}
}

func (s *Store) Products(ctx context.Context, onlyActive bool) ([]domain.Product, error) {
	rows, err := s.qs().ListProducts(ctx, onlyActive)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Product, len(rows))
	for i, p := range rows {
		out[i] = productFromDB(p)
	}
	return out, nil
}

func (s *Store) Product(ctx context.Context, id int) (domain.Product, error) {
	p, err := s.qs().ProductByID(ctx, int32(id))
	if notFound(err) {
		return domain.Product{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Product{}, err
	}
	return productFromDB(p), nil
}

func (s *Store) AddProduct(ctx context.Context, p domain.Product) error {
	return s.qs().InsertProduct(ctx, db.InsertProductParams{Name: p.Name, Kind: string(p.Kind), Lessons: int32(p.Lessons),
		DiscountPct: int32(p.DiscountPct), ValidDays: sql.NullInt32{Int32: int32(p.ValidDays), Valid: p.ValidDays > 0}})
}

func (s *Store) ToggleProduct(ctx context.Context, id int) error {
	return s.qs().ToggleProduct(ctx, int32(id))
}

type Payment struct {
	PayerID, StudentID, TutorID int64
	ProductID                   int
	Amount                      int64
}

func (s *Store) InsertPayment(ctx context.Context, p Payment) (int64, error) {
	return s.qs().InsertPayment(ctx, db.InsertPaymentParams{PayerID: p.PayerID, StudentID: p.StudentID, TutorID: p.TutorID,
		ProductID: int32(p.ProductID), Amount: p.Amount})
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
	p := db.InsertLedgerParams{StudentID: e.StudentID, TutorID: e.TutorID, Delta: int32(e.Delta), UnitPrice: e.UnitPrice,
		Reason: e.Reason, PaymentID: nullID(e.PaymentID), LessonID: nullID(e.LessonID)}
	if e.ExpiresAt != nil {
		p.ExpiresAt = sql.NullTime{Time: *e.ExpiresAt, Valid: true}
	}
	return s.qs().InsertLedger(ctx, p)
}

// Balance — сколько оплаченных уроков осталось у пары ученик–репетитор.
func (s *Store) Balance(ctx context.Context, studentID, tutorID int64) (int, error) {
	n, err := s.qs().Balance(ctx, db.BalanceParams{StudentID: studentID, TutorID: tutorID})
	return int(n), err
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

func feedbackFromDB(f db.FeedbackDetail) Feedback {
	return Feedback{ID: f.ID, LessonID: f.LessonID, StudentID: f.StudentID, StudentName: f.StudentName, TutorID: f.TutorID,
		TutorName: f.TutorName, Liked: f.Liked, Comment: f.Comment, Forwarded: f.ForwardedAt.Valid, CreatedAt: f.CreatedAt}
}

// InsertFeedback — ok=false, если отзыв на этот урок уже оставлен.
func (s *Store) InsertFeedback(ctx context.Context, lessonID, studentID, tutorID int64, liked bool) (int64, bool, error) {
	id, err := s.qs().InsertFeedback(ctx, db.InsertFeedbackParams{LessonID: lessonID, StudentID: studentID, TutorID: tutorID, Liked: liked})
	if notFound(err) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (s *Store) SetFeedbackComment(ctx context.Context, id int64, comment string) error {
	return s.qs().SetFeedbackComment(ctx, db.SetFeedbackCommentParams{ID: id, Comment: comment})
}

func (s *Store) Feedback(ctx context.Context, id int64) (Feedback, error) {
	f, err := s.qs().FeedbackByID(ctx, id)
	if notFound(err) {
		return Feedback{}, domain.ErrNotFound
	}
	if err != nil {
		return Feedback{}, err
	}
	return feedbackFromDB(f), nil
}

func (s *Store) RecentFeedback(ctx context.Context, limit int) ([]Feedback, error) {
	rows, err := s.qs().RecentFeedback(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]Feedback, len(rows))
	for i, f := range rows {
		out[i] = feedbackFromDB(f)
	}
	return out, nil
}

// MarkForwarded — ok=false, если уже пересылали.
func (s *Store) MarkForwarded(ctx context.Context, id int64) (bool, error) {
	n, err := s.qs().MarkForwarded(ctx, id)
	return n == 1, err
}

// LikedTutor — понравился ли ученику пробный урок у этого репетитора.
func (s *Store) LikedTutor(ctx context.Context, studentID, tutorID int64) (bool, error) {
	return s.qs().LikedTutor(ctx, db.LikedTutorParams{StudentID: studentID, TutorID: tutorID})
}

// ---- доступ в закрытый канал ----

// AccessFacts — факты для правила domain.AccessEligible и признак, открыт ли доступ сейчас.
// В запросе «нет значения» кодируется эпохой Unix, здесь превращается в nil.
func (s *Store) AccessFacts(ctx context.Context, studentID int64, now time.Time) (domain.AccessFacts, bool, error) {
	r, err := s.qs().AccessFacts(ctx, db.AccessFactsParams{StudentID: studentID, Now: now})
	if err != nil {
		return domain.AccessFacts{}, false, err
	}
	orNil := func(t time.Time) *time.Time {
		if t.Unix() <= 0 {
			return nil
		}
		return &t
	}
	return domain.AccessFacts{Liked: r.Liked, GrantedAt: orNil(r.GrantedAt), LastHeld: orNil(r.LastHeld), HasUpcoming: r.HasUpcoming},
		r.Active, nil
}

// GrantAccess открывает доступ. ok=false — доступ уже был открыт (например, параллельным процессом).
func (s *Store) GrantAccess(ctx context.Context, studentID int64) (bool, error) {
	n, err := s.qs().GrantAccess(ctx, studentID)
	return n == 1, err
}

// RevokeAccess закрывает доступ. ok=false — уже закрыт.
func (s *Store) RevokeAccess(ctx context.Context, studentID int64) (bool, error) {
	n, err := s.qs().RevokeAccess(ctx, studentID)
	return n == 1, err
}

// AccessCandidates — ученики, которым понравился пробный, или у кого доступ сейчас открыт.
func (s *Store) AccessCandidates(ctx context.Context) ([]int64, error) {
	return s.qs().AccessCandidates(ctx)
}
