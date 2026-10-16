package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListRollbackUnavailableConnection(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	// Bound the fake server too, so the regression fails instead of hanging.
	if err := server.SetDeadline(time.Now().Add(5 * rollbackTimeout)); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveUnavailableRollback(server)
	}()

	config, err := pgxpool.ParseConfig("postgres://test@127.0.0.1/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	var dialed atomic.Bool
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		if dialed.Swap(true) {
			return nil, errors.New("synthetic database unavailable")
		}
		return client, nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	repository := New(pool)
	filter := expense.Filter{
		Limit: 50,
		Sort:  "spent_at",
		Order: "desc",
	}
	started := time.Now()
	_, err = repository.List(context.Background(), filter)
	elapsed := time.Since(started)

	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		t.Fatalf("original query error lost: %v", err)
	}
	if databaseError.Code != "XX000" {
		t.Fatalf("want original error code XX000, got %q", databaseError.Code)
	}
	if elapsed > 3*rollbackTimeout {
		t.Fatalf("rollback cleanup took %s", elapsed)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("failed transaction retained its pool connection")
	}
}

// Speak just enough PostgreSQL to fail the count query, then stop answering
// rollback. This exercises the real pgx network timeout without a live database.
func serveUnavailableRollback(conn net.Conn) error {
	defer conn.Close()
	backend := pgproto3.NewBackend(conn, conn)
	if _, err := backend.ReceiveStartupMessage(); err != nil {
		return err
	}
	backend.Send(&pgproto3.AuthenticationOk{})
	backend.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
	backend.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
	backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if err := backend.Flush(); err != nil {
		return err
	}

	beginQuery, err := receiveQuery(backend, "begin")
	if err != nil {
		return err
	}
	if !strings.Contains(beginQuery, "repeatable read read only") {
		return errors.New("list transaction options changed")
	}
	backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("BEGIN")})
	backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'T'})
	if err := backend.Flush(); err != nil {
		return err
	}

	if _, err := receiveQuery(backend, "SELECT count(*)"); err != nil {
		return err
	}
	backend.Send(&pgproto3.ErrorResponse{
		Severity: "ERROR",
		Code:     "XX000",
		Message:  "synthetic query failure",
	})
	backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'E'})
	if err := backend.Flush(); err != nil {
		return err
	}

	if _, err := receiveQuery(backend, "rollback"); err != nil {
		return err
	}
	// Leave rollback unanswered. pgx should close the connection when its
	// cleanup context expires, optionally sending a Terminate message first.
	message, err := backend.Receive()
	if _, ok := message.(*pgproto3.Terminate); ok {
		_, err = backend.Receive()
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("expected connection close after rollback timeout: %w", err)
	}
	return nil
}

func receiveQuery(backend *pgproto3.Backend, expectedPrefix string) (string, error) {
	message, err := backend.Receive()
	if err != nil {
		return "", err
	}
	query, ok := message.(*pgproto3.Query)
	if !ok {
		return "", fmt.Errorf("expected %s query, got %T", expectedPrefix, message)
	}
	if !strings.HasPrefix(query.String, expectedPrefix) {
		return "", fmt.Errorf("expected query starting with %s", expectedPrefix)
	}
	return query.String, nil
}
