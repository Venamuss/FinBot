package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDatabaseStartupErrorsAreSafe(t *testing.T) {
	cases := []struct {
		name, databaseURL, operation, category string
	}{
		{"invalid configuration", "postgres://user:private-password@host:bad/db", "database_configuration", "configuration"},
		{"canceled startup", "postgres://user:private-password@127.0.0.1:1/db", "database_connection", "canceled"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			pool, err := openDatabase(ctx, test.databaseURL)
			if pool != nil || err == nil {
				t.Fatal("failed startup returned a pool or no error")
			}
			var failure *startupError
			if !errors.As(err, &failure) || failure.operation != test.operation || failure.category != test.category || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "postgres://") {
				t.Fatal("startup lost safe stage/category metadata or exposed credentials")
			}
		})
	}
}

func TestServeHTTPDrainsActiveRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	shutdownStarted := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-t.Context().Done():
		}
	})}
	defer server.Close()
	server.RegisterOnShutdown(func() {
		close(shutdownStarted)
	})
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveHTTP(ctx, server, listener)
	}()

	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				err = errors.New("active request was interrupted")
			}
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case <-shutdownStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP shutdown did not start")
	}
	select {
	case err := <-stopped:
		t.Fatalf("server stopped before draining active request: %v", err)
	default:
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP shutdown did not finish")
	}
}
