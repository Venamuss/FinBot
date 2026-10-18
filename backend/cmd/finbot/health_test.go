package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadiness(t *testing.T) {
	cases := []struct {
		name      string
		available bool
		status    int
		body      string
	}{
		{name: "available", available: true, status: http.StatusNoContent},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: "unavailable\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := readinessHandler(func(ctx context.Context) error {
				called = true
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Error("readiness query has no bounded deadline")
				}
				if !test.available {
					return errors.New("postgres://private-credentials private-diagnostics")
				}
				return nil
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if !called || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("readiness did not probe the dependency or prevent caching")
			}
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatal("readiness returned an incorrect status or unsafe body")
			}
		})
	}
}

func TestReadinessHonorsRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := readinessHandler(func(ctx context.Context) error {
		return ctx.Err()
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("canceled probe reported ready")
	}
}
