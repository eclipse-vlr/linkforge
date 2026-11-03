package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
	"github.com/jackc/pgx/v5"
)

// Exercise the complete application shutdown, including deferred pool.Close.
// The proxy forwards startup traffic, then accepts new TCP connections without
// replying to PostgreSQL handshakes. No database rows outside the test schema
// are changed. Real network I/O is intentionally outside a synctest bubble.
func TestApplicationShutdownDuringPoolRecovery(t *testing.T) {
	for _, minimum := range []string{"pool_min_conns", "pool_min_idle_conns"} {
		t.Run(minimum, func(t *testing.T) {
			dsn := os.Getenv("TEST_DATABASE_URL")
			if dsn == "" {
				t.Skip("requires TEST_DATABASE_URL")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			admin, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close(context.Background())
			code, err := link.GenerateCode()
			if err != nil {
				t.Fatal(err)
			}
			schema := pgx.Identifier{"shutdown_" + code}.Sanitize()
			if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}()

			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			upstream := parsed.Host
			if parsed.Port() == "" {
				upstream = net.JoinHostPort(parsed.Hostname(), "5432")
			}
			proxy, stalled := newShutdownProxy(t, upstream)
			parsed.Host = proxy.listener.Addr().String()
			q := parsed.Query()
			q.Set("search_path", schema)
			q.Set("pool_max_conns", "1")
			q.Set(minimum, "1")
			q.Set("pool_health_check_period", "20ms")
			q.Set("pool_max_conn_lifetime", "100ms")
			// Explicitly long timeout must not extend shutdown; the other case
			// exercises the default (no connection timeout in the supplied URL).
			q.Del("connect_timeout")
			if minimum == "pool_min_idle_conns" {
				q.Set("connect_timeout", "30")
			}
			parsed.RawQuery = q.Encode()
			port, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := port.Addr().String()
			if err := port.Close(); err != nil {
				t.Fatal(err)
			}
			clearConfigEnv(t)
			t.Setenv("DATABASE_URL", parsed.String())
			t.Setenv("HTTP_ADDR", addr)
			t.Setenv("SHUTDOWN_TIMEOUT", "200ms")
			t.Chdir("../..")
			ready := make(chan struct{})
			logger := slog.New(slog.NewJSONHandler(&startupWriter{ready: ready}, nil))
			appCtx, stopApp := context.WithCancel(ctx)
			result := make(chan error, 1)
			go func() { result <- run(appCtx, logger) }()
			joined := false
			defer func() {
				stopApp()
				// Also unblocks an unfixed implementation on test failure.
				proxy.close()
				if !joined {
					select {
					case <-result:
					case <-ctx.Done():
						t.Error("application cleanup timed out")
					}
				}
			}()
			select {
			case <-ready:
			case err := <-result:
				joined = true
				t.Fatalf("startup failed: %v", err)
			case <-ctx.Done():
				t.Fatal("startup timed out")
			}
			proxy.stall.Store(true)
			select {
			case <-stalled:
			case <-ctx.Done():
				t.Fatal("pool recovery did not start")
			}
			start := time.Now()
			stopApp()
			select {
			case err := <-result:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("full shutdown with stalled %s recovery: %s (HTTP budget 200ms)", minimum, time.Since(start))
			case <-time.After(time.Second):
				t.Fatal("full shutdown exceeded 200ms budget plus scheduling tolerance; PostgreSQL handshake still blocked")
			}
		})
	}
}

type startupWriter struct {
	ready chan struct{}
	once  sync.Once
}

func (w *startupWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "HTTP server started") {
		w.once.Do(func() { close(w.ready) })
	}
	return len(p), nil
}

type shutdownProxy struct {
	listener  net.Listener
	stall     atomic.Bool
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func newShutdownProxy(t *testing.T, upstream string) (*shutdownProxy, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &shutdownProxy{listener: listener, cancel: cancel, done: make(chan struct{})}
	stalled := make(chan struct{}, 1)
	go func() {
		defer close(p.done)
		for {
			incoming, err := listener.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer incoming.Close()
				stop := context.AfterFunc(ctx, func() { _ = incoming.Close() })
				defer stop()
				if p.stall.Load() {
					// Wait for actual protocol bytes before signaling the test.
					var first [1]byte
					if _, err := io.ReadFull(incoming, first[:]); err != nil {
						return
					}
					select {
					case stalled <- struct{}{}:
					default:
					}
					_, _ = io.Copy(io.Discard, incoming)
					return
				}
				outgoing, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", upstream)
				if err != nil {
					return
				}
				defer outgoing.Close()
				stopOut := context.AfterFunc(ctx, func() { _ = outgoing.Close() })
				defer stopOut()
				copied := make(chan struct{})
				go func() { _, _ = io.Copy(outgoing, incoming); _ = outgoing.Close(); close(copied) }()
				_, _ = io.Copy(incoming, outgoing)
				_ = incoming.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(p.close)
	return p, stalled
}

func (p *shutdownProxy) close() {
	p.closeOnce.Do(func() {
		p.cancel()
		_ = p.listener.Close()
		<-p.done
		p.wg.Wait()
	})
}
