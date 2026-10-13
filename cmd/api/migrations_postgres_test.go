package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMigrationsPostgres(t *testing.T) {
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

	// Hold a table lock so that the next migration cannot complete.
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "LOCK TABLE "+quotedSchema+".links IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("000003_blocked.up.sql", []byte("ALTER TABLE links ADD COLUMN test_column text;"), 0600); err != nil {
		t.Fatal(err)
	}
	migrationCtx, stopMigration := context.WithCancel(ctx)
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
	stopMigration()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked SQL migration did not stop")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// An interrupted migration stays dirty; never silently force its version.
	if err := admin.QueryRow(ctx, "SELECT version, dirty FROM "+quotedSchema+".schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != 3 || !dirty {
		t.Fatalf("expected dirty interrupted migration, got %d, %t", version, dirty)
	}
}
