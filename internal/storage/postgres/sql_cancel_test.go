package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
	"github.com/jackc/pgx/v5"
)

// A canceled client call alone is not the oracle: the backend must terminate
// before releasing the row lock, and the canceled UPDATE must not increment it.
func TestStoreClosePreservesServerSQLCancellation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	schema := pgx.Identifier{"cancel_close_" + code}.Sanitize()
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema+"; CREATE TABLE "+schema+".links(code text PRIMARY KEY, click_count bigint NOT NULL); INSERT INTO "+schema+".links VALUES ('abc12345', 0)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	for attempt := range 5 {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			appName := fmt.Sprintf("cancel_close_%s_%d", code, attempt)
			q := parsed.Query()
			q.Set("search_path", schema)
			q.Set("application_name", appName)
			q.Set("pool_max_conns", "1")
			parsed.RawQuery = q.Encode()
			store, err := NewStore(ctx, parsed.String())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			tx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(ctx, "SELECT * FROM "+schema+".links FOR UPDATE"); err != nil {
				t.Fatal(err)
			}
			queryCtx, stopQuery := context.WithCancel(ctx)
			defer stopQuery()
			result := make(chan error, 1)
			go func() { result <- store.IncrementClicks(queryCtx, "abc12345") }()
			joined := false
			defer func() {
				stopQuery()
				if !joined {
					select {
					case <-result:
					case <-ctx.Done():
						t.Error("query owner did not finish")
					}
				}
			}()
			waiting := func() (bool, error) {
				if _, err := tx.Exec(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
					return false, err
				}
				var blocked bool
				err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE application_name=$1 AND state='active' AND wait_event_type='Lock')", appName).Scan(&blocked)
				return blocked, err
			}
			if !awaitDatabaseState(t, ctx, waiting) {
				t.Fatal("UPDATE never reached the row lock")
			}
			stopQuery()
			select {
			case err := <-result:
				joined = true
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("query error: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			store.Close()
			backendGone := func() (bool, error) {
				if _, err := admin.Exec(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
					return false, err
				}
				var gone bool
				err := admin.QueryRow(ctx, "SELECT NOT EXISTS(SELECT FROM pg_stat_activity WHERE application_name=$1)", appName).Scan(&gone)
				return gone, err
			}
			if !awaitDatabaseState(t, ctx, backendGone) {
				t.Error("PostgreSQL backend still present after Store.Close; SQL cancellation was not delivered")
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			// Once the backend disappears it cannot commit after this assertion.
			if !awaitDatabaseState(t, ctx, backendGone) {
				t.Fatal("backend did not finish after unlocking")
			}
			var count int64
			if err := admin.QueryRow(ctx, "SELECT click_count FROM "+schema+".links WHERE code='abc12345'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("canceled UPDATE committed after close: click_count=%d", count)
			}
		})
	}
}

func awaitDatabaseState(t *testing.T, ctx context.Context, check func() (bool, error)) bool {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := check()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return true
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}
