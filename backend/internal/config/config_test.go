package config

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func resetEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://synthetic-db")
	for _, name := range []string{
		"TELEGRAM_BOT_TOKEN", "TELEGRAM_ALLOWED_USER_IDS", "TIME_ZONE", "HTTP_ADDR",
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "LLM_TIMEOUT", "LOG_LEVEL",
		"HTTP_REQUEST_TIMEOUT", "STARTUP_TIMEOUT", "TELEGRAM_HTTP_TIMEOUT",
		"TELEGRAM_POLL_TIMEOUT", "TELEGRAM_RETRY_DELAY", "TELEGRAM_POLL_RETRY_DELAY",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	resetEnvironment(t)
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.DatabaseURL != "postgres://synthetic-db" || configuration.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal("existing configuration defaults changed")
	}
	if configuration.LLM.BaseURL != "https://openrouter.ai/api/v1" || configuration.LLM.Model != "google/gemma-4-31b-it:free" {
		t.Fatal("incorrect initial endpoint/model defaults")
	}
	if configuration.LLM.Timeout != 30*time.Second || configuration.LLM.APIKey != "" {
		t.Fatal("incorrect timeout/key defaults")
	}
}

func TestLoadOverrides(t *testing.T) {
	resetEnvironment(t)
	t.Setenv("HTTP_ADDR", ":8090")
	t.Setenv("LLM_BASE_URL", "http://localhost:11434/v1/")
	t.Setenv("LLM_MODEL", "local-model")
	t.Setenv("LLM_API_KEY", "synthetic-key")
	t.Setenv("LLM_TIMEOUT", "1m30s")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.HTTPAddr != ":8090" || configuration.LLM.BaseURL != "http://localhost:11434/v1/" || configuration.LLM.Model != "local-model" {
		t.Fatal("endpoint/model override ignored")
	}
	if configuration.LLM.Timeout != 90*time.Second || configuration.LLM.APIKey != "synthetic-key" {
		t.Fatal("timeout/key override ignored")
	}
}

func TestLoadInvalidSettings(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "DATABASE_URL", value: ""},
		{name: "HTTP_ADDR", value: "invalid"},
		{name: "LLM_BASE_URL", value: "relative/v1"},
		{name: "LLM_BASE_URL", value: "ftp://localhost/v1"},
		{name: "LLM_BASE_URL", value: "https:///v1"},
		{name: "LLM_BASE_URL", value: "http://localhost:bad/v1"},
		{name: "LLM_BASE_URL", value: "https://user:synthetic-key@localhost/v1"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1?key=synthetic-key"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1?"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1#synthetic-key"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1#"},
		{name: "LLM_MODEL", value: " \t"},
		{name: "LLM_API_KEY", value: "synthetic-key\nheader"},
		{name: "LLM_TIMEOUT", value: "synthetic-key"},
		{name: "LLM_TIMEOUT", value: "30"},
		{name: "LLM_TIMEOUT", value: "0s"},
		{name: "LLM_TIMEOUT", value: "-1s"},
		{name: "LLM_TIMEOUT", value: "999999999999h"},
	}
	for _, test := range tests {
		t.Run(test.name+"/"+test.value, func(t *testing.T) {
			resetEnvironment(t)
			t.Setenv(test.name, test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("want safe setting error for %s, got %v", test.name, err)
			}
			if strings.Contains(err.Error(), "synthetic-key") {
				t.Fatal("configuration error leaked input")
			}
		})
	}
}

func TestTelegramConfiguration(t *testing.T) {
	for _, value := range []string{"", "   ", "101, 202,101", "9223372036854775807"} {
		t.Run(value, func(t *testing.T) {
			resetEnvironment(t)
			t.Setenv("TELEGRAM_ALLOWED_USER_IDS", value)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TimeZone.String() != "Europe/Moscow" {
				t.Fatal("wrong default time zone")
			}
			if value == "101, 202,101" && (len(cfg.TelegramAllowedUserIDs) != 2 || !cfg.TelegramAllowedUserIDs[101] || !cfg.TelegramAllowedUserIDs[202]) {
				t.Fatal("allowlist normalization failed")
			}
			if strings.TrimSpace(value) == "" && len(cfg.TelegramAllowedUserIDs) != 0 {
				t.Fatal("empty allowlist grants access")
			}
		})
	}
	for _, value := range []string{"0", "01", "-1", "+1", "101,,202", "101,", "abc", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			resetEnvironment(t)
			t.Setenv("TELEGRAM_ALLOWED_USER_IDS", value)
			if _, err := Load(); err == nil {
				t.Fatal("malformed allowlist accepted")
			}
		})
	}
	resetEnvironment(t)
	t.Setenv("TIME_ZONE", "invalid/timezone")
	if _, err := Load(); err == nil {
		t.Fatal("invalid time zone accepted")
	}
}

