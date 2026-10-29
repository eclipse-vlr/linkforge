//go:build linux

package postgres

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
	"github.com/jackc/pgx/v5"
)

func migrationResourceSnapshot(t *testing.T) (int, int) {
	t.Helper()
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 2); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(profile.String(), "github.com/golang-migrate/migrate/v4.(*Migration).Buffer("), len(entries)
}

func TestMigrationErrorResources(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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
	schema := "migration_resources_" + code
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted+"; CREATE TABLE "+quoted+".links(dummy int)"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", quoted)
	parsed.RawQuery = query.Encode()
	beforeBuffers, beforeFD := migrationResourceSnapshot(t)
	beforeG := runtime.NumGoroutine()
	const failures = 12
	for i := 0; i < failures; i++ {
		err := Migrate(ctx, "file://../../../migrations", parsed.String())
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("failed migration %d = %v, want original CREATE TABLE error", i+1, err)
		}
		var dirty bool
		var version int
		if err := admin.QueryRow(ctx, "SELECT version,dirty FROM "+quoted+".schema_migrations").Scan(&version, &dirty); err != nil {
			t.Fatal(err)
		}
		if version != 1 || !dirty {
			t.Fatalf("failed migration state = %d/%t, want 1/true", version, dirty)
		}
		if _, err := admin.Exec(ctx, "DROP TABLE "+quoted+".schema_migrations"); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	var afterBuffers, afterFD int
	for {
		afterBuffers, afterFD = migrationResourceSnapshot(t)
		if (afterBuffers == beforeBuffers && afterFD <= beforeFD) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	runtime.GC()
	t.Logf("%d failed migrations: Buffer goroutines %d -> %d; FDs %d -> %d; total goroutines %d -> %d", failures, beforeBuffers, afterBuffers, beforeFD, afterFD, beforeG, runtime.NumGoroutine())
	if afterBuffers != beforeBuffers || afterFD > beforeFD {
		var profile bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&profile, 2)
		t.Log(profile.String())
		t.Fatalf("FAILED RESOURCE CLEANUP: Buffer goroutines delta=%d, FD delta=%d", afterBuffers-beforeBuffers, afterFD-beforeFD)
	}
}
