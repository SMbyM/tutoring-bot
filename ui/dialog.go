package ui

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/actions"
	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
)

// Диалог: бот задаёт вопрос (ask) и ждёт текстовый ответ. Вопрос — типизированная структура,
// хранится в БД вместе с данными; ответ обрабатывает зарегистрированный ниже обработчик.
//
// Правила, которые раньше помнил каждый обработчик сам:
//   - после успешного ответа вопрос снимается автоматически;
//   - при ошибке ввода (domain.InputError, uerr) вопрос остаётся — можно ответить ещё раз;
//   - при любой другой ошибке вопрос снимается, чтобы пользователь не застрял;
//   - обработчик может задать следующий вопрос (ask) — тогда снимать нечего.

type question interface{ kind() string }

type (
	askName            struct{}
	askChildName       struct{}
	askChildGrade      struct{ Name string } // ответ — кнопкой с классом
	askFeedbackComment struct{ FeedbackID int64 }
	askReason          struct {
		LessonID int64
		Kind     core.ChangeKind
		Start    int64 // unix; 0 — отмена
	}
	askWindows    struct{}
	askException  struct{}
	askBio        struct{}
	askTutorPrice struct{ TutorID int64 }
	askBasePrice  struct{}
	askProduct    struct{}
	askSubject    struct{}
)

func (askName) kind() string            { return "name" }
func (askChildName) kind() string       { return "kidname" }
func (askChildGrade) kind() string      { return "kidgrade" }
func (askFeedbackComment) kind() string { return "fbcomment" }
func (askReason) kind() string          { return "reason" }
func (askWindows) kind() string         { return "windows" }
func (askException) kind() string       { return "exception" }
func (askBio) kind() string             { return "bio" }
func (askTutorPrice) kind() string      { return "aprice" }
func (askBasePrice) kind() string       { return "abase" }
func (askProduct) kind() string         { return "aproduct" }
func (askSubject) kind() string         { return "asubject" }

type answerFunc func(e *Engine, r *req, raw json.RawMessage, text string) error

var answers = map[string]answerFunc{}

// on регистрирует обработчик ответа на вопрос типа Q.
func on[Q question](h func(e *Engine, r *req, q Q, text string) error) {
	var zero Q
	answers[zero.kind()] = func(e *Engine, r *req, raw json.RawMessage, text string) error {
		var q Q
		if err := json.Unmarshal(raw, &q); err != nil {
			return err
		}
		return h(e, r, q, text)
	}
}

func init() {
	on((*Engine).answerName)
	on((*Engine).answerChildName)
	on((*Engine).answerChildGrade)
	on((*Engine).answerFeedbackComment)
	on((*Engine).answerReason)
	on((*Engine).answerWindows)
	on((*Engine).answerException)
	on((*Engine).answerBio)
	on((*Engine).answerTutorPrice)
	on((*Engine).answerBasePrice)
	on((*Engine).answerProduct)
	on((*Engine).answerSubject)
}

// ask задаёт вопрос; следующий текст пользователя станет ответом на него.
func (e *Engine) ask(r *req, q question) error {
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	r.asked = true
	return e.S.SetState(r.ctx, r.u.ID, q.kind(), raw)
}

// forget снимает вопрос (например, пользователь ушёл в меню).
func (e *Engine) forget(r *req) error { return e.S.ClearState(r.ctx, r.u.ID) }

// pending — текущий вопрос, если он типа Q.
func pending[Q question](e *Engine, r *req) (Q, bool, error) {
	var q Q
	kind, raw, ok, err := e.S.State(r.ctx, r.u.ID)
	if err != nil || !ok || kind != q.kind() {
		return q, false, err
	}
	err = json.Unmarshal(raw, &q)
	return q, err == nil, err
}

// answer передаёт текст обработчику текущего вопроса. handled=false — вопроса нет.
func (e *Engine) answer(r *req, text string) (bool, error) {
	kind, raw, ok, err := e.S.State(r.ctx, r.u.ID)
	if err != nil || !ok {
		return false, err
	}
	h, known := answers[kind]
	if !known { // вопрос из старой версии бота
		return false, e.forget(r)
	}
	r.asked = false
	err = h(e, r, raw, text)
	if retryable(err) {
		return true, err
	}
	if !r.asked {
		if ferr := e.forget(r); err == nil {
			err = ferr
		}
	}
	return true, err
}

func retryable(err error) bool {
	var ie domain.InputError
	var ue userError
	return errors.As(err, &ie) || errors.As(err, &ue)
}

// ---- обработчики ответов ----

