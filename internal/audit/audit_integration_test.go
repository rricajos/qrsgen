package audit

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dsnFromEnv returns a Postgres DSN if INTEGRATION_PG_DSN is set, otherwise "".
// Tests that need a real database skip when it's missing — CI can opt in.
func dsnFromEnv() string {
	return os.Getenv("INTEGRATION_PG_DSN")
}

func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := dsnFromEnv()
	if dsn == "" {
		t.Skip("INTEGRATION_PG_DSN not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_EnsureSchemaIdempotent(t *testing.T) {
	pool := newIntegrationPool(t)
	for i := 0; i < 3; i++ {
		if err := EnsureSchema(context.Background(), pool); err != nil {
			t.Fatalf("EnsureSchema iter %d: %v", i, err)
		}
	}
}

func TestIntegration_RecordAndQuery(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, pool); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	l := New(pool, slog.New(slog.DiscardHandler))

	const instance = "audit-integration-test"
	// Clean slate for this instance only (the trigger will block DELETE on
	// real entries; we use it precisely to confirm that — but for setup we
	// rely on a tagged metadata marker so other tests aren't affected).
	l.Record(ctx, "test", "smoke", instance, "tgt", map[string]any{"k": "v"})

	entries, err := l.Query(ctx, instance, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	got := entries[0]
	if got.Instance != instance || got.Actor != "test" || got.Action != "smoke" || got.Target != "tgt" {
		t.Errorf("entry mismatch: %+v", got)
	}
	if got.Metadata["k"] != "v" {
		t.Errorf("metadata = %+v, want k=v", got.Metadata)
	}
}

func TestIntegration_UpdateDeleteRejected(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, pool); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	l := New(pool, slog.New(slog.DiscardHandler))
	l.Record(ctx, "test", "immut.test", "audit-im-test", "", nil)

	if _, err := pool.Exec(ctx, `UPDATE bridge_audit_log SET actor='nope' WHERE action='immut.test'`); err == nil {
		t.Error("UPDATE on bridge_audit_log must be rejected")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE error not from trigger: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM bridge_audit_log WHERE action='immut.test'`); err == nil {
		t.Error("DELETE on bridge_audit_log must be rejected")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("DELETE error not from trigger: %v", err)
	}
}
