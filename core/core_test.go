package core_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/outbox"
	"github.com/SMbyM/tutoring-bot/store"
)

// Интеграционные тесты: нужен PostgreSQL, адрес в TEST_DATABASE_URL. База очищается!

type sent struct {
	chat string
	m    msg.Message
}

// fakeSender — адаптер мессенджера для тестов: забирает записи outbox, как настоящий.
type fakeSender struct {
	mu   sync.Mutex
	out  []sent
	gate *fakeGate
	d    *outbox.Dispatcher
}

func (f *fakeSender) Provider() string { return "test" }
func (f *fakeSender) Handle(_ context.Context, it store.OutboxItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch it.Kind {
	case store.OutChannelInvite:
		f.gate.invites = append(f.gate.invites, it.ExternalID)
	case store.OutChannelKick:
		f.gate.kicked = append(f.gate.kicked, it.ExternalID)
		return nil
	}
	f.out = append(f.out, sent{it.ChatID, it.Message})
	return nil
}

// take доставляет очередь и возвращает (и очищает) сообщения, пришедшие в чат.
func (f *fakeSender) take(chat string) []msg.Message {
	for {
		n, err := f.d.DrainOnce(context.Background())
		if err != nil {
			panic(err)
		}
		if n == 0 {
			break
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var got []msg.Message
	var rest []sent
	for _, s := range f.out {
		if s.chat == chat {
			got = append(got, s.m)
		} else {
			rest = append(rest, s)
		}
	}
	f.out = rest
	return got
}

type fakeGate struct {
	invites []string
	kicked  []string
}

func (g *fakeGate) Provider() string { return "test" }
func (g *fakeGate) InviteLink(_ context.Context, name string) (string, error) {
	g.invites = append(g.invites, name)
	return "https://t.me/+invite_" + name, nil
}
func (g *fakeGate) Kick(_ context.Context, ext string) error {
	g.kicked = append(g.kicked, ext)
	return nil
}

type env struct {
	app                                  *core.App
	s                                    *store.Store
	snd                                  *fakeSender
	gate                                 *fakeGate
	admin, tutor, tutor2, kid, mom, kid2 domain.User
	math                                 int
}

func setup(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.ResetForTests(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{s: s, gate: &fakeGate{}}
	e.snd = &fakeSender{gate: e.gate}
	e.snd.d = outbox.New(s, e.snd)
	e.app = core.New(s, core.Options{Debug: true, ChannelEnabled: true})
	mk := func(name string, role domain.Role) domain.User {
		u, err := s.CreateUser(ctx, store.Identity{Provider: "test", ExternalID: name, ChatID: name}, name, role)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	e.admin = mk("admin", domain.RoleAdmin)
	e.tutor = mk("tutor", domain.RoleTutor)
	e.tutor2 = mk("tutor2", domain.RoleTutor)
	e.kid = mk("kid", domain.RoleStudent)
	e.kid2 = mk("kid2", domain.RoleStudent)
	e.mom = mk("mom", domain.RoleParent)
	must(t, s.Link(ctx, e.mom.ID, e.kid.ID))
	subs, _ := s.Subjects(ctx)
	e.math = subs[0].ID
	allWeek := "пн 10:00-20:00\nвт 10:00-20:00\nср 10:00-20:00\nчт 10:00-20:00\nпт 10:00-20:00\nсб 10:00-20:00\nвс 10:00-20:00"
	ws, err := domain.ParseWindows(allWeek)
	must(t, err)
	for _, tu := range []domain.User{e.tutor, e.tutor2} {
		must(t, s.EnsureTutor(ctx, tu.ID))
		must(t, s.ToggleTutorSubject(ctx, tu.ID, e.math))
		must(t, s.ReplaceWindows(ctx, tu.ID, ws))
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func hasText(ms []msg.Message, sub string) bool {
	for _, m := range ms {
		if strings.Contains(m.Text, sub) {
			return true
		}
	}
	return false
}

func TestFullFlow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.app
	realNow := time.Now()

	// --- пробный урок ---
	slots, _, err := a.FreeSlots(ctx, e.tutor.ID, e.kid.ID, 0)
	must(t, err)
	if len(slots) == 0 {
		t.Fatal("нет свободных окон")
	}
	start := slots[len(slots)/2] // где-то в середине горизонта: точно больше 12 часов вперёд
	trial, err := a.BookTrial(ctx, e.kid, e.kid.ID, e.tutor.ID, e.math, start)
	must(t, err)
	if !hasText(e.snd.take("tutor"), "пробный") || !hasText(e.snd.take("mom"), "пробный") {
		t.Error("репетитор и родитель должны узнать о записи на пробный")
	}
	if _, err := a.BookTrial(ctx, e.kid, e.kid.ID, e.tutor.ID, e.math, start.Add(2*time.Hour)); !errors.Is(err, domain.ErrTrialUsed) {
		t.Errorf("второй пробный: %v", err)
	}
	if _, err := a.BookTrial(ctx, e.kid2, e.kid2.ID, e.tutor.ID, e.math, start); !errors.Is(err, domain.ErrSlotTaken) {
		t.Errorf("занятое окно: %v", err)
	}
	if _, err := a.BookTrial(ctx, e.kid2, e.kid.ID, e.tutor2.ID, e.math, start); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("чужой ученик: %v", err)
	}
	if _, err := a.BookOnce(ctx, e.kid, e.kid.ID, e.tutor.ID, e.math, start.Add(3*time.Hour)); !errors.Is(err, domain.ErrNotEnrolled) {
		t.Errorf("обычный урок без закрепления: %v", err)
	}

	// --- урок прошёл: вопрос репетитору, отметка, отзыв ---
	a.Now = func() time.Time { return trial.EndsAt().Add(time.Minute) }
	must(t, a.PromptMarks(ctx))
	if !hasText(e.snd.take("tutor"), "Как прошёл урок") {
		t.Fatal("репетитора не спросили про урок")
	}
	res, err := a.MarkLesson(ctx, e.tutor, trial.ID, "held")
	must(t, err)
	if !strings.Contains(res, "состоялся") {
		t.Error(res)
	}
	if !hasText(e.snd.take("kid"), "Как прошёл пробный урок") {
		t.Fatal("ученика не спросили про пробный")
	}
	if err := a.Enroll(ctx, e.kid, e.kid.ID, e.tutor.ID, e.math); !errors.Is(err, domain.ErrNotEnrolled) {
		t.Errorf("закрепление без отзыва: %v", err)
	}
	fbID, _, err := a.LeaveFeedback(ctx, e.kid, trial.ID, true)
	must(t, err)
	if !hasText(e.snd.take("mom"), "понравился") {
		t.Error("родитель должен видеть отзыв")
	}
	if !hasText(e.snd.take("admin"), "понравился") {
		t.Error("админ должен видеть отзыв")
	}
	if len(e.snd.take("tutor")) != 0 {
		t.Error("репетитор не должен получать отзыв напрямую")
	}
	if kidMsgs := e.snd.take("kid"); len(e.gate.invites) != 1 || !hasText(kidMsgs, "закрытый канал") {
		t.Error("после понравившегося пробного должен открыться канал")
	}
	_, err = a.ForwardFeedback(ctx, e.kid, fbID)
	if !errors.Is(err, domain.ErrNotAllowed) {
		t.Error("пересылать отзыв может только админ")
	}
	_, err = a.ForwardFeedback(ctx, e.admin, fbID)
	must(t, err)
	if !hasText(e.snd.take("tutor"), "понравился пробный") {
		t.Error("админ переслал отзыв репетитору")
	}
	a.Now = time.Now

	// --- закрепление, оплата, постоянный слот ---
	must(t, a.Enroll(ctx, e.kid, e.kid.ID, e.tutor.ID, e.math))
	products, _ := e.s.Products(ctx, true)
	var pack domain.Product
	for _, p := range products {
		if p.Kind == domain.ProductPack {
			pack = p
		}
	}
	out, err := a.Purchase(ctx, e.mom, e.kid.ID, e.tutor.ID, pack.ID)
	must(t, err)
	if bal, _ := e.s.Balance(ctx, e.kid.ID, e.tutor.ID); bal != 8 {
		t.Fatalf("баланс после пакета %d (%s)", bal, out)
	}
	// изменение цены не трогает купленное
	must(t, e.s.SetBasePrice(ctx, 300000))
	if bal, _ := e.s.Balance(ctx, e.kid.ID, e.tutor.ID); bal != 8 {
		t.Error("баланс не должен зависеть от новой цены")
	}

	slots, _, err = a.FreeSlots(ctx, e.tutor.ID, e.kid.ID, 0)
	must(t, err)
	recStart := slots[len(slots)-1].Add(-7 * 24 * time.Hour) // повтор в пределах горизонта
	for recStart.Before(realNow.Add(13 * time.Hour)) {
		recStart = recStart.Add(7 * 24 * time.Hour)
	}
	_, created, err := a.BookRecurring(ctx, e.mom, e.kid.ID, e.tutor.ID, e.math, recStart)
	must(t, err)
	if created < 3 {
		t.Fatalf("постоянный слот создал %d уроков", created)
	}
	up, _ := e.s.UpcomingForStudent(ctx, e.kid.ID, 10)
	if len(up) != created {
		t.Fatalf("будущих уроков %d, создано %d", len(up), created)
	}
	far := up[len(up)-1]

	// --- перенос учеником требует подтверждения родителя ---
	newSlots, _, err := a.FreeSlots(ctx, e.tutor.ID, e.kid.ID, far.ID)
	must(t, err)
	var target time.Time
	for _, s := range newSlots {
		if s.After(realNow.Add(24 * time.Hour)) {
			target = s
			break
		}
	}
	oc, err := a.RequestChange(ctx, e.kid, far.ID, core.ChangeReschedule, target, "")
	must(t, err)
	if oc != core.PendingApproval {
		t.Fatalf("ожидалось подтверждение родителя, получено %v", oc)
	}
	momMsgs := e.snd.take("mom")
	if len(momMsgs) == 0 || len(momMsgs[0].Buttons) == 0 {
		t.Fatal("родитель должен получить кнопки подтверждения")
	}
	reqID := msg.Parse(momMsgs[0].Buttons[0][0].Action).Int(0)
	if _, err := a.DecideRequest(ctx, e.kid, reqID, true); !errors.Is(err, domain.ErrNotAllowed) {
		t.Error("ученик не может одобрить сам себя")
	}
	_, err = a.DecideRequest(ctx, e.mom, reqID, true)
	must(t, err)
	moved, _ := e.s.Lesson(ctx, far.ID)
	if !moved.StartsAt.Equal(target) {
		t.Errorf("урок не перенесён: %v вместо %v", moved.StartsAt, target)
	}
	if again, _ := a.DecideRequest(ctx, e.mom, reqID, false); !strings.Contains(again, "уже решён") {
		t.Error("повторное решение должно игнорироваться")
	}

	// родитель меняет сам, без подтверждения
	oc, err = a.RequestChange(ctx, e.mom, moved.ID, core.ChangeCancel, time.Time{}, "")
	must(t, err)
	if oc != core.Applied {
		t.Errorf("родитель отменяет сразу: %v", oc)
	}

	// свободный режим: ученик отменяет сам
	st, _ := e.s.Settings(ctx, e.kid.ID)
	st.RescheduleNeedsParent = false
	must(t, e.s.SaveSettings(ctx, e.kid.ID, st))
	next := up[0]
	// поздняя отмена: «сейчас» за 2 часа до урока
	a.Now = func() time.Time { return next.StartsAt.Add(-2 * time.Hour) }
	oc, err = a.RequestChange(ctx, e.kid, next.ID, core.ChangeCancel, time.Time{}, "")
	must(t, err)
	if oc != core.NeedReason {
		t.Fatalf("поздняя отмена без причины: %v", oc)
	}
	oc, err = a.RequestChange(ctx, e.kid, next.ID, core.ChangeCancel, time.Time{}, "заболел")
	must(t, err)
	if oc != core.Applied {
		t.Fatalf("поздняя отмена с причиной: %v", oc)
	}
	late, _ := e.s.Lesson(ctx, next.ID)
	if !late.LateCancel || late.Status != domain.StatusCancelled {
		t.Errorf("поздняя отмена должна быть помечена: %+v", late)
	}
	if !hasText(e.snd.take("tutor"), "Позже чем за 12 часов") {
		t.Error("репетитор должен видеть пометку поздней отмены")
	}

	// --- напоминания: утро в день урока, без дублей ---
	l2 := up[1]
	loc := domain.LoadLocation("")
	ls := l2.StartsAt.In(loc)
	morning := time.Date(ls.Year(), ls.Month(), ls.Day(), core.MorningHour, 5, 0, 0, loc)
	if morning.After(l2.StartsAt) {
		morning = l2.StartsAt.Add(-30 * time.Minute)
	}
	a.Now = func() time.Time { return morning }
	e.snd.take("kid")
	e.snd.take("tutor")
	e.snd.take("mom")
	must(t, a.SendReminders(ctx))
	must(t, a.SendReminders(ctx))
	if n := len(e.snd.take("tutor")); n < 1 || n > 2 {
		t.Errorf("репетитору напоминаний: %d", n)
	}
	if !hasText(e.snd.take("mom"), "Сегодня урок") {
		t.Error("родитель получает утреннее напоминание")
	}

	// --- урок не отметили: через сутки считается состоявшимся и списывается ---
	a.Now = func() time.Time { return l2.EndsAt().Add(25 * time.Hour) }
	must(t, a.AutoHold(ctx))
	if bal, _ := e.s.Balance(ctx, e.kid.ID, e.tutor.ID); bal != 7 {
		t.Errorf("после автоотметки баланс %d, ожидалось 7", bal)
	}
	held, _ := e.s.Lesson(ctx, l2.ID)
	if held.Status != domain.StatusHeld {
		t.Errorf("статус %s", held.Status)
	}
}

func TestAccessRevokedAfterInactivity(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.app
	slots, _, err := a.FreeSlots(ctx, e.tutor.ID, e.kid2.ID, 0)
	must(t, err)
	trial, err := a.BookTrial(ctx, e.kid2, e.kid2.ID, e.tutor.ID, e.math, slots[0])
	must(t, err)
	a.Now = func() time.Time { return trial.EndsAt().Add(time.Minute) }
	_, err = a.MarkLesson(ctx, e.tutor, trial.ID, "held")
	must(t, err)
	_, _, err = a.LeaveFeedback(ctx, e.kid2, trial.ID, true)
	must(t, err)
	e.snd.take("kid2")
	if len(e.gate.invites) != 1 {
		t.Fatal("доступ не выдан")
	}
	a.Now = func() time.Time { return trial.EndsAt().Add(45 * 24 * time.Hour) }
	must(t, a.SyncAllAccess(ctx))
	must(t, a.SyncAllAccess(ctx)) // повторный проход не должен исключать дважды
	e.snd.take("kid2")
	if len(e.gate.kicked) != 1 || e.gate.kicked[0] != "kid2" {
		t.Errorf("ученик без занятий должен быть исключён: %v", e.gate.kicked)
	}
}

func TestDislikedTrialGivesNoAccess(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.app
	slots, _, _ := a.FreeSlots(ctx, e.tutor2.ID, e.kid2.ID, 0)
	trial, err := a.BookTrial(ctx, e.kid2, e.kid2.ID, e.tutor2.ID, e.math, slots[0])
	must(t, err)
	a.Now = func() time.Time { return trial.EndsAt().Add(time.Minute) }
	_, err = a.MarkLesson(ctx, e.tutor2, trial.ID, "held")
	must(t, err)
	_, _, err = a.LeaveFeedback(ctx, e.kid2, trial.ID, false)
	must(t, err)
	e.snd.take("kid2")
	if len(e.gate.invites) != 0 {
		t.Error("непонравившийся пробный не даёт доступ")
	}
	if err := a.Enroll(ctx, e.kid2, e.kid2.ID, e.tutor2.ID, e.math); !errors.Is(err, domain.ErrNotEnrolled) {
		t.Error("нельзя закрепиться за непонравившимся репетитором")
	}
}

func TestManagedChildMessagesGoToParent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	childID, err := e.s.CreateManagedChild(ctx, e.mom.ID, "Маша", 6)
	must(t, err)
	e.app.Notify(ctx, childID, msg.Text("привет"))
	got := e.snd.take("mom")
	if len(got) != 1 || !strings.Contains(got[0].Text, "Маша") {
		t.Errorf("сообщение ребёнку без мессенджера должно прийти родителю с именем: %+v", got)
	}
}

// Параллельная запись в одно окно: advisory-блокировка репетитора пропускает ровно одну.
func TestConcurrentBookingSameSlot(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	slots, _, err := e.app.FreeSlots(ctx, e.tutor.ID, e.kid.ID, 0)
	must(t, err)
	start := slots[3]
	const n = 10
	var students []domain.User
	for i := 0; i < n; i++ {
		u, err := e.s.CreateUser(ctx, store.Identity{Provider: "test", ExternalID: "racer" + string(rune('a'+i)), ChatID: "x"}, "racer", domain.RoleStudent)
		must(t, err)
		students = append(students, u)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, taken := 0, 0
	for _, st := range students {
		wg.Add(1)
		go func(st domain.User) {
			defer wg.Done()
			_, err := e.app.BookTrial(ctx, st, st.ID, e.tutor.ID, e.math, start)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrSlotTaken):
				taken++
			default:
				t.Errorf("неожиданная ошибка: %v", err)
			}
		}(st)
	}
	wg.Wait()
	if ok != 1 || taken != n-1 {
		t.Fatalf("успешных записей %d, отказов %d", ok, taken)
	}
}

// Два воркера одновременно: репетитора спрашивают про урок один раз, доступ в канал выдаётся один раз.
func TestTwoWorkersNoDuplicates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	slots, _, err := e.app.FreeSlots(ctx, e.tutor.ID, e.kid.ID, 0)
	must(t, err)
	trial, err := e.app.BookTrial(ctx, e.kid, e.kid.ID, e.tutor.ID, e.math, slots[0])
	must(t, err)
	e.snd.take("tutor")
	e.snd.take("mom")

	later := func() time.Time { return trial.EndsAt().Add(time.Minute) }
	w1 := core.New(e.s, core.Options{ChannelEnabled: true})
	w2 := core.New(e.s, core.Options{ChannelEnabled: true})
	w1.Now, w2.Now = later, later
	var wg sync.WaitGroup
	for _, w := range []*core.App{w1, w2, w1, w2} {
		wg.Add(1)
		go func(w *core.App) { defer wg.Done(); w.Tick(ctx) }(w)
	}
	wg.Wait()
	prompts := 0
	for _, m := range e.snd.take("tutor") {
		if strings.Contains(m.Text, "Как прошёл урок") {
			prompts++
		}
	}
	if prompts != 1 {
		t.Fatalf("репетитора спросили %d раз", prompts)
	}

	e.app.Now = later
	_, err = e.app.MarkLesson(ctx, e.tutor, trial.ID, "held")
	must(t, err)
	_, _, err = e.app.LeaveFeedback(ctx, e.kid, trial.ID, true)
	must(t, err)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); must(t, w1.SyncAllAccess(ctx)) }()
	}
	wg.Wait()
	e.snd.take("kid")
	if len(e.gate.invites) != 1 {
		t.Errorf("ссылок в канал выдано %d, нужна одна", len(e.gate.invites))
	}
}

// Уведомление уходит в мессенджер, где пользователь был последним.
func TestNotifyPrefersLastSeenMessenger(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.s.CreateUser(ctx, store.Identity{Provider: "vk", ExternalID: "kid-vk", ChatID: "kid-vk"}, "дубль", domain.RoleStudent)
	must(t, err)
	// привязываем VK-аккаунт к тому же ученику и «заходим» из VK позже
	_, err = e.s.DebugExec(ctx, `UPDATE identities SET user_id=$1, last_seen_at=now()+interval '1 minute' WHERE provider='vk'`, e.kid.ID)
	must(t, err)
	e.app.Notify(ctx, e.kid.ID, msg.Text("куда?"))
	if got := e.snd.take("kid"); len(got) != 0 {
		t.Error("сообщение не должно уйти в старый мессенджер")
	}
	if n, _ := e.s.PendingOutbox(ctx); n != 1 {
		t.Errorf("сообщение должно ждать VK-адаптер, в очереди %d", n)
	}
}
