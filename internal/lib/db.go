package lib

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pgxpool ping: %w", err)
	}
	return pool, nil
}

// EnsureBridgeSchema crea las tablas propias de qrsgen (no las de whatsmeow,
// que las gestiona el propio sqlstore.Container).
func EnsureBridgeSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS bridge_dedup (
			instance_name TEXT NOT NULL,
			remote_jid TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (instance_name, remote_jid, content_hash)
		);
		CREATE INDEX IF NOT EXISTS bridge_dedup_seen_idx ON bridge_dedup (seen_at);
	`)
	return err
}
