package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Venamuss/FinBot/internal/config"
	"github.com/Venamuss/FinBot/internal/conversation"
	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/httpapi"
	"github.com/Venamuss/FinBot/internal/llm"
	"github.com/Venamuss/FinBot/internal/postgres"
	"github.com/Venamuss/FinBot/internal/telegram"
	"github.com/Venamuss/FinBot/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		// Only fixed stage errors reach logs: driver errors may contain credentials or data.
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancelStartup()
	pool, err := openDatabase(startupCtx, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	listener, err := net.Listen("tcp", configuration.HTTPAddr)
	if err != nil {
		return errors.New("HTTP listen failed")
	}

	repository := postgres.New(pool)
	service := expense.NewService(repository)
	server := &http.Server{
		Handler:           httpapi.New(service),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	botCtx, cancelBot := context.WithCancel(signalCtx)
	botStopped := startBot(botCtx, configuration, pool)
	defer func() {
		cancelBot()
		<-botStopped
	}()
	return serveHTTP(signalCtx, server, listener)
}

func openDatabase(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	if err := waitForDatabase(ctx, pool); err != nil {
		pool.Close()
		return nil, errors.New("database connection failed")
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		pool.Close()
		return nil, errors.New("database migration failed")
	}
	return pool, nil
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	slog.Info("HTTP server started")

	var serveErr error
	select {
	case serveErr = <-serveErrors:
	case <-ctx.Done():
		if err := shutdownHTTP(server); err != nil {
			return err
		}
		serveErr = <-serveErrors
	}
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return errors.New("HTTP server failed")
	}
	slog.Info("HTTP server stopped")
	return nil
}

func shutdownHTTP(server *http.Server) error {
	// Shutdown must remain usable after the signal context has been canceled.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := server.Shutdown(shutdownCtx); err != nil {
		// Shutdown's failure takes precedence over a secondary forced-close error.
		_ = server.Close()
		return errors.New("HTTP shutdown timed out")
	}
	return nil
}

func waitForDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error

	for {
		pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)

		err := pool.Ping(pingCtx)
		pingCancel()

		if err == nil {
			return nil
		}

		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"database not ready: %w (last ping error: %v)",
				ctx.Err(),
				lastErr,
			)
		case <-ticker.C:
		}
	}
}

func startBot(ctx context.Context, configuration config.Config, pool *pgxpool.Pool) <-chan struct{} {
	stopped := make(chan struct{})
	if configuration.TelegramToken == "" {
		close(stopped)
		return stopped
	}

	model, err := llm.New(configuration.LLM)
	if err != nil {
		// Load already validates these settings. Keep the REST service available.
		slog.Error("Telegram model initialization failed")
		close(stopped)
		return stopped
	}

	store := postgres.NewTelegramStore(pool)
	orchestrator := conversation.New(model, store, configuration.TelegramAllowedUserIDs, configuration.TimeZone)
	bot := telegram.NewBot(telegram.New(configuration.TelegramToken), configuration.TelegramAllowedUserIDs, orchestrator.Handle)
	go func() {
		defer close(stopped)
		slog.Info("Telegram polling started")
		if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("Telegram polling stopped unexpectedly")
		}
		slog.Info("Telegram polling stopped")
	}()
	return stopped
}
