package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStats(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to test statistics against PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	schema := "stats_test_" + suffix
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean test schema: %v", err)
		}
	}()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = quotedSchema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &PostgresStore{pool: pool}
	apply := func(path string) {
		t.Helper()
		sql, err := os.ReadFile("../../migrations/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	apply("000001_create_links_table.up.sql")
	if err := store.Save(ctx, "existing", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	apply("000002_add_click_count.up.sql")
	if err := store.Save(ctx, "new", "https://example.org"); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"existing", "new"} {
		stats, err := store.GetStats(ctx, code)
		if err != nil || stats.Code != code || stats.ClickCount != 0 {
			t.Fatalf("expected initial zero count for %s, got %+v, %v", code, stats, err)
		}
	}
	const increments = 40
	errs := make(chan error, increments)
	var wg sync.WaitGroup
	for i := 0; i < increments; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.IncrementClicks(ctx, "existing")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	stats, err := store.GetStats(ctx, "existing")
	if err != nil || stats.ClickCount != increments {
		t.Fatalf("concurrent increments lost: %+v, %v", stats, err)
	}
	if err := store.IncrementClicks(ctx, "missing"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("expected missing link on increment, got %v", err)
	}
	if _, err := store.GetStats(ctx, "missing"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("expected missing link on stats, got %v", err)
	}
	apply("000002_add_click_count.down.sql")
	if original, err := store.Get(ctx, "existing"); err != nil || original != "https://example.com" {
		t.Fatalf("rollback affected link: %q, %v", original, err)
	}
	apply("000002_add_click_count.up.sql")
	if stats, err := store.GetStats(ctx, "existing"); err != nil || stats.ClickCount != 0 {
		t.Fatalf("reapply failed: %+v, %v", stats, err)
	}
}
