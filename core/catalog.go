package core

import (
	"context"

	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/store"
)

// Запросы ученика и родителя. Всё, что касается конкретного ученика, проверяет,
// что пользователь — сам ученик, его родитель или админ.

func (a *App) Subjects(ctx context.Context) ([]store.Subject, error) {
	return a.S.Subjects(ctx)
}

type TutorOption struct {
	Tutor    store.Tutor
	Enrolled bool // ученик уже занимается у этого репетитора
}

func (a *App) TutorsForSubject(ctx context.Context, u domain.User, studentID int64, subjectID int) ([]TutorOption, error) {
	if err := a.canActForStudent(ctx, u, studentID); err != nil {
		return nil, err
	}
	ts, err := a.S.TutorsBySubject(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	out := make([]TutorOption, len(ts))
	for i, t := range ts {
		ok, err := a.S.IsEnrolled(ctx, studentID, t.ID, subjectID)
		if err != nil {
			return nil, err
		}
		out[i] = TutorOption{t, ok}
	}
	return out, nil
}

// TutorOffer — карточка репетитора для конкретного ученика: цена и что ему сейчас доступно.
type TutorOffer struct {
	Tutor     store.Tutor
	Price     int64
	Enrolled  bool // можно записываться на постоянное время и разовые уроки
	Liked     bool // пробный понравился — можно закрепиться
	TrialUsed bool // пробный уже записан или проведён
}

func (a *App) TutorOffer(ctx context.Context, u domain.User, studentID int64, subjectID int, tutorID int64) (TutorOffer, error) {
	if err := a.canActForStudent(ctx, u, studentID); err != nil {
		return TutorOffer{}, err
	}
	var o TutorOffer
	var err error
	if o.Tutor, err = a.S.Tutor(ctx, tutorID); err != nil {
		return o, err
	}
	if o.Price, err = a.tutorUnitPrice(ctx, tutorID); err != nil {
		return o, err
	}
	if o.Enrolled, err = a.S.IsEnrolled(ctx, studentID, tutorID, subjectID); err != nil {
		return o, err
	}
	if o.TrialUsed, err = a.S.HasTrial(ctx, studentID, tutorID); err != nil {
		return o, err
	}
	o.Liked, err = a.S.LikedTutor(ctx, studentID, tutorID)
	return o, err
}

func (a *App) StudentLessons(ctx context.Context, u domain.User, studentID int64, limit int) ([]domain.Lesson, error) {
	if err := a.canActForStudent(ctx, u, studentID); err != nil {
		return nil, err
	}
	return a.S.UpcomingForStudent(ctx, studentID, limit)
}

// Lesson — урок, если пользователь его участник (ученик, родитель, репетитор) или админ.
func (a *App) Lesson(ctx context.Context, u domain.User, id int64) (domain.Lesson, error) {
	l, err := a.S.Lesson(ctx, id)
	if err != nil {
		return l, err
	}
	if u.ID == l.TutorID || u.EffectiveRole(a.Opt.Debug) == domain.RoleAdmin {
		return l, nil
	}
	if err := a.canActForStudent(ctx, u, l.StudentID); err != nil {
		return domain.Lesson{}, err
	}
	return l, nil
}

type EnrollmentBalance struct {
	store.Enrollment
	Balance int
}

// Balances — у каких репетиторов занимается ученик и сколько оплаченных уроков осталось.
func (a *App) Balances(ctx context.Context, u domain.User, studentID int64) ([]EnrollmentBalance, error) {
	if err := a.canActForStudent(ctx, u, studentID); err != nil {
		return nil, err
	}
	ens, err := a.S.ActiveEnrollments(ctx, studentID)
	if err != nil {
		return nil, err
	}
	out := make([]EnrollmentBalance, len(ens))
	for i, en := range ens {
		bal, err := a.S.Balance(ctx, studentID, en.TutorID)
		if err != nil {
			return nil, err
		}
		out[i] = EnrollmentBalance{en, bal}
	}
	return out, nil
}

type ProductOffer struct {
	Product domain.Product
	Price   int64 // итоговая цена с учётом цены репетитора и скидки
}

// Offers — активные тарифы с ценами для пары ученик–репетитор.
func (a *App) Offers(ctx context.Context, u domain.User, studentID, tutorID int64) ([]ProductOffer, error) {
	if err := a.canActForStudent(ctx, u, studentID); err != nil {
		return nil, err
	}
	unit, err := a.tutorUnitPrice(ctx, tutorID)
	if err != nil {
		return nil, err
	}
	ps, err := a.S.Products(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]ProductOffer, len(ps))
	for i, p := range ps {
		out[i] = ProductOffer{p, domain.PriceOf(p, unit)}
	}
	return out, nil
}
