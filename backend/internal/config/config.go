package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DatabaseURL            string
	HTTPAddr               string
	LLM                    llm.Config
	TelegramToken          string
	TelegramAllowedUserIDs map[int64]bool
	TimeZone               *time.Location
	LogLevel               slog.Level
	Telegram               telegram.Config
	TelegramPollRetryDelay time.Duration
	HTTPRequestTimeout     time.Duration
	StartupTimeout         time.Duration
}

func Load() (Config, error) {
	configuration := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		HTTPAddr:    os.Getenv("HTTP_ADDR"),
	}

	if strings.TrimSpace(configuration.DatabaseURL) == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if _, err := pgxpool.ParseConfig(configuration.DatabaseURL); err != nil {
		return Config{}, errors.New("DATABASE_URL must be a valid PostgreSQL connection string")
	}
	if configuration.HTTPAddr == "" {
		configuration.HTTPAddr = "127.0.0.1:8080"
	}

	_, port, err := net.SplitHostPort(configuration.HTTPAddr)
	if err != nil {
		return Config{}, errors.New("HTTP_ADDR must be host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || strings.Trim(port, "0123456789") != "" || portNumber < 1 || portNumber > 65535 {
		return Config{}, errors.New("HTTP_ADDR must contain a numeric port from 1 to 65535")
	}

	switch strings.ToUpper(os.Getenv("LOG_LEVEL")) {
	case "", "INFO":
		configuration.LogLevel = slog.LevelInfo
	case "DEBUG":
		configuration.LogLevel = slog.LevelDebug
	case "WARN":
		configuration.LogLevel = slog.LevelWarn
	case "ERROR":
		configuration.LogLevel = slog.LevelError
	default:
		return Config{}, errors.New("LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
	}
	configuration.LLM = llm.Config{
		BaseURL: os.Getenv("LLM_BASE_URL"),
		Model:   os.Getenv("LLM_MODEL"),
		APIKey:  os.Getenv("LLM_API_KEY"),
	}
	if configuration.LLM.BaseURL == "" {
		configuration.LLM.BaseURL = "https://openrouter.ai/api/v1"
	}
	if configuration.LLM.Model == "" {
		configuration.LLM.Model = "google/gemma-4-31b-it:free"
	}
	durationSettings := []struct {
		name        string
		destination *time.Duration
		fallback    time.Duration
	}{
		{"LLM_TIMEOUT", &configuration.LLM.Timeout, 30 * time.Second},
		{"HTTP_REQUEST_TIMEOUT", &configuration.HTTPRequestTimeout, 5 * time.Second},
		{"STARTUP_TIMEOUT", &configuration.StartupTimeout, 30 * time.Second},
		{"TELEGRAM_HTTP_TIMEOUT", &configuration.Telegram.Timeout, 40 * time.Second},
		{"TELEGRAM_POLL_TIMEOUT", &configuration.Telegram.PollTimeout, 25 * time.Second},
		{"TELEGRAM_RETRY_DELAY", &configuration.Telegram.RetryDelay, time.Second},
		{"TELEGRAM_POLL_RETRY_DELAY", &configuration.TelegramPollRetryDelay, 5 * time.Second},
	}
	for _, setting := range durationSettings {
		value, err := positiveDuration(setting.name, setting.fallback)
		if err != nil {
			return Config{}, err
		}
		*setting.destination = value
	}
	if configuration.Telegram.PollTimeout%time.Second != 0 || configuration.Telegram.PollTimeout > 50*time.Second {
		return Config{}, errors.New("TELEGRAM_POLL_TIMEOUT must be whole seconds from 1s to 50s")
	}
	// Two retries use delays of one and two times RetryDelay.
	if configuration.Telegram.RetryDelay > time.Duration(1<<63-1)/2 {
		return Config{}, errors.New("TELEGRAM_RETRY_DELAY is too large for exponential backoff")
	}
	if configuration.Telegram.Timeout <= configuration.Telegram.PollTimeout {
		return Config{}, errors.New("TELEGRAM_HTTP_TIMEOUT must exceed TELEGRAM_POLL_TIMEOUT")
	}
	// Preserve the fixed HTTP write deadline and leave time to encode the response.
	if configuration.HTTPRequestTimeout >= 15*time.Second {
		return Config{}, errors.New("HTTP_REQUEST_TIMEOUT must be less than the 15s HTTP write timeout")
	}
	if err := configuration.LLM.Validate(); err != nil {
		return Config{}, err
	}
	configuration.TelegramToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if strings.ContainsAny(configuration.TelegramToken, " \t\r\n/?#") {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN has invalid characters")
	}
	configuration.TelegramAllowedUserIDs, err = parseAllowlist(os.Getenv("TELEGRAM_ALLOWED_USER_IDS"))
	if err != nil {
		return Config{}, err
	}
	zone := os.Getenv("TIME_ZONE")
	if zone == "" {
		zone = "Europe/Moscow"
	}
	configuration.TimeZone, err = time.LoadLocation(zone)
	if err != nil {
		return Config{}, errors.New("TIME_ZONE must be a valid IANA time zone")
	}
	return configuration, nil
}

func parseAllowlist(value string) (map[int64]bool, error) {
	allowed := make(map[int64]bool)
	if strings.TrimSpace(value) == "" {
		return allowed, nil
	}
	for _, entry := range strings.Split(value, ",") {
		id, err := expense.ParseID(strings.TrimSpace(entry))
		if err != nil {
			return nil, errors.New("TELEGRAM_ALLOWED_USER_IDS must be comma-separated positive decimal IDs")
		}
		allowed[id] = true
	}
	return allowed, nil
}

func positiveDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return duration, nil
}
