package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func startTestServer(t *testing.T, server *http.Server, timeout time.Duration) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- serveHTTP(ctx, server, listener, newTestLogger(), timeout) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return "http://" + listener.Addr().String(), cancel, result
}

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server event")
	}
}

func awaitServerResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server result")
		return nil
	}
}

func TestServeHTTPWaitsForActiveRequest(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	stopping := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			_, _ = io.WriteString(w, "completed")
		case <-r.Context().Done():
		}
	})}
	server.RegisterOnShutdown(func() { close(stopping) })
	url, cancel, result := startTestServer(t, server, 3*time.Second)
	client := &http.Client{Timeout: 4 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	requestResult := make(chan error, 1)
	go func() {
		response, err := client.Get(url)
		if err == nil {
			defer response.Body.Close()
			var body []byte
			body, err = io.ReadAll(response.Body)
			if err == nil && (response.StatusCode != http.StatusOK || string(body) != "completed") {
				err = errors.New("active request did not complete successfully")
			}
		}
		requestResult <- err
	}()
	awaitSignal(t, entered)
	cancel()
	awaitSignal(t, stopping)
	select {
	case err := <-result:
		t.Fatalf("server stopped before active request completed: %v", err)
	default:
	}
	close(release)
	if err := awaitServerResult(t, requestResult); err != nil {
		t.Fatal(err)
	}
	if err := awaitServerResult(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestServeHTTPClosesConnectionsAfterTimeout(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	})}
	url, cancel, result := startTestServer(t, server, 50*time.Millisecond)
	client := &http.Client{Timeout: 4 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	requestResult := make(chan error, 1)
	go func() {
		response, err := client.Get(url)
		if response != nil {
			response.Body.Close()
		}
		requestResult <- err
	}()
	awaitSignal(t, entered)
	cancel()
	if err := awaitServerResult(t, result); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected shutdown deadline error, got %v", err)
	}
	awaitSignal(t, canceled)
	if err := awaitServerResult(t, requestResult); err == nil {
		t.Fatal("expected interrupted request")
	}
}

func TestServeHTTPReturnsListenerError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	result := make(chan error, 1)
	go func() {
		result <- serveHTTP(context.Background(), &http.Server{}, listener, newTestLogger(), time.Second)
	}()
	if err := awaitServerResult(t, result); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected listener error, got %v", err)
	}
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
