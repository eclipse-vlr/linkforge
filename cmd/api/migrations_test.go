package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestMigrationsStopDuringHandshake(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "cancel"
		if timeout {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			peerDone := make(chan struct{})
			accepted := make(chan struct{})
			go func() {
				defer close(peerDone)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				close(accepted)
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				// Consume startup bytes but never answer the PostgreSQL handshake.
				_, _ = io.Copy(io.Discard, conn)
			}()
			ctx, cancel := context.WithCancel(context.Background())
			if timeout {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- runMigrations(ctx, "postgres://user:password@"+listener.Addr().String()+"/test?sslmode=disable")
			}()
			awaitSignal(t, accepted)
			want := context.DeadlineExceeded
			if !timeout {
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("migration did not stop")
			}
			awaitSignal(t, peerDone)
		})
	}
}
