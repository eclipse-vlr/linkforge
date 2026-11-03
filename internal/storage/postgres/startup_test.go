package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestStoreStartupCancellationInterruptsPoolConstructor(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entered, peerDone := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	go func() {
		defer close(peerDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var first [1]byte
		if _, err := io.ReadFull(conn, first[:]); err != nil {
			return
		}
		close(entered)
		<-release
	}()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		store, err := NewStore(ctx, "postgres://user:secret@"+listener.Addr().String()+"/db?sslmode=disable&connect_timeout=30&pool_min_conns=1&pool_max_conns=1")
		if store != nil {
			store.Close()
		}
		result <- err
	}()
	joined := false
	defer func() {
		cancel()
		close(release)
		_ = listener.Close()
		<-peerDone
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("startup owner did not finish")
			}
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handshake did not start")
	}
	cancel()
	select {
	case err := <-result:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup cleanup waited for the 30-second handshake timeout")
	}
}
