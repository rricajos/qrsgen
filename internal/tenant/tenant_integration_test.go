package tenant

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func dsnFromEnv() string { return os.Getenv("INTEGRATION_PG_DSN") }

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
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := EnsureSchema(ctx, pool); err != nil {
			t.Fatalf("EnsureSchema iter %d: %v", i, err)
		}
	}
}

func TestIntegration_SetGetDelete(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, pool); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	r := New(pool)
	const ot = "tenant-test-roundtrip"
	t.Cleanup(func() { _ = r.Delete(ctx, ot) })

	cfg := Config{
		OwnerTag:            ot,
		DownstreamBaseURL:   "http://example.test",
		DownstreamAPIToken:  "tok-secret",
		DownstreamAccountID: 7,
		DownstreamInboxID:   42,
	}
	if err := r.Set(ctx, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := r.Get(ctx, ot)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DownstreamBaseURL != cfg.DownstreamBaseURL || got.DownstreamAccountID != 7 || got.DownstreamInboxID != 42 {
		t.Errorf("Get mismatch: %+v", got)
	}
	if got.DownstreamAPIToken != "tok-secret" {
		t.Errorf("token not persisted")
	}

	// Update via Set (upsert)
	cfg.DownstreamInboxID = 99
	if err := r.Set(ctx, cfg); err != nil {
		t.Fatalf("Set update: %v", err)
	}
	got2, err := r.Get(ctx, ot)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got2.DownstreamInboxID != 99 {
		t.Errorf("update did not propagate: got %d", got2.DownstreamInboxID)
	}

	// Delete + Get → ErrNotFound
	if err := r.Delete(ctx, ot); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, ot); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_GetEmptyOwnerTag(t *testing.T) {
	pool := newIntegrationPool(t)
	r := New(pool)
	if _, err := r.Get(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(\"\") err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_DeleteUnknown(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, pool); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	r := New(pool)
	if err := r.Delete(ctx, "tenant-does-not-exist-xyz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete unknown err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_SetValidation(t *testing.T) {
	pool := newIntegrationPool(t)
	r := New(pool)
	if err := r.Set(context.Background(), Config{}); err == nil {
		t.Error("expected error on empty owner_tag")
	}
	if err := r.Set(context.Background(), Config{OwnerTag: "x"}); err == nil {
		t.Error("expected error on missing downstream url/token")
	}
}

func TestIntegration_WarmupAndList(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	if err := EnsureSchema(ctx, pool); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	r := New(pool)
	const ot1, ot2 = "tenant-warmup-a", "tenant-warmup-b"
	t.Cleanup(func() {
		_ = r.Delete(ctx, ot1)
		_ = r.Delete(ctx, ot2)
	})
	if err := r.Set(ctx, Config{OwnerTag: ot1, DownstreamBaseURL: "http://a.test", DownstreamAPIToken: "ta", DownstreamAccountID: 1}); err != nil {
		t.Fatalf("Set ot1: %v", err)
	}
	if err := r.Set(ctx, Config{OwnerTag: ot2, DownstreamBaseURL: "http://b.test", DownstreamAPIToken: "tb", DownstreamAccountID: 2}); err != nil {
		t.Fatalf("Set ot2: %v", err)
	}

	// Warmup en un nuevo resolver — debe poblar cache desde DB
	fresh := New(pool)
	if err := fresh.Warmup(ctx); err != nil {
		t.Fatalf("Warmup: %v", err)
	}
	got, err := fresh.Get(ctx, ot1)
	if err != nil {
		t.Fatalf("Get after warmup: %v", err)
	}
	if got.DownstreamBaseURL != "http://a.test" {
		t.Errorf("warmup loaded wrong config: %+v", got)
	}

	list, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range list {
		seen[c.OwnerTag] = true
		if c.DownstreamAPIToken != "" {
			t.Errorf("List leaked token for %s", c.OwnerTag)
		}
	}
	if !seen[ot1] || !seen[ot2] {
		t.Errorf("List missing tenants; got %v", seen)
	}
}
