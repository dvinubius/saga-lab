package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Exposure int

const (
	Internal Exposure = iota
	Public
)

func Serve(ctx context.Context, addr string, handler http.Handler, exposure Exposure) error {
	traced := otelhttp.NewHandler(handler, "http.server",
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return exposure == Public }),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/readyz" && !strings.HasPrefix(r.URL.Path, "/static/")
		}))
	server := &http.Server{Addr: addr, Handler: traced, ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.ListenAndServe() }()
	slog.Info("listening", "addr", addr)

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func Readiness(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := check(r.Context()); err != nil {
			slog.Warn("not ready", "error", err)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	}
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write JSON response", "error", err)
	}
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}
