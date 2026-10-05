// Package worker — фоновый цикл: напоминания, вопросы репетитору после урока,
// автоотметка, продление постоянных слотов, синхронизация доступа в канал.
package worker

import (
	"context"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
)

// DailyEvery — как часто выполнять «медленные» задачи.
const DailyEvery = 6 * time.Hour

func Run(ctx context.Context, app *core.App, every time.Duration) {
	app.DailyTick(ctx)
	app.Tick(ctx)
	fast := time.NewTicker(every)
	slow := time.NewTicker(DailyEvery)
	defer fast.Stop()
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			app.Tick(ctx)
		case <-slow.C:
			app.DailyTick(ctx)
		}
	}
}
