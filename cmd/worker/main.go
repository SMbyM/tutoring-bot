// Фоновый воркер: напоминания, вопросы репетитору после урока, автоотметка,
// продление постоянных слотов, доступ в канал. Не зависит от мессенджеров:
// всё исходящее пишет в outbox, доставляют адаптеры (cmd/tg, позже VK и сайт).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/internal/config"
	"github.com/SMbyM/tutoring-bot/store"
	"github.com/SMbyM/tutoring-bot/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("svc", "worker"))
	if err := run(); err != nil {
		slog.Error("остановка", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		return err
	}
	slog.Info("воркер запущен", "env", cfg.Env, "interval", cfg.WorkerInterval.String())
	worker.Run(ctx, core.New(s, cfg.CoreOptions()), cfg.WorkerInterval)
	return nil
}
