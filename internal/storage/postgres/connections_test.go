package postgres

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
)

func TestOwnedLookupCancelsInFlightResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered, result := make(chan struct{}), make(chan error, 1)
		lookup := ownedLookup(lifetime, func(ctx context.Context, _ string) ([]string, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		go func() { _, err := lookup(context.Background(), "unused"); result <- err }()
		<-entered
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("lookup error: %v", err)
		}
	})
}

func TestOwnedDialerCancelsInFlightDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered := make(chan struct{})
		result := make(chan error, 1)
		dial := ownedDialer(lifetime, func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		go func() { _, err := dial(context.Background(), "tcp", "unused"); result <- err }()
		<-entered
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("dial error: %v", err)
		}
	})
}

func TestOwnedConnectionCancellationAndConcurrentClose(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			left, right := net.Pipe()
			defer right.Close()
			dial := ownedDialer(lifetime, func(context.Context, string, string) (net.Conn, error) { return left, nil })
			conn, err := dial(context.Background(), "tcp", "unused")
			if err != nil {
				t.Fatal(err)
			}
			read := make(chan error, 1)
			go func() { var b [1]byte; _, err := conn.Read(b[:]); read <- err }()
			synctest.Wait()
			if cancelFirst {
				cancel()
			}
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() { _ = conn.Close() })
			}
			wg.Wait()
			if err := <-read; err == nil {
				t.Fatal("blocked socket read was not interrupted")
			}
			cancel()
			synctest.Wait()
		})
	}
}
