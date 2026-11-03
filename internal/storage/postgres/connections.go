package postgres

import (
	"context"
	"net"
	"sync"
)

// Name resolution happens before DialFunc in pgx and needs the same lifetime.
func ownedLookup(lifetime context.Context, lookup func(context.Context, string) ([]string, error)) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, host string) ([]string, error) {
		if err := lifetime.Err(); err != nil {
			return nil, err
		}
		lookupCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		return lookup(lookupCtx, host)
	}
}

// ownedDialer makes even background pool work stop when the store closes.
func ownedDialer(lifetime context.Context, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := lifetime.Err(); err != nil {
			return nil, err
		}
		dialCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lifetime, cancel)
		conn, err := dial(dialCtx, network, address)
		stop()
		cancel()
		if err != nil {
			return nil, err
		}
		owned := &ownedConnection{Conn: conn, closed: make(chan struct{})}
		owned.stop = context.AfterFunc(lifetime, func() {
			defer close(owned.closed)
			_ = conn.Close()
		})
		return owned, nil
	}
}

type ownedConnection struct {
	net.Conn
	stop      func() bool
	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (c *ownedConnection) Close() error {
	c.closeOnce.Do(func() {
		if !c.stop() {
			// Join a running cancellation callback before returning socket ownership.
			<-c.closed
		}
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}
