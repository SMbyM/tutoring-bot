// Telegram-адаптер: принимает апдейты, отдаёт Mini App и доставляет сообщения из outbox в Telegram.
// Фоновые задачи (напоминания и т.п.) выполняет отдельный процесс cmd/worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // часовые пояса внутри бинарника: не зависим от образа

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/internal/config"
	"github.com/SMbyM/tutoring-bot/outbox"
	"github.com/SMbyM/tutoring-bot/store"
	"github.com/SMbyM/tutoring-bot/transport/miniapp"
	"github.com/SMbyM/tutoring-bot/transport/telegram"
	"github.com/SMbyM/tutoring-bot/ui"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("svc", "tg"))
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
	if cfg.BotToken == "" {
		return fmt.Errorf("не задан BOT_TOKEN")
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

	logProxy()
	app := core.New(s, cfg.CoreOptions())
	eng := ui.NewEngine(app, cfg.AdminTelegramIDs, cfg.MiniAppURL, cfg.PolicyURL)
	tg, err := telegram.New(ctx, cfg.BotToken, cfg.TelegramAPIURL, eng, cfg.ChannelID)
	if err != nil {
		return err
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, ReadHeaderTimeout: 10 * time.Second,
		Handler: (&miniapp.Server{App: app, BotToken: cfg.BotToken, Log: slog.Default()}).Handler()}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "err", err)
			stop()
		}
	}()
	go outbox.New(s, tg).Run(ctx)

	slog.Info("бот запущен", "username", tg.Username(), "env", cfg.Env, "http", cfg.HTTPAddr)
	tg.Start(ctx) // блокирует до сигнала остановки

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// logProxy пишет в лог, через какой прокси бот пойдёт в Telegram (без логина и пароля).
func logProxy() {
	req, _ := http.NewRequest(http.MethodGet, "https://api.telegram.org", nil)
	p, err := http.ProxyFromEnvironment(req)
	switch {
	case err != nil:
		slog.Warn("прокси указан с ошибкой", "err", err)
	case p == nil:
		slog.Info("Telegram: прямое подключение, прокси не задан")
	default:
		slog.Info("Telegram: через прокси", "proxy", p.Scheme+"://"+p.Host)
	}
}
