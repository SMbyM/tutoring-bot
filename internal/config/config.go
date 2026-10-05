// Package config читает настройки из переменных окружения.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env              string // debug | production
	BotToken         string
	TelegramAPIURL   string // свой адрес Bot API (например, через туннель); пусто — api.telegram.org
	DatabaseURL      string
	HTTPAddr         string
	MiniAppURL       string // публичный HTTPS-адрес Mini App; пусто — Mini App не показывается
	ChannelID        int64  // закрытый канал с материалами; 0 — функция выключена
	AdminTelegramIDs map[string]bool
	PolicyURL        string
	AccessInactivity time.Duration
	BookingHorizon   time.Duration // на сколько вперёд показывать свободные окна
	RecurringAhead   time.Duration // на сколько вперёд создаются уроки постоянного слота
	WorkerInterval   time.Duration
}

func (c Config) Debug() bool { return c.Env != "production" }

func Load() (Config, error) {
	c := Config{
		Env:              getenv("APP_ENV", "debug"),
		BotToken:         os.Getenv("BOT_TOKEN"),
		TelegramAPIURL:   os.Getenv("TELEGRAM_API_URL"),
		DatabaseURL:      getenv("DATABASE_URL", "postgres://bot:bot@localhost:5432/bot?sslmode=disable"),
		HTTPAddr:         getenv("HTTP_ADDR", ":8080"),
		MiniAppURL:       strings.TrimRight(os.Getenv("MINIAPP_URL"), "/"),
		PolicyURL:        os.Getenv("POLICY_URL"),
		AdminTelegramIDs: map[string]bool{},
		AccessInactivity: days(getenv("ACCESS_INACTIVITY_DAYS", "30")),
		BookingHorizon:   days(getenv("BOOKING_HORIZON_DAYS", "14")),
		RecurringAhead:   days(getenv("RECURRING_AHEAD_DAYS", "28")),
		WorkerInterval:   time.Minute,
	}
	if c.Env != "debug" && c.Env != "production" {
		return c, fmt.Errorf("APP_ENV должен быть debug или production, а не %q", c.Env)
	}
	if c.BotToken == "" {
		return c, fmt.Errorf("не задан BOT_TOKEN")
	}
	if v := os.Getenv("CHANNEL_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("CHANNEL_ID: %w", err)
		}
		c.ChannelID = id
	}
	for _, id := range strings.Split(os.Getenv("ADMIN_TG_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			c.AdminTelegramIDs[id] = true
		}
	}
	if v := os.Getenv("WORKER_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("WORKER_INTERVAL: %w", err)
		}
		c.WorkerInterval = d
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func days(s string) time.Duration {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		n = 30
	}
	return time.Duration(n) * 24 * time.Hour
}
