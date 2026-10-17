package config

import (
	"errors"
	"net"
	"os"
	"time"

	"github.com/Venamuss/FinBot/internal/llm"
)

type Config struct {
	DatabaseURL string
	HTTPAddr    string
	LLM         llm.Config
}

func Load() (Config, error) {
	configuration := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		HTTPAddr:    os.Getenv("HTTP_ADDR"),
	}
	if configuration.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if configuration.HTTPAddr == "" {
		configuration.HTTPAddr = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(configuration.HTTPAddr); err != nil {
		return Config{}, errors.New("HTTP_ADDR must be host:port")
	}
	configuration.LLM = llm.Config{
		BaseURL: os.Getenv("LLM_BASE_URL"),
		Model:   os.Getenv("LLM_MODEL"),
		APIKey:  os.Getenv("LLM_API_KEY"),
		Timeout: 30 * time.Second,
	}
	if configuration.LLM.BaseURL == "" {
		configuration.LLM.BaseURL = "https://openrouter.ai/api/v1"
	}
	if configuration.LLM.Model == "" {
		configuration.LLM.Model = "google/gemma-4-31b-it:free"
	}
	if value := os.Getenv("LLM_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, errors.New("LLM_TIMEOUT must be a positive Go duration")
		}
		configuration.LLM.Timeout = timeout
	}
	if err := configuration.LLM.Validate(); err != nil {
		return Config{}, err
	}
	return configuration, nil
}
