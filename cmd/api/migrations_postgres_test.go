package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
)

func TestMigrationsPostgres(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "advisory_lock"} {
		t.Run(mode, func(t *testing.T) { testMigrationsPostgresCancellation(t, mode) })
	}
}

func testMigrationsPostgresCancellation(t *testing.T, mode string) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to test migration cancellation against PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	suffix, err := generateCode()
	if err != nil {
		t.Fatal(err)
	}
	schema := "migrations_test_" + suffix
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean schema: %v", err)
		}
	}()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", quotedSchema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()

	dir := t.TempDir()
	for _, name := range []string{"000001_create_links_table.up.sql", "000002_add_click_count.up.sql"} {
		data, err := os.ReadFile(filepath.Join("../../migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	for range 2 {
		if err := applyMigrations(ctx, "file://.", parsed.String()); err != nil {
			t.Fatal(err)
		}
	}
	var version int
	var dirty bool
	if err := admin.QueryRow(ctx, "SELECT version, dirty FROM "+quotedSchema+".schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != 2 || dirty {
		t.Fatalf("unexpected migration state: %d, %t", version, dirty)
	}

	// Block either SQL execution or the migrator's initial advisory lock.
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if mode == "advisory_lock" {
		lockID, err := database.GenerateAdvisoryLockId(admin.Config().Database, schema, "schema_migrations")
		if err != nil {
			t.Fatal(err)
		}
		id, err := strconv.ParseInt(lockID, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", id); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := tx.Exec(ctx, "LOCK TABLE "+quotedSchema+".links IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("000003_blocked.up.sql", []byte("ALTER TABLE links ADD COLUMN test_column text;"), 0600); err != nil {
		t.Fatal(err)
	}
	migrationCtx, stopMigration := context.WithCancel(ctx)
	if mode == "deadline" {
		stopMigration()
		migrationCtx, stopMigration = context.WithTimeout(ctx, 3*time.Second)
	}
	defer stopMigration()
	result := make(chan error, 1)
	go func() { result <- applyMigrations(migrationCtx, "file://.", parsed.String()) }()
	// Wait for a real SQL lock wait before canceling, not merely a started goroutine.
	pollCtx, stopPoll := context.WithTimeout(ctx, 5*time.Second)
	defer stopPoll()
	for {
		var waiting bool
		if _, err := tx.Exec(pollCtx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(pollCtx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')", schema).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("migration finished before cancellation: %v", err)
		case <-pollCtx.Done():
			t.Fatal("migration did not reach lock wait")
		case <-time.After(20 * time.Millisecond):
		}
	}
	wantErr := context.Canceled
	if mode != "deadline" {
		stopMigration()
	} else {
		wantErr = context.DeadlineExceeded
	}
	select {
	case err := <-result:
		if !errors.Is(err, wantErr) {
			t.Fatalf("got %v, want %v", err, wantErr)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("blocked SQL migration did not stop")
	}
	// A closed client is not proof of server-side cancellation. Keep the lock
	// held until PostgreSQL itself confirms that the SQL is no longer active.
	stoppedCtx, stopWait := context.WithTimeout(ctx, 3*time.Second)
	defer stopWait()
	for {
		if _, err := tx.Exec(stoppedCtx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		var active bool
		if err := tx.QueryRow(stoppedCtx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='active')", schema).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if !active {
			break
		}
		select {
		case <-stoppedCtx.Done():
			t.Fatal("migration returned but PostgreSQL is still executing SQL")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var altered bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='links' AND column_name='test_column')", schema).Scan(&altered); err != nil {
		t.Fatal(err)
	}
	if altered {
		t.Fatal("canceled DDL changed the schema after the lock was released")
	}
	// An interrupted migration stays dirty; never silently force its version.
	if err := admin.QueryRow(ctx, "SELECT version, dirty FROM "+quotedSchema+".schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	wantVersion, wantDirty := 3, true
	if mode == "advisory_lock" {
		wantVersion, wantDirty = 2, false
	}
	if version != wantVersion || dirty != wantDirty {
		t.Fatalf("migration state = %d/%t, want %d/%t", version, dirty, wantVersion, wantDirty)
	}
}
