package main

import (
	"context"
	"errors"
	"strings"
	"testing"
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
