package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener, logger *slog.Logger, timeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	logger.Info("HTTP server started", "addr", listener.Addr().String())

	var serverErr error
	select {
	case <-ctx.Done():
	case serverErr = <-serveErr:
		// Also drain active requests if the listener unexpectedly fails.
	}

	logger.Info("stopping HTTP server", "timeout", timeout.String())
	// The signal context is already canceled; shutdown needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		logger.Error("HTTP shutdown failed; closing connections", "error", shutdownErr)
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}

	if serverErr == nil {
		serverErr = <-serveErr
	}
	if errors.Is(serverErr, http.ErrServerClosed) {
		serverErr = nil
	}
	if err := errors.Join(serverErr, shutdownErr); err != nil {
		return fmt.Errorf("stop HTTP server: %w", err)
	}
	logger.Info("HTTP server stopped")
	return nil
}
