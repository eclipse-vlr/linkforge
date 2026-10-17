package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	migrationTimeout       = 30 * time.Second
	migrationCancelTimeout = 2 * time.Second
)

func runMigrations(ctx context.Context, databaseURL string) error {
	return applyMigrations(ctx, "file://migrations", databaseURL)
}

func applyMigrations(parent context.Context, sourceURL, databaseURL string) (result error) {
	ctx, cancel := context.WithTimeout(parent, migrationTimeout)
	var db *sql.DB
	var conn *sql.Conn
	var m *migrate.Migrate
	var stopCancellation func() bool
	var cancellationDone chan struct{}
	var cancellationErr error
	defer func() {
		ctxErr := ctx.Err()
		// On failure, interrupt outstanding driver operations before cleanup.
		// On success, let PostgreSQL receive a normal connection termination.
		if result != nil || ctxErr != nil {
			cancel()
		}
		// Join the callback before closing driver resources or reading its error.
		if stopCancellation != nil && !stopCancellation() {
			<-cancellationDone
		}
		if m != nil {
			sourceErr, databaseErr := m.Close()
			result = errors.Join(result, sourceErr, databaseErr)
		} else if conn != nil {
			result = errors.Join(result, conn.Close())
		}
		if db != nil {
			result = errors.Join(result, db.Close())
		}
		cancel()
		result = errors.Join(result, ctxErr, cancellationErr)
	}()

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("invalid migration database configuration")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	// Server-side limits remain effective if cancellation cannot be delivered.
	remainingMS := max(int64(1), int64((time.Until(deadline)+time.Millisecond-1)/time.Millisecond))
	config.RuntimeParams["statement_timeout"] = strconv.FormatInt(remainingMS, 10)
	config.RuntimeParams["lock_timeout"] = strconv.FormatInt(remainingMS, 10)
	db = stdlib.OpenDB(*config)
	db.SetMaxOpenConns(1)
	conn, err = db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect for migrations: %w", err)
	}
	var native *pgx.Conn
	if err := conn.Raw(func(driverConn any) error {
		pgxDriver, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected migration connection type %T", driverConn)
		}
		native = pgxDriver.Conn()
		return nil
	}); err != nil {
		return fmt.Errorf("access migration connection: %w", err)
	}
	pgConn := native.PgConn()
	cancellationDone = make(chan struct{})
	stopCancellation = context.AfterFunc(ctx, func() {
		defer close(cancellationDone)
		// A canceled startup context cannot deliver the separate cancel request.
		cancelCtx, cancelRequest := context.WithTimeout(context.Background(), migrationCancelTimeout)
		defer cancelRequest()
		cancellationErr = pgConn.CancelRequest(cancelCtx)
		if cancellationErr == nil {
			cancellationErr = cancelCtx.Err()
		}
		if cancellationErr != nil {
			cancellationErr = fmt.Errorf("cancel migration query: %w", cancellationErr)
		}
		// Only after sending CancelRequest, unblock I/O and forbid further SQL.
		_ = pgConn.Conn().Close()
	})
	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("initialize migration driver: %w", err)
	}
	m, err = migrate.NewWithDatabaseInstance(sourceURL, "postgres", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
