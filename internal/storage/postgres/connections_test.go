package postgres

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5"
)

func TestConnectionLifecycleCancelsConnect(t *testing.T) {
	for _, alreadyStopped := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			if alreadyStopped {
				cancel()
			}
			tracer := &connectionLifecycle{lifetime: lifetime}
			ctx := tracer.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
			cancel()
			<-ctx.Done()
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal(ctx.Err())
			}
			tracer.TraceConnectEnd(ctx, pgx.TraceConnectEndData{})
		})
	}
}

func TestConnectionLifecycleReleasesConstructorAfterConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime, cancel := context.WithCancel(context.Background())
		defer cancel()
		tracer := &connectionLifecycle{lifetime: lifetime}
		ctx := tracer.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
		tracer.TraceConnectEnd(ctx, pgx.TraceConnectEndData{})
		cancel()
		synctest.Wait()
		queryCtx := context.Background()
		if tracer.TraceQueryStart(queryCtx, nil, pgx.TraceQueryStartData{}) != queryCtx {
			t.Fatal("constructor lifecycle changed the query context")
		}
	})
}