func (e *Engine) answerName(r *req, _ askName, text string) error {
	if err := e.App.SetName(r.ctx, r.u, text); err != nil {
		return err
	}
	r.u.Name = strings.TrimSpace(text)
	return e.home(r)
}

func (e *Engine) answerChildName(r *req, _ askChildName, text string) error {
	name := strings.TrimSpace(text)
	if name == "" {
		return domain.InputError("напишите имя ребёнка")
	}
	if err := e.ask(r, askChildGrade{Name: trim(name, 60)}); err != nil {
		return err
	}
	r.send("В каком классе "+trim(name, 60)+"?", gradeRows(func(g int) actions.Action { return actions.ChildGrade{Grade: g} })...)
	return nil
}

// Класс выбирают кнопкой; на текст — повторяем вопрос с кнопками (вопрос остаётся тем же).
func (e *Engine) answerChildGrade(r *req, q askChildGrade, _ string) error {
	if err := e.ask(r, q); err != nil {
		return err
	}
	r.send("Выберите класс кнопкой — в каком классе "+q.Name+"?", gradeRows(func(g int) actions.Action { return actions.ChildGrade{Grade: g} })...)
	return nil
}

func (e *Engine) answerFeedbackComment(r *req, q askFeedbackComment, text string) error {
	f, err := e.App.SetFeedbackComment(r.ctx, r.u, q.FeedbackID, text)
	if err != nil {
		return err
	}
	rows := [][]msg.Button{msg.Row(msg.Btn("🏠 В меню", actions.Home{}))}
	if f.Liked {
		rows = append([][]msg.Button{msg.Row(msg.Btn("🤝 Заниматься у этого репетитора",
			actions.Enroll{TutorID: f.TutorID, SubjectID: f.SubjectID, StudentID: f.StudentID}))}, rows...)
	}
	r.send("Спасибо, комментарий передан.", rows...)
	return nil
}

func (e *Engine) answerReason(r *req, q askReason, text string) error {
	reason := strings.TrimSpace(text)
	if reason == "" {
		return domain.InputError("напишите причину")
	}
	var start time.Time
	if q.Start != 0 {
		start = time.Unix(q.Start, 0)
	}
	return e.applyChange(r, q.LessonID, q.Kind, start, trim(reason, 300))
}

func (e *Engine) answerWindows(r *req, _ askWindows, text string) error {
	ws, err := domain.ParseWindows(text)
	if err != nil {
		return uerr(err)
	}
	if err := e.App.SetWindows(r.ctx, r.u, ws); err != nil {
		return err
	}
	r.send("✅ Расписание сохранено. Уже записанные уроки не меняются.")
	return e.tutorWindows(r)
}

func (e *Engine) answerException(r *req, _ askException, text string) error {
	from, to, note, err := parseException(text, e.App.Now(), r.loc())
	if err != nil {
		return uerr(err)
	}
	if err := e.App.AddException(r.ctx, r.u, from, to, note); err != nil {
		return err
	}
	r.send("✅ Добавлено. В эти дни записаться к вам не получится. Уже записанные уроки на эти даты перенесите или отмените вручную.")
	return e.tutorWindows(r)
}

func (e *Engine) answerBio(r *req, _ askBio, text string) error {
	if err := e.App.SetBio(r.ctx, r.u, text); err != nil {
		return err
	}
	return e.tutorProfile(r)
}

func (e *Engine) answerTutorPrice(r *req, q askTutorPrice, text string) error {
	v, err := domain.ParseRub(text)
	if err != nil {
		return uerr(err)
	}
	if err := e.App.SetTutorPrice(r.ctx, r.u, q.TutorID, &v); err != nil {
		return err
	}
	return e.adminTutorCard(r, q.TutorID)
}

func (e *Engine) answerBasePrice(r *req, _ askBasePrice, text string) error {
	v, err := domain.ParseRub(text)
	if err != nil {
		return uerr(err)
	}
	if err := e.App.SetBasePrice(r.ctx, r.u, v); err != nil {
		return err
	}
	return e.adminPrices(r)
}

func (e *Engine) answerProduct(r *req, _ askProduct, text string) error {
	p, err := parseProduct(text)
	if err != nil {
		return uerr(err)
	}
	if err := e.App.AddProduct(r.ctx, r.u, p); err != nil {
		return err
	}
	return e.adminPrices(r)
}

func (e *Engine) answerSubject(r *req, _ askSubject, text string) error {
	if err := e.App.AddSubject(r.ctx, r.u, text); err != nil {
		return err
	}
	_, err := e.adminAction(r, actions.Subjects{})
	return err
}
