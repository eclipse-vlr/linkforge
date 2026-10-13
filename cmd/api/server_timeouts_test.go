package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPServerTimeouts(t *testing.T) {
	server := newHTTPServer(":0", http.NotFoundHandler())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatal("all HTTP timeouts must be bounded")
	}
	if server.ReadHeaderTimeout > server.ReadTimeout || server.ReadTimeout >= server.WriteTimeout {
		t.Fatal("timeouts must leave time for reading headers, reading a body, and writing a response")
	}
}

func TestHTTPServerRejectsSlowRequests(t *testing.T) {
	for _, tc := range []struct{ name, request string }{
		{"headers", "POST /api/v1/links HTTP/1.1\r\nHost: localhost\r\n"},
		{"body", "POST /api/v1/links HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n{\"url\":"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewLinkStore()
			server := newHTTPServer(":0", newRouter(store, newTestLogger(), "https://sho.rt"))
			server.ReadHeaderTimeout = 100 * time.Millisecond
			server.ReadTimeout = 200 * time.Millisecond
			address, cancel, result := startTestServer(t, server, time.Second)
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(address, "http://"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(conn, tc.request); err != nil {
				t.Fatal(err)
			}
			response, err := io.ReadAll(conn)
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				t.Fatal("server failed to close slow connection")
			}
			if strings.Contains(string(response), "201 Created") {
				t.Fatalf("slow request was accepted: %s", response)
			}
			cancel()
			if err := awaitServerResult(t, result); err != nil {
				t.Fatal(err)
			}
			store.mu.RLock()
			defer store.mu.RUnlock()
			if len(store.links) != 0 {
				t.Fatal("slow request saved a link")
			}
		})
	}
}
