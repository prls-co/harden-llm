package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-466

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type acquisitionBudgetTrace struct {
	deadline time.Time
	calls    int
	stopErr  error
}

func (trace *acquisitionBudgetTrace) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	trace.deadline, _ = ctx.Deadline()
	trace.calls++
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	trace.stopErr = stopped.Err()
	return stopped
}
func (*acquisitionBudgetTrace) TraceAcquireEnd(context.Context, *pgxpool.Pool, pgxpool.TraceAcquireEndData) {
}
func (*acquisitionBudgetTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (*acquisitionBudgetTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestSchemaAcquisitionBudget(t *testing.T) {
	t.Parallel()
	for _, operation := range []struct {
		name   string
		budget time.Duration
		call   func(context.Context, *pgxpool.Pool) error
	}{
		{"schema initialization", 30 * time.Second, ApplySchema},
		{"request storage", 600 * time.Millisecond, func(ctx context.Context, pool *pgxpool.Pool) error {
			return withStorageTx(ctx, pool, pgx.TxOptions{}, func(context.Context, pgx.Tx) error {
				t.Error("canceled acquisition reached transaction body")
				return nil
			})
		}},
	} {
		for _, parentBounded := range []bool{false, true} {
			name := operation.name + "/own deadline"
			if parentBounded {
				name = operation.name + "/earlier caller deadline"
			}
			t.Run(name, func(t *testing.T) {
				trace := &acquisitionBudgetTrace{}
				cfg, err := pgxpool.ParseConfig("postgres://fixture:fixture@127.0.0.1:1/fixture?sslmode=disable")
				if err != nil {
					t.Fatal(err)
				}
				cfg.ConnConfig.Tracer = trace
				cfg.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
					t.Error("canceled acquisition attempted network I/O")
					return nil, errors.New("unexpected dial")
				}
				pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(pool.Close)
				parent := context.Background()
				callerDeadline := time.Now().Add(100 * time.Millisecond)
				if parentBounded {
					var cancel context.CancelFunc
					parent, cancel = context.WithDeadline(parent, callerDeadline)
					defer cancel()
				}
				start := time.Now()
				err = operation.call(parent, pool)
				end := time.Now()
				if trace.stopErr == nil || !errors.Is(err, trace.stopErr) || trace.calls != 1 {
					t.Fatalf("expected one stopped acquisition preserving its context error: calls=%d error=%v", trace.calls, err)
				}
				if parentBounded {
					if !trace.deadline.Equal(callerDeadline) {
						t.Fatal("operation replaced the earlier caller deadline")
					}
				} else if trace.deadline.Before(start.Add(operation.budget)) || trace.deadline.After(end.Add(operation.budget)) {
					t.Fatalf("acquisition deadline does not use %s budget: offset=%s", operation.budget, trace.deadline.Sub(start))
				}
			})
		}
	}
}
