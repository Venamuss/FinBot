package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Venamuss/FinBot/internal/observability"
)

var requestSequence atomic.Uint64

type responseWriter struct {
	http.ResponseWriter
	status        int
	errorCategory string
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(data)
	if err != nil {
		w.errorCategory = "response_write"
	}
	return written, err
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// IDs are generated locally, never copied from untrusted headers. The sequence
	// identifies requests within this process; lifecycle logs delimit restarts.
	id := strconv.FormatUint(requestSequence.Add(1), 10)
	ctx := observability.WithRequestID(r.Context(), id)
	ctx, cancel := context.WithTimeout(ctx, h.requestTimeout)
	defer cancel()

	r = r.WithContext(ctx)
	recorder := &responseWriter{ResponseWriter: w}
	started := time.Now()
	h.mux.ServeHTTP(recorder, r)
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	if recorder.errorCategory == "" {
		recorder.errorCategory = "none"
	}
	level := slog.LevelInfo
	if recorder.status >= 500 || recorder.errorCategory == "response_write" {
		level = slog.LevelError
	} else if recorder.status >= 400 {
		level = slog.LevelWarn
	}
	// Pattern contains only registered route templates, never raw IDs, paths,
	// queries, headers, field names, bodies, or error messages.
	observability.Logger(ctx, "http", "request").Log(ctx, level, "HTTP request completed",
		"route", r.Pattern, "http_status", recorder.status,
		"duration_ms", time.Since(started).Milliseconds(), "error_category", recorder.errorCategory)
}
