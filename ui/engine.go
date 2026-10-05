// Package ui — сценарии диалога с пользователем, не зависящие от мессенджера.
// Адаптер (Telegram, позже VK/MAX) превращает апдейты в Input и рисует возвращённые msg.Message.
package ui

import (
	"context"
	"errors"
	"github.com/SMbyM/tutoring-bot/actions"
	"log/slog"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

type Input struct {
	Provider   string
	ExternalID string
	ChatID     string
	Username   string
	FirstName  string
	Text       string // текст сообщения (в т.ч. команды)
	Action     string // нажатая кнопка
}

type Engine struct {
	App        *core.App
	S          *store.Store
	AdminIDs   map[string]bool     // external id администраторов (из конфигурации)
	BotLink    func(string) string // ссылка-приглашение с payload для /start
	MiniAppURL string
	PolicyURL  string
	Log        *slog.Logger
}

func NewEngine(app *core.App, adminIDs map[string]bool, miniAppURL, policyURL string) *Engine {
	return &Engine{App: app, S: app.S, AdminIDs: adminIDs, MiniAppURL: miniAppURL, PolicyURL: policyURL,
		Log: slog.Default(), BotLink: func(p string) string { return "/start " + p }}
}

func (e *Engine) debug() bool { return e.App.Opt.Debug }

// req — состояние обработки одного входящего события.
type req struct {
	ctx      context.Context
	e        *Engine
	in       Input
	u        domain.User
	role     domain.Role
	callback bool
	out      []msg.Message
}

func (r *req) loc() *time.Location { return r.u.Location() }

// screen — навигационный экран: при нажатии кнопки заменяет текущее сообщение.
func (r *req) screen(text string, rows ...[]msg.Button) {
	r.out = append(r.out, msg.Message{Text: text, Buttons: rows, Replace: r.callback && !r.hasVisible()})
}

func (r *req) hasVisible() bool {
	for _, m := range r.out {
		if m.Text != "" {
			return true
		}
	}
	return false
}

// send — всегда новое сообщение.
func (r *req) send(text string, rows ...[]msg.Button) {
	r.out = append(r.out, msg.Message{Text: text, Buttons: rows})
}

func (r *req) toast(t string) {
	r.out = append(r.out, msg.Message{Toast: t})
}

// fail показывает пользователю понятную ошибку, внутренние — логирует.
func (r *req) fail(err error) {
	if err == nil {
		return
	}
	known := []error{domain.ErrSlotTaken, domain.ErrTrialUsed, domain.ErrNotAllowed, domain.ErrNotFound, domain.ErrInPast,
		domain.ErrNotScheduled, domain.ErrReasonRequired, domain.ErrNotEnrolled, domain.ErrOutsideWindows,
		domain.ErrInviteInvalid, domain.ErrNoChildSelected}
	for _, k := range known {
		if errors.Is(err, k) {
			r.send("⚠️ " + k.Error())
			return
		}
	}
	var ie domain.InputError
	if errors.As(err, &ie) {
		r.send("⚠️ " + ie.Error())
		return
	}
	var ue userError
	if errors.As(err, &ue) {
		r.send("⚠️ " + ue.Error())
		return
	}
	r.e.Log.Error("ошибка обработки", "user", r.u.ID, "action", r.in.Action, "text", r.in.Text, "err", err)
	r.send("😵 Что-то пошло не так. Попробуйте ещё раз или напишите администратору.")
}

// userError — ошибка с текстом для пользователя (например, неверный формат ввода).
type userError struct{ error }

func uerr(err error) error { return userError{err} }

// Handle обрабатывает одно входящее событие и возвращает ответы для этого же чата.
func (e *Engine) Handle(ctx context.Context, in Input) []msg.Message {
	r := &req{ctx: ctx, e: e, in: in, callback: in.Action != ""}
	if err := e.handle(r); err != nil {
		r.fail(err)
	}
	return r.out
}

func (e *Engine) handle(r *req) error {
	ctx, in := r.ctx, r.in
	id := store.Identity{Provider: in.Provider, ExternalID: in.ExternalID, ChatID: in.ChatID, Username: in.Username}
	isAdmin := in.Provider == "tg" && e.AdminIDs[in.ExternalID]
	u, err := e.App.Identify(ctx, id, isAdmin)
	if err != nil {
		return err
	}
	r.u = u
	r.role = u.EffectiveRole(e.debug())

	text := strings.TrimSpace(in.Text)
	switch {
	case strings.HasPrefix(text, "/start"):
		_ = e.S.ClearState(ctx, u.ID)
		if arg := strings.TrimSpace(strings.TrimPrefix(text, "/start")); arg != "" {
			if err := e.useInvite(r, arg); err != nil {
				r.fail(err)
			}
		}
		return e.home(r)
	case text == "/menu" || text == "/cancel" || text == "Меню":
		_ = e.S.ClearState(ctx, u.ID)
		return e.home(r)
	case text == "/debug":
		return e.debugMenu(r)
	case text == "/windows":
		return e.tutorWindows(r)
	}

	if in.Action != "" {
		a, err := actions.Decode(in.Action)
		if err != nil {
			r.toast("Кнопка устарела")
			return nil
		}
		return e.action(r, a)
	}
	if text != "" {
		state, data, err := e.S.State(ctx, u.ID)
		if err != nil {
			return err
		}
		if state != "" {
			return e.onText(r, state, data, text)
		}
	}
	return e.home(r)
}

// useInvite — /start p_<код> (ученик позвал родителя), c_<код> (родитель позвал ребёнка), t_<код> (админ позвал репетитора).
func (e *Engine) useInvite(r *req, payload string) error {
	text, err := e.App.AcceptInvite(r.ctx, r.u, payload)
	if err != nil {
		return err
	}
	r.send(text)
	// роль могла измениться — перечитываем пользователя
	u, _, err := e.App.FindUser(r.ctx, r.in.Provider, r.in.ExternalID)
	if err != nil {
		return err
	}
	r.u, r.role = u, u.EffectiveRole(e.debug())
	return nil
}

// home — главное меню или следующий шаг регистрации.
func (e *Engine) home(r *req) error {
	u := r.u
	if !u.Consent {
		text := "Здравствуйте! Это бот школы репетиторов: запись на занятия, расписание и напоминания.\n\n" +
			"Мы храним только имя, класс и расписание занятий. Нажимая «Согласен», вы соглашаетесь на обработку этих данных. " +
			"Если ученику нет 18 лет, согласие даёт родитель — привяжите его аккаунт в меню после регистрации."
		rows := [][]msg.Button{msg.Row(msg.Btn("✅ Согласен", actions.Consent{}))}
		if e.PolicyURL != "" {
			rows = append([][]msg.Button{msg.Row(msg.Link("📄 Политика обработки данных", e.PolicyURL))}, rows...)
		}
		r.screen(text, rows...)
		return nil
	}
	if u.Role == domain.RoleNone {
		r.screen("Кто вы?", msg.Row(msg.Btn("🎒 Ученик", actions.ChooseRole{Role: string(domain.RoleStudent)}), msg.Btn("👨‍👩‍👧 Родитель", actions.ChooseRole{Role: string(domain.RoleParent)})))
		return nil
	}
	if strings.TrimSpace(u.Name) == "" {
		return e.askName(r)
	}
	if u.Role == domain.RoleStudent && u.Grade == 0 && u.ManagedBy == 0 {
		r.screen("В каком вы классе?", gradeRows(func(g int) actions.Action { return actions.SetGrade{Grade: g} })...)
		return nil
	}
	st, err := e.App.Settings(r.ctx, u)
	if err != nil {
		return err
	}
	if !st.Onboarded {
		return e.settingsScreen(r, true)
	}
	return e.mainMenu(r)
}

func (e *Engine) askName(r *req) error {
	if err := e.S.SetState(r.ctx, r.u.ID, "name", nil); err != nil {
		return err
	}
	hint := ""
	if r.in.FirstName != "" {
		hint = "\n\nИли нажмите кнопку, чтобы оставить имя из профиля."
	}
	var rows [][]msg.Button
	if r.in.FirstName != "" {
		rows = append(rows, msg.Row(msg.Btn("Оставить «"+trim(r.in.FirstName, 20)+"»", actions.KeepProfileName{})))
	}
	r.screen("Как к вам обращаться? Напишите имя (можно без фамилии)."+hint, rows...)
	return nil
}

func gradeRows(mk func(grade int) actions.Action) [][]msg.Button {
	var rows [][]msg.Button
	var row []msg.Button
	for g := 1; g <= 11; g++ {
		row = append(row, msg.Btn(itoa(g), mk(g)))
		if len(row) == 6 {
			rows = append(rows, row)
			row = nil
		}
	}
	return append(rows, row)
}

func (e *Engine) mainMenu(r *req) error {
	var rows [][]msg.Button
	title := "Главное меню"
	switch r.role {
	case domain.RoleStudent:
		rows = [][]msg.Button{
			msg.Row(msg.Btn("📅 Мои уроки", actions.StudentLessons{StudentID: r.u.ID})),
			msg.Row(msg.Btn("➕ Записаться на урок", actions.PickSubject{StudentID: r.u.ID})),
			msg.Row(msg.Btn("💳 Оплата и баланс", actions.Balances{StudentID: r.u.ID})),
			msg.Row(msg.Btn("👨‍👩‍👧 Привязать родителя", actions.InviteParent{}), msg.Btn("⚙️ Настройки", actions.Settings{})),
		}
	case domain.RoleParent:
		kids, err := e.App.Children(r.ctx, r.u)
		if err != nil {
			return err
		}
		for _, k := range kids {
			rows = append(rows, msg.Row(msg.Btn("👤 "+k.Name, actions.Child{KidID: k.ID})))
		}
		if len(kids) == 0 {
			title += "\n\nПока не привязано ни одного ребёнка."
		}
		rows = append(rows,
			msg.Row(msg.Btn("➕ Ребёнок с Telegram", actions.InviteChild{}), msg.Btn("➕ Ребёнок без Telegram", actions.AddChild{})),
			msg.Row(msg.Btn("⚙️ Мои настройки", actions.Settings{})))
	case domain.RoleTutor:
		rows = [][]msg.Button{
			msg.Row(msg.Btn("📅 Мои уроки", actions.TutorLessons{})),
			msg.Row(msg.Btn("🕒 Окна расписания", actions.Windows{}), msg.Btn("👥 Ученики", actions.MyStudents{})),
			msg.Row(msg.Btn("👤 Профиль", actions.Profile{}), msg.Btn("⚙️ Настройки", actions.Settings{})),
		}
	case domain.RoleAdmin:
		rows = [][]msg.Button{
			msg.Row(msg.Btn("👩‍🏫 Репетиторы", actions.AdminTutors{}), msg.Btn("➕ Пригласить репетитора", actions.InviteTutor{})),
			msg.Row(msg.Btn("💰 Цены и тарифы", actions.Prices{}), msg.Btn("📚 Предметы", actions.Subjects{})),
			msg.Row(msg.Btn("💬 Отзывы о пробных", actions.FeedbackList{}), msg.Btn("⚠️ Поздние отмены", actions.LateCancels{})),
			msg.Row(msg.Btn("⚙️ Настройки", actions.Settings{})),
		}
	}
	if r.u.CanSwitchRoles(e.debug()) {
		rows = append(rows, msg.Row(msg.Btn("🧪 Тест: роль «"+r.role.Title()+"»", actions.Debug{})))
	}
	who := r.u.Name
	if who != "" {
		title = who + ", " + strings.ToLower(title[:1]) + title[1:]
	}
	r.screen(title, rows...)
	return nil
}

// onText — ответы на вопросы бота (имя, причина отмены и т.п.).
func (e *Engine) onText(r *req, state string, data map[string]string, text string) error {
	ctx, u := r.ctx, r.u
	clear := func() error { return e.S.ClearState(ctx, u.ID) }
	switch state {
	case "name":
		if err := e.App.SetName(ctx, u, text); err != nil {
			return err
		}
		r.u.Name = strings.TrimSpace(text)
		if err := clear(); err != nil {
			return err
		}
		return e.home(r)
	case "reason":
		if err := clear(); err != nil {
			return err
		}
		return e.applyChange(r, atoi64(data["lesson"]), core.ChangeKind(data["kind"]), unix(data["start"]), trim(text, 300))
	case "fbcomment":
		if err := clear(); err != nil {
			return err
		}
		f, err := e.App.SetFeedbackComment(ctx, u, atoi64(data["fb"]), text)
		if err != nil {
			return err
		}
		rows := [][]msg.Button{msg.Row(msg.Btn("🏠 В меню", actions.Home{}))}
		if f.Liked {
			rows = append([][]msg.Button{msg.Row(msg.Btn("🤝 Заниматься у этого репетитора", actions.Enroll{TutorID: f.TutorID, SubjectID: f.SubjectID, StudentID: f.StudentID}))}, rows...)
		}
		r.send("Спасибо, комментарий передан.", rows...)
		return nil
	case "kidname":
		if err := e.S.SetState(ctx, u.ID, "kidgrade", map[string]string{"name": trim(text, 60)}); err != nil {
			return err
		}
		r.send("В каком классе "+trim(text, 60)+"?", gradeRows(func(g int) actions.Action { return actions.ChildGrade{Grade: g} })...)
		return nil
	}
	if handled, err := e.tutorText(r, state, data, text); handled {
		return err
	}
	if handled, err := e.adminText(r, state, data, text); handled {
		return err
	}
	_ = clear()
	return e.home(r)
}

// action — нажатия кнопок. Действия уже разобраны и проверены пакетом actions.
func (e *Engine) action(r *req, act actions.Action) error {
	ctx, u := r.ctx, r.u
	switch a := act.(type) {
	case actions.Home:
		_ = e.S.ClearState(ctx, u.ID)
		return e.home(r)
	case actions.Consent:
		if err := e.App.AcceptConsent(ctx, u); err != nil {
			return err
		}
		r.u.Consent = true
		return e.home(r)
	case actions.ChooseRole:
		role := domain.Role(a.Role)
		if err := e.App.ChooseRole(ctx, u, role); err != nil {
			return e.home(r)
		}
		r.u.Role, r.role = role, role
		return e.home(r)
	case actions.KeepProfileName:
		if err := e.App.SetName(ctx, u, trim(r.in.FirstName, 60)); err != nil {
			return err
		}
		r.u.Name = r.in.FirstName
		_ = e.S.ClearState(ctx, u.ID)
		return e.home(r)
	case actions.SetGrade:
		if err := e.App.SetGrade(ctx, u, a.Grade); err != nil {
			return err
		}
		r.u.Grade = a.Grade
		return e.home(r)
	case actions.Settings, actions.ToggleSetting, actions.SettingsDone:
		return e.settingsAction(r, act)
	}
	for _, h := range []func(*req, actions.Action) (bool, error){e.studentAction, e.lessonAction, e.parentAction, e.tutorAction, e.adminAction, e.debugAction} {
		if handled, err := h(r, act); handled {
			return err
		}
	}
	r.toast("Кнопка устарела")
	return nil
}