func TestOperationalDefaultsAndOverrides(t *testing.T) {
	resetEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.HTTPRequestTimeout != 5*time.Second || cfg.StartupTimeout != 30*time.Second {
		t.Fatal("incorrect HTTP/startup/log defaults")
	}
	if cfg.Telegram.Timeout != 40*time.Second || cfg.Telegram.PollTimeout != 25*time.Second || cfg.Telegram.RetryDelay != time.Second || cfg.TelegramPollRetryDelay != 5*time.Second {
		t.Fatal("incorrect operational defaults")
	}
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HTTP_REQUEST_TIMEOUT", "2s")
	t.Setenv("STARTUP_TIMEOUT", "1m")
	t.Setenv("TELEGRAM_HTTP_TIMEOUT", "15s")
	t.Setenv("TELEGRAM_POLL_TIMEOUT", "10s")
	t.Setenv("TELEGRAM_RETRY_DELAY", "500ms")
	t.Setenv("TELEGRAM_POLL_RETRY_DELAY", "2s")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.HTTPRequestTimeout != 2*time.Second || cfg.StartupTimeout != time.Minute {
		t.Fatal("HTTP/startup/log overrides ignored")
	}
	if cfg.Telegram.Timeout != 15*time.Second || cfg.Telegram.PollTimeout != 10*time.Second || cfg.Telegram.RetryDelay != 500*time.Millisecond || cfg.TelegramPollRetryDelay != 2*time.Second {
		t.Fatal("operational overrides ignored")
	}
}

func TestInvalidOperationalConfiguration(t *testing.T) {
	cases := []struct{ name, value string }{
		{"DATABASE_URL", "   "},
		{"DATABASE_URL", "postgres://user:private-password@host:bad/db"},
		{"DATABASE_URL", "postgres://host/db?pool_max_conns=private-value"},
		{"HTTP_ADDR", ":0"},
		{"HTTP_ADDR", ":+8080"},
		{"TELEGRAM_RETRY_DELAY", "2000000h"},
		{"HTTP_ADDR", ":65536"},
		{"HTTP_ADDR", ":private-port"},
		{"LOG_LEVEL", "private-level"},
		{"LOG_LEVEL", "INFO+1"},
		{"HTTP_REQUEST_TIMEOUT", "15s"},
		{"TELEGRAM_POLL_TIMEOUT", "1ms"},
		{"TELEGRAM_POLL_TIMEOUT", "51s"},
		{"TELEGRAM_HTTP_TIMEOUT", "25s"},
		{"TELEGRAM_BOT_TOKEN", "private-token/secret"},
	}
	for _, name := range []string{"HTTP_REQUEST_TIMEOUT", "STARTUP_TIMEOUT", "TELEGRAM_HTTP_TIMEOUT", "TELEGRAM_POLL_TIMEOUT", "TELEGRAM_RETRY_DELAY", "TELEGRAM_POLL_RETRY_DELAY"} {
		for _, value := range []string{"private-duration", "0s", "-1s", "999999999999h"} {
			cases = append(cases, struct{ name, value string }{name, value})
		}
	}
	for _, test := range cases {
		t.Run(test.name+"/"+test.value, func(t *testing.T) {
			resetEnvironment(t)
			t.Setenv(test.name, test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.name) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("missing safe variable-specific error: %v", err)
			}
		})
	}
}

func TestLogLevelFiltering(t *testing.T) {
	for _, test := range []struct {
		level   slog.Level
		visible int
	}{{slog.LevelDebug, 4}, {slog.LevelInfo, 3}, {slog.LevelWarn, 2}, {slog.LevelError, 1}} {
		t.Run(test.level.String(), func(t *testing.T) {
			resetEnvironment(t)
			t.Setenv("LOG_LEVEL", test.level.String())
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: cfg.LogLevel}))
			for _, candidate := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
				logger.Log(context.Background(), candidate, "test")
			}
			if got := strings.Count(output.String(), "\n"); got != test.visible {
				t.Fatalf("incorrect number of visible levels: %d", got)
			}
		})
	}
}
