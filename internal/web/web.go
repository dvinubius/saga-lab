package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

func Serve(ctx context.Context, listener net.Listener, handler http.Handler, exposure Exposure, logger *slog.Logger) error {
	traced := otelhttp.NewHandler(handler, "http.server",
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return exposure == Public }),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/readyz" && !strings.HasPrefix(r.URL.Path, "/static/")
		}))
	server := &http.Server{
		Handler:           traced,
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	logger.Info("listening", "addr", listener.Addr().String())

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

func Readiness(logger *slog.Logger, check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := check(r.Context()); err != nil {
			logger.Warn("not ready", "error", err)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	}
}

func WriteJSON(logger *slog.Logger, w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logger.Warn("write JSON response", "error", err)
	}
}

func WriteError(logger *slog.Logger, w http.ResponseWriter, status int, message string) {
	WriteJSON(logger, w, status, map[string]string{"error": message})
}
