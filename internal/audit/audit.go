// Package audit persists an append-only log of high-value operations on
// qrsgen: instance provisioning, configuration changes, deletions, and
// security-relevant lifecycle events.
//
// The underlying table has triggers that REJECT every UPDATE/DELETE so the
// row history is tamper-evident at the database level — a compromised app
// cannot rewrite the log without DBA privileges.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is the public read shape.
type Entry struct {
	ID       int64          `json:"id"`
	TS       time.Time      `json:"ts"`
	Instance string         `json:"instance,omitempty"`
	Actor    string         `json:"actor"`
	Action   string         `json:"action"`
	Target   string         `json:"target,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Logger writes audit entries. Use New + EnsureSchema during bootstrap.
type Logger struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New returns a Logger. pool must be a working pgxpool.
func New(pool *pgxpool.Pool, logger *slog.Logger) *Logger {
	return &Logger{pool: pool, logger: logger}
}

// EnsureSchema creates the bridge_audit_log table and the trigger that
// rejects UPDATE/DELETE on it. Idempotent.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_audit_log (
			id        BIGSERIAL PRIMARY KEY,
			ts        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			instance  TEXT,
			actor     TEXT NOT NULL,
			action    TEXT NOT NULL,
			target    TEXT,
			metadata  JSONB
		)`,
		`CREATE INDEX IF NOT EXISTS bridge_audit_log_ts_idx ON bridge_audit_log (ts DESC)`,
		`CREATE INDEX IF NOT EXISTS bridge_audit_log_instance_idx ON bridge_audit_log (instance)`,
		`CREATE INDEX IF NOT EXISTS bridge_audit_log_action_idx ON bridge_audit_log (action)`,
		`CREATE OR REPLACE FUNCTION bridge_audit_log_reject()
		 RETURNS TRIGGER LANGUAGE plpgsql AS $$
		 BEGIN
			 RAISE EXCEPTION 'bridge_audit_log is append-only; UPDATE/DELETE forbidden';
		 END;
		 $$`,
		`DROP TRIGGER IF EXISTS bridge_audit_log_no_update ON bridge_audit_log`,
		`DROP TRIGGER IF EXISTS bridge_audit_log_no_delete ON bridge_audit_log`,
		`CREATE TRIGGER bridge_audit_log_no_update
		 BEFORE UPDATE ON bridge_audit_log
		 FOR EACH ROW EXECUTE FUNCTION bridge_audit_log_reject()`,
		`CREATE TRIGGER bridge_audit_log_no_delete
		 BEFORE DELETE ON bridge_audit_log
		 FOR EACH ROW EXECUTE FUNCTION bridge_audit_log_reject()`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("audit schema: %w", err)
		}
	}
	return nil
}

// Record inserts an audit entry. Best-effort: if it fails (network, DB
// down, etc.) the caller still proceeds — the operation must not be blocked
// by audit-log unavailability. The error is logged for ops.
//
// actor is "api" for unauthenticated calls, "api:<token-tag>" if you adopt
// per-token tagging, or "system" for internal events.
func (l *Logger) Record(ctx context.Context, actor, action, instance, target string, metadata map[string]any) {
	if l == nil || l.pool == nil {
		return
	}
	var metaJSON []byte
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err == nil {
			metaJSON = b
		}
	}
	if _, err := l.pool.Exec(ctx, `
		INSERT INTO bridge_audit_log (actor, action, instance, target, metadata)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5)
	`, actor, action, instance, target, metaJSON); err != nil {
		l.logger.Warn("audit.Record failed (operation proceeded)", "err", err, "action", action, "instance", instance)
	}
}

// Query reads recent entries, optionally filtered by instance. limit is
// capped at 500 to keep responses small; pagination via since is left for
// callers that need it.
func (l *Logger) Query(ctx context.Context, instance string, limit int) ([]Entry, error) {
	if l == nil || l.pool == nil {
		return nil, fmt.Errorf("audit logger not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows = l.pool.Query
	const base = `
		SELECT id, ts, COALESCE(instance,''), actor, action, COALESCE(target,''), metadata
		FROM bridge_audit_log
	`
	var (
		query string
		args  []any
	)
	if instance != "" {
		query = base + ` WHERE instance=$1 ORDER BY id DESC LIMIT $2`
		args = []any{instance, limit}
	} else {
		query = base + ` ORDER BY id DESC LIMIT $1`
		args = []any{limit}
	}
	r, err := rows(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit: %w", err)
	}
	defer r.Close()

	out := make([]Entry, 0, limit)
	for r.Next() {
		var e Entry
		var metaJSON []byte
		if err := r.Scan(&e.ID, &e.TS, &e.Instance, &e.Actor, &e.Action, &e.Target, &metaJSON); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &e.Metadata)
		}
		out = append(out, e)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
