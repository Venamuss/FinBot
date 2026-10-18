package main

import (
	"context"
	"net/http"
	"time"
)

// Readiness is registered only after migrations. A running HTTP listener alone
// does not imply the application can serve requests that require PostgreSQL.
func readinessHandler(ping func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		w.Header().Set("Cache-Control", "no-store")
		if err := ping(ctx); err != nil {
			// Driver errors can contain credentials; never send them to the probe.
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
