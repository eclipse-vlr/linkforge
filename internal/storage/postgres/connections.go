package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// connectionLifecycle limits only ConnectConfig, including DNS, dial and
// authentication. CancelRequest uses pgconn's independent bounded cleanup
// context and the original dialer, even after the store stops constructors.
type connectionLifecycle struct {
	lifetime context.Context
}

type connectionCleanupKey struct{}

func (t *connectionLifecycle) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	connecting, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	stop := context.AfterFunc(t.lifetime, func() {
		defer close(done)
		cancel()
	})
	if t.lifetime.Err() != nil {
		cancel()
	}
	cleanup := func() {
		if !stop() {
			<-done
		}
		cancel()
	}
	return context.WithValue(connecting, connectionCleanupKey{}, cleanup)
}

func (*connectionLifecycle) TraceConnectEnd(ctx context.Context, _ pgx.TraceConnectEndData) {
	ctx.Value(connectionCleanupKey{}).(func())()
}

// ConnConfig.Tracer accepts a QueryTracer; query contexts stay unchanged.
func (*connectionLifecycle) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (*connectionLifecycle) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
