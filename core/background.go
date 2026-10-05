package core

import (
	"context"
	"fmt"
	"time"

	"github.com/SMbyM/tutoring-bot/actions"
	"github.com/SMbyM/tutoring-bot/domain"
	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/store"
)

// SyncAccess приводит доступ ученика в канал к правилу domain.AccessEligible.
// Выдача и исключение идемпотентны: параллельные процессы не пришлют две ссылки.
// Саму ссылку создаёт адаптер мессенджера при обработке записи outbox.
func (a *App) SyncAccess(ctx context.Context, studentID int64) error {
	if !a.Opt.ChannelEnabled {
		return nil
	}
	facts, active, err := a.S.AccessFacts(ctx, studentID, a.Now())
	if err != nil {
		return err
	}
	eligible := domain.AccessEligible(facts, a.Now(), a.Opt.AccessInactivity)
	switch {
	case eligible && !active:
		ok, err := a.S.GrantAccess(ctx, studentID)
		if err != nil || !ok {
			return err
		}
		st, err := a.S.UserByID(ctx, studentID)
		if err != nil {
			return err
		}
		a.enqueue(ctx, studentID, store.OutChannelInvite, msg.Text(fmt.Sprintf(
			"🔓 %s, вам открыт закрытый канал с материалами для подготовки. Ссылка одноразовая и действует сутки.", st.Name)))
	case !eligible && active:
		ok, err := a.S.RevokeAccess(ctx, studentID)
		if err != nil || !ok {
			return err
		}
		ids, err := a.S.Identities(ctx, studentID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := a.S.Enqueue(ctx, store.OutboxItem{Provider: id.Provider, Kind: store.OutChannelKick, ExternalID: id.ExternalID}); err != nil {
				return err
			}
		}
		a.Notify(ctx, studentID, msg.Text("Доступ к закрытому каналу приостановлен: давно не было занятий. Запишитесь на урок — доступ вернётся автоматически."))
	}
	return nil
}

func (a *App) SyncAllAccess(ctx context.Context) error {
	ids, err := a.S.AccessCandidates(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := a.SyncAccess(ctx, id); err != nil {
			a.Log.Warn("синхронизация доступа", "user", id, "err", err)
		}
	}
	return nil
}

// PromptMarks — после окончания урока спрашиваем репетитора, состоялся ли он.
func (a *App) PromptMarks(ctx context.Context) error {
	ls, err := a.S.LessonsToPrompt(ctx, a.Now())
	if err != nil {
		return err
	}
	for _, l := range ls {
		ok, err := a.S.ClaimPrompt(ctx, l.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue // уже спросил другой процесс
		}
		t, err := a.S.Tutor(ctx, l.TutorID)
		if err != nil {
			continue
		}
		a.Notify(ctx, l.TutorID, msg.Message{
			Text: "Как прошёл урок?\n" + LessonLine(l, t.Location(), false, true) + "\n(без ответа через 24 часа урок будет считаться состоявшимся)",
			Buttons: [][]msg.Button{
				msg.Row(msg.Btn("✅ Состоялся", actions.MarkLesson{LessonID: l.ID, Mark: actions.MarkHeld})),
				msg.Row(msg.Btn("🙈 Ученик не пришёл", actions.MarkLesson{LessonID: l.ID, Mark: actions.MarkNoShow}), msg.Btn("🔄 Перенесли", actions.MarkLesson{LessonID: l.ID, Mark: actions.MarkMoved})),
			}})
	}
	return nil
}

// AutoHold — неотмеченные уроки через сутки считаются состоявшимися.
func (a *App) AutoHold(ctx context.Context) error {
	ls, err := a.S.LessonsToAutoHold(ctx, a.Now())
	if err != nil {
		return err
	}
	for _, l := range ls {
		ok, err := a.S.SetStatus(ctx, l.ID, domain.StatusHeld, true)
		if err != nil {
			return err
		}
		if ok {
			l.Status = domain.StatusHeld
			a.onHeld(ctx, l)
		}
	}
	return nil
}

// MorningHour — во сколько по местному времени получателя приходит утреннее напоминание.
const MorningHour = 8

// SendReminders — утром в день урока (всем) и за час (ученику и репетитору), с учётом настроек и поясов.
func (a *App) SendReminders(ctx context.Context) error {
	now := a.Now()
	ls, err := a.S.LessonsStartingBetween(ctx, now, now.Add(36*time.Hour))
	if err != nil {
		return err
	}
	for _, l := range ls {
		t, err := a.S.Tutor(ctx, l.TutorID)
		if err != nil {
			continue
		}
		parents := a.parentIDs(ctx, l.StudentID)
		// утреннее: ученик, родители, репетитор
		for _, uid := range append([]int64{l.StudentID, l.TutorID}, parents...) {
			a.maybeRemind(ctx, l, t.Location(), uid, "morning", now)
		}
		// за час: ученик и репетитор (ребёнку без мессенджера — через родителя)
		if l.StartsAt.Sub(now) <= time.Hour {
			for _, uid := range []int64{l.StudentID, l.TutorID} {
				a.maybeRemind(ctx, l, t.Location(), uid, "hour", now)
			}
		}
	}
	return nil
}

func (a *App) maybeRemind(ctx context.Context, l domain.Lesson, lessonLoc *time.Location, uid int64, kind string, now time.Time) {
	u, err := a.S.UserByID(ctx, uid)
	if err != nil {
		return
	}
	st, err := a.S.Settings(ctx, uid)
	if err != nil {
		return
	}
	loc := u.Location()
	switch kind {
	case "morning":
		if u.ManagedBy != 0 {
			return // родитель ребёнка без мессенджера получает своё утреннее напоминание
		}
		if !st.RemindMorning {
			return
		}
		ln, ll := now.In(loc), l.StartsAt.In(loc)
		sameDay := ln.Year() == ll.Year() && ln.YearDay() == ll.YearDay()
		if !sameDay || ln.Hour() < MorningHour {
			return
		}
	case "hour":
		if !st.RemindHour {
			return
		}
	}
	first, err := a.S.TryLogReminder(ctx, l.ID, uid, kind)
	if err != nil || !first {
		return
	}
	var text string
	withTutor, withStudent := uid != l.TutorID, uid != l.StudentID
	if kind == "morning" {
		text = "☀️ Сегодня урок: " + LessonLine(l, loc, withTutor, withStudent)
	} else {
		text = "⏰ Через час урок: " + LessonLine(l, loc, withTutor, withStudent)
	}
	a.Notify(ctx, uid, msg.Text(text))
}

// Tick — один проход всех фоновых задач.
func (a *App) Tick(ctx context.Context) {
	for name, job := range map[string]func(context.Context) error{
		"reminders": a.SendReminders,
		"prompt":    a.PromptMarks,
		"autohold":  a.AutoHold,
	} {
		if err := job(ctx); err != nil {
			a.Log.Error("фоновая задача", "job", name, "err", err)
		}
	}
}

// DailyTick — задачи раз в несколько часов: продление слотов и синхронизация доступа.
func (a *App) DailyTick(ctx context.Context) {
	if err := a.ExtendRecurring(ctx); err != nil {
		a.Log.Error("продление слотов", "err", err)
	}
	if err := a.SyncAllAccess(ctx); err != nil {
		a.Log.Error("доступ в канал", "err", err)
	}
	if err := a.S.PurgeOutbox(ctx, 14*24*time.Hour); err != nil {
		a.Log.Error("очистка outbox", "err", err)
	}
}
