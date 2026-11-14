package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"acona-api-humantxt/internal/api"
	"acona-api-humantxt/internal/fetch"
	"acona-api-humantxt/internal/vm"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	downloader := fetch.New(fetch.Options{AllowPrivate: os.Getenv("HUMANTXT_ALLOW_PRIVATE") == "1"})

	pool := min(runtime.NumCPU(), 8)

	engine, err := vm.New(pool, downloader)
	if err != nil {
		logger.Error("start javascript engine", "err", err)
		os.Exit(1)
	}
	defer engine.Close()

	handler := logRequests(logger, api.New(engine))

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       90 * time.Second,
	}

	shutdownOnSignal(srv, logger)

	logger.Info("listening", "addr", addr, "pool", pool)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("listen", "err", err)
		os.Exit(1)
	}
}

func shutdownOnSignal(srv *http.Server, logger *slog.Logger) {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-done
		// an extraction of a large document needs tens of seconds to finish cleanly
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("shutdown", "err", err)
		}
	}()
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		logger.Info("request",
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"dur", time.Since(start).Round(time.Millisecond),
			"remote", clientIP(r),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}
