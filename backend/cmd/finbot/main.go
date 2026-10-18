package main

import (
	"context"
	"errors"
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
	"github.com/Venamuss/FinBot/internal/observability"
	"github.com/Venamuss/FinBot/internal/postgres"
	"github.com/Venamuss/FinBot/internal/telegram"
	"github.com/Venamuss/FinBot/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// startupError carries only safe stage/category metadata across the log boundary.
type startupError struct {
	operation string
	category  string
}

func (err *startupError) Error() string {
	return err.operation + " failed"
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		// Only fixed stage errors reach logs: driver errors may contain credentials or data.
		operation := "configuration"
		category := "configuration"
		var startup *startupError
		if errors.As(err, &startup) {
			operation = startup.operation
			category = startup.category
		}
		slog.Error("Application stopped", "component", "app", "operation", operation, "error_category", category, "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: configuration.LogLevel})))
	slog.Info("Application starting", "component", "app", "operation", "startup")
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(signalCtx, configuration.StartupTimeout)
	defer cancelStartup()
	pool, err := openDatabase(startupCtx, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	listener, err := net.Listen("tcp", configuration.HTTPAddr)
	if err != nil {
		return &startupError{operation: "http_listen", category: observability.Category(err)}
	}

	repository := postgres.New(pool)
	service := expense.NewService(repository)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", readinessHandler(pool.Ping))
	mux.Handle("/", httpapi.New(service, configuration.HTTPRequestTimeout))
	server := &http.Server{
		Handler:           mux,
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
		return nil, &startupError{operation: "database_configuration", category: "configuration"}
	}
	if err := waitForDatabase(ctx, pool); err != nil {
		pool.Close()
		return nil, &startupError{operation: "database_connection", category: observability.Category(err)}
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		pool.Close()
		return nil, &startupError{operation: "database_migration", category: observability.Category(err)}
	}
	slog.Info("Database ready and migrations applied", "component", "postgres", "operation", "startup")
	return pool, nil
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	slog.Info("HTTP server started", "component", "http", "operation", "listen")

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
		return &startupError{operation: "http_serve", category: observability.Category(serveErr)}
	}
	slog.Info("HTTP server stopped", "component", "http", "operation", "shutdown")
	return nil
}

func shutdownHTTP(server *http.Server) error {
	// Shutdown must remain usable after the signal context has been canceled.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := server.Shutdown(shutdownCtx); err != nil {
		// Shutdown's failure takes precedence over a secondary forced-close error.
		_ = server.Close()
		return &startupError{operation: "http_shutdown", category: observability.Category(err)}
	}
	return nil
}

func waitForDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var attempt int

	for {
		pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)

		err := pool.Ping(pingCtx)
		pingCancel()

		if err == nil {
			return nil
		}

		attempt++
		observability.Logger(ctx, "postgres", "connect").DebugContext(ctx, "Database readiness retry", "attempt", attempt, "error_category", observability.Category(err))

		select {
		case <-ctx.Done():
			return ctx.Err()
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
		slog.Error("Telegram model initialization failed", "component", "telegram", "operation", "startup", "error_category", "configuration")
		close(stopped)
		return stopped
	}

	store := postgres.NewTelegramStore(pool)
	orchestrator := conversation.New(model, store, configuration.TelegramAllowedUserIDs, configuration.TimeZone)
	bot := telegram.NewBot(telegram.New(configuration.TelegramToken, configuration.Telegram), configuration.TelegramAllowedUserIDs, orchestrator.Handle)
	go func() {
		defer close(stopped)
		slog.Info("Telegram polling started", "component", "telegram", "operation", "poll")
		if err := bot.Run(ctx, configuration.TelegramPollRetryDelay); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("Telegram polling stopped unexpectedly", "component", "telegram", "operation", "poll", "error_category", observability.Category(err))
		}
		slog.Info("Telegram polling stopped", "component", "telegram", "operation", "poll")
	}()
	return stopped
}
