// Бот онлайн-школы репетиторов: запись, расписание, напоминания, родители, закрытый канал.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // часовые пояса внутри бинарника: не зависим от образа

	"github.com/SMbyM/tutoring-bot/internal/config"
	"github.com/SMbyM/tutoring-bot/internal/core"
	"github.com/SMbyM/tutoring-bot/internal/store"
	"github.com/SMbyM/tutoring-bot/internal/transport/miniapp"
	"github.com/SMbyM/tutoring-bot/internal/transport/telegram"
	"github.com/SMbyM/tutoring-bot/internal/ui"
	"github.com/SMbyM/tutoring-bot/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
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

	app := core.New(s, core.Options{
		Debug: cfg.Debug(), AccessInactivity: cfg.AccessInactivity, BookingHorizon: cfg.BookingHorizon,
		RecurringAhead: cfg.RecurringAhead, PolicyURL: cfg.PolicyURL, MiniAppURL: cfg.MiniAppURL,
	})
	eng := ui.NewEngine(app, cfg.AdminTelegramIDs, cfg.MiniAppURL, cfg.PolicyURL)

	tg, err := telegram.New(ctx, cfg.BotToken, cfg.TelegramAPIURL, eng, cfg.ChannelID)
	if err != nil {
		return err
	}
	app.AddSender(tg)
	if cfg.ChannelID != 0 {
		app.Gate = tg.Gate()
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, ReadHeaderTimeout: 10 * time.Second,
		Handler: (&miniapp.Server{S: s, BotToken: cfg.BotToken, Debug: cfg.Debug()}).Handler()}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "err", err)
			stop()
		}
	}()

	go worker.Run(ctx, app, cfg.WorkerInterval)

	slog.Info("бот запущен", "username", tg.Username(), "env", cfg.Env, "http", cfg.HTTPAddr)
	tg.Start(ctx) // блокирует до сигнала остановки

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
