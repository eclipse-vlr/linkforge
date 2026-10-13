package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/lib/pq"
)

const migrationTimeout = 30 * time.Second

func runMigrations(ctx context.Context, databaseURL string) error {
	return applyMigrations(ctx, "file://migrations", databaseURL)
}

func applyMigrations(parent context.Context, sourceURL, databaseURL string) (result error) {
	ctx, cancel := context.WithTimeout(parent, migrationTimeout)
	var db *sql.DB
	var conn *sql.Conn
	var m *migrate.Migrate
	defer func() {
		ctxErr := ctx.Err()
		// On failure, interrupt outstanding driver operations before cleanup.
		// On success, let PostgreSQL receive a normal connection termination.
		if result != nil || ctxErr != nil {
			cancel()
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
		result = errors.Join(result, ctxErr)
	}()

	connector, err := pq.NewConnector(databaseURL)
	if err != nil {
		return fmt.Errorf("configure migration connection: %w", err)
	}
	connector.Dialer(migrationDialer{ctx: ctx})
	db = sql.OpenDB(connector)
	conn, err = db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect for migrations: %w", err)
	}
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

// The migration driver uses context.Background for locks and some queries.
// Binding its sockets to the startup context also bounds authentication, locks,
// and SQL execution, without leaving a blocked migration goroutine behind.
type migrationDialer struct {
	ctx context.Context
}

func (d migrationDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialTimeout(network, address, 0)
}

func (d migrationDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(d.ctx, network, address)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(d.ctx, func() { _ = conn.Close() })
	return &migrationConn{Conn: conn, stop: stop}, nil
}

type migrationConn struct {
	net.Conn
	stop func() bool
}

func (c *migrationConn) Close() error {
	c.stop()
	return c.Conn.Close()
}
