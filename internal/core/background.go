package core

import (
	"context"
	"fmt"
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
	"github.com/SMbyM/tutoring-bot/internal/msg"
)

// SyncAccess приводит доступ ученика в канал к правилу domain.AccessEligible:
// выдаёт одноразовую ссылку или исключает из канала.
func (a *App) SyncAccess(ctx context.Context, studentID int64) error {
	if a.Gate == nil {
		return nil
	}
	facts, active, err := a.S.AccessFacts(ctx, studentID, a.Now())
	if err != nil {
		return err
	}
	eligible := domain.AccessEligible(facts, a.Now(), a.Opt.AccessInactivity)
	switch {
	case eligible && !active:
		st, err := a.S.UserByID(ctx, studentID)
		if err != nil {
			return err
		}
		link, err := a.Gate.InviteLink(ctx, fmt.Sprintf("u%d", studentID))
		if err != nil {
			return err
		}
		if err := a.S.GrantAccess(ctx, studentID); err != nil {
			return err
		}
		a.Notify(ctx, studentID, msg.Message{
			Text:    fmt.Sprintf("🔓 %s, вам открыт закрытый канал с материалами для подготовки. Ссылка одноразовая и действует сутки.", st.Name),
			Buttons: [][]msg.Button{msg.Row(msg.Link("Вступить в канал", link))}})
	case !eligible && active:
		if err := a.S.RevokeAccess(ctx, studentID); err != nil {
			return err
		}
		ids, err := a.S.Identities(ctx, studentID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id.Provider == a.Gate.Provider() {
				if err := a.Gate.Kick(ctx, id.ExternalID); err != nil {
					a.Log.Warn("исключение из канала", "user", studentID, "err", err)
				}
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
		if err := a.S.MarkPrompted(ctx, l.ID); err != nil {
			return err
		}
		t, err := a.S.Tutor(ctx, l.TutorID)
		if err != nil {
			continue
		}
		a.Notify(ctx, l.TutorID, msg.Message{
			Text: "Как прошёл урок?\n" + LessonLine(l, t.Location(), false, true) + "\n(без ответа через 24 часа урок будет считаться состоявшимся)",
			Buttons: [][]msg.Button{
				msg.Row(msg.Btn("✅ Состоялся", "mk", l.ID, "held")),
				msg.Row(msg.Btn("🙈 Ученик не пришёл", "mk", l.ID, "noshow"), msg.Btn("🔄 Перенесли", "mk", l.ID, "moved")),
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
}
