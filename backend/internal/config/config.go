package config

import (
	"errors"
	"net"
	"os"
)

type Config struct {
	DatabaseURL string
	HTTPAddr    string
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
	return configuration, nil
}
