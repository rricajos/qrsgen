// Package tenant gestiona configuración per-tenant (mapping owner_tag →
// downstream config). Permite que un solo proceso qrsgen sirva varios
// clientes distintos, cada uno con su propio downstream URL/token/account.
//
// Diseño:
//   - Tabla bridge_tenant con (owner_tag PK, downstream_base_url,
//     downstream_api_token, downstream_account_id, downstream_inbox_id).
//   - Resolver mantiene un cache in-memory invalidado en cada Set/Delete.
//   - Si una instancia tiene owner_tag pero no existe tenant para ese tag,
//     o no tiene owner_tag, se usa el fallback global (env DOWNSTREAM_*).
package tenant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config describe la configuración downstream de un tenant concreto.
type Config struct {
	OwnerTag            string    `json:"owner_tag"`
	DownstreamBaseURL   string    `json:"downstream_base_url"`
	DownstreamAPIToken  string    `json:"-"` // nunca se serializa
	DownstreamAccountID int       `json:"downstream_account_id"`
	DownstreamInboxID   int       `json:"downstream_inbox_id,omitempty"`
	WebhookHMACSecret   string    `json:"-"` // nunca se serializa
	// PayloadTemplate es un Go text/template opcional que reescribe el
	// body de POST /messages antes del envío al downstream. Si vacío
	// (default), se usa el payload Chatwoot-shape. Variables disponibles:
	// .Content, .MessageType, .SourceID, .ConversationID, .CreatedAtUnix
	// (int64), .InReplyTo (int). El template debe producir JSON válido.
	// Si el template falla en parse o execute, se loguea warning y se
	// usa el payload default — no se bloquea el msg. Desde v0.65.0.
	PayloadTemplate string    `json:"payload_template,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

// ErrNotFound se devuelve cuando un owner_tag no existe en bridge_tenant.
var ErrNotFound = errors.New("tenant: not found")

// Resolver mantiene un cache in-memory de configs y proporciona lookup
// rápido. Safe for concurrent use.
type Resolver struct {
	pool *pgxpool.Pool

	mu    sync.RWMutex
	cache map[string]*Config
}

// EnsureSchema crea la tabla bridge_tenant + aplica migraciones idempotentes.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_tenant (
			owner_tag             TEXT PRIMARY KEY,
			downstream_base_url   TEXT NOT NULL,
			downstream_api_token  TEXT NOT NULL,
			downstream_account_id INT  NOT NULL DEFAULT 1,
			downstream_inbox_id   INT  NOT NULL DEFAULT 0,
			created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// v0.26 — HMAC secret per-tenant para verificar webhooks entrantes.
		// Si NULL/empty → fallback al WEBHOOK_HMAC_SECRET global del env.
		`ALTER TABLE bridge_tenant ADD COLUMN IF NOT EXISTS webhook_hmac_secret TEXT`,
		// v0.65.0 — payload_template (Go text/template) opcional que
		// reescribe el body de POST messages antes del envío. Si NULL/empty,
		// se usa el shape Chatwoot default. Habilita downstreams con
		// payload distinto sin escribir un adapter completo.
		`ALTER TABLE bridge_tenant ADD COLUMN IF NOT EXISTS payload_template TEXT`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("tenant schema: %w", err)
		}
	}
	return nil
}

// New returns a Resolver with empty cache. Call Warmup to preload.
func New(pool *pgxpool.Pool) *Resolver {
	return &Resolver{
		pool:  pool,
		cache: map[string]*Config{},
	}
}

// Warmup carga todos los tenants existentes al cache. Llamar al boot.
func (r *Resolver) Warmup(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
		SELECT owner_tag, downstream_base_url, downstream_api_token,
		       downstream_account_id, downstream_inbox_id,
		       COALESCE(webhook_hmac_secret, ''),
		       COALESCE(payload_template, ''),
		       created_at, updated_at
		FROM bridge_tenant
	`)
	if err != nil {
		return fmt.Errorf("warmup: %w", err)
	}
	defer rows.Close()
	loaded := map[string]*Config{}
	for rows.Next() {
		var c Config
		if err := rows.Scan(&c.OwnerTag, &c.DownstreamBaseURL, &c.DownstreamAPIToken,
			&c.DownstreamAccountID, &c.DownstreamInboxID,
			&c.WebhookHMACSecret, &c.PayloadTemplate,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		loaded[c.OwnerTag] = &c
	}
	r.mu.Lock()
	r.cache = loaded
	r.mu.Unlock()
	return rows.Err()
}

// Get devuelve la config del tenant. Si está en cache, lectura O(1). Si
// no, hace lookup y cachea. Devuelve ErrNotFound si no existe.
func (r *Resolver) Get(ctx context.Context, ownerTag string) (*Config, error) {
	if ownerTag == "" {
		return nil, ErrNotFound
	}
	r.mu.RLock()
	c, ok := r.cache[ownerTag]
	r.mu.RUnlock()
	if ok {
		return c, nil
	}
	// Cache miss — lookup DB.
	var loaded Config
	err := r.pool.QueryRow(ctx, `
		SELECT owner_tag, downstream_base_url, downstream_api_token,
		       downstream_account_id, downstream_inbox_id,
		       COALESCE(webhook_hmac_secret, ''),
		       COALESCE(payload_template, ''),
		       created_at, updated_at
		FROM bridge_tenant WHERE owner_tag=$1
	`, ownerTag).Scan(&loaded.OwnerTag, &loaded.DownstreamBaseURL,
		&loaded.DownstreamAPIToken, &loaded.DownstreamAccountID, &loaded.DownstreamInboxID,
		&loaded.WebhookHMACSecret, &loaded.PayloadTemplate,
		&loaded.CreatedAt, &loaded.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant %q: %w", ownerTag, err)
	}
	r.mu.Lock()
	r.cache[ownerTag] = &loaded
	r.mu.Unlock()
	return &loaded, nil
}

// Set upsert el tenant. Invalida el cache.
func (r *Resolver) Set(ctx context.Context, c Config) error {
	if c.OwnerTag == "" {
		return errors.New("tenant: owner_tag required")
	}
	if c.DownstreamBaseURL == "" || c.DownstreamAPIToken == "" {
		return errors.New("tenant: downstream_base_url + downstream_api_token required")
	}
	if c.DownstreamAccountID == 0 {
		c.DownstreamAccountID = 1
	}
	// Set = PUT semantic: replace all fields. WebhookHMACSecret vacío en
	// payload PUT borra el HMAC del tenant. Para preservar selectivamente
	// usar Patch.
	var createdAt, updatedAt time.Time
	err := r.pool.QueryRow(ctx, `
		INSERT INTO bridge_tenant (owner_tag, downstream_base_url, downstream_api_token, downstream_account_id, downstream_inbox_id, webhook_hmac_secret, payload_template)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''))
		ON CONFLICT (owner_tag) DO UPDATE SET
			downstream_base_url   = EXCLUDED.downstream_base_url,
			downstream_api_token  = EXCLUDED.downstream_api_token,
			downstream_account_id = EXCLUDED.downstream_account_id,
			downstream_inbox_id   = EXCLUDED.downstream_inbox_id,
			webhook_hmac_secret   = EXCLUDED.webhook_hmac_secret,
			payload_template      = EXCLUDED.payload_template,
			updated_at            = NOW()
		RETURNING created_at, updated_at
	`, c.OwnerTag, c.DownstreamBaseURL, c.DownstreamAPIToken, c.DownstreamAccountID, c.DownstreamInboxID, c.WebhookHMACSecret, c.PayloadTemplate).Scan(&createdAt, &updatedAt)
	if err != nil {
		return fmt.Errorf("upsert tenant: %w", err)
	}
	c.CreatedAt = createdAt
	c.UpdatedAt = updatedAt
	r.mu.Lock()
	cp := c
	r.cache[c.OwnerTag] = &cp
	r.mu.Unlock()
	return nil
}

// Patch hace un update parcial del tenant. Solo se modifican los
// campos del map presentes. Keys aceptadas:
//
//   - "downstream_base_url" (string)
//   - "downstream_api_token" (string)
//   - "downstream_account_id" (int)
//   - "downstream_inbox_id" (int)
//   - "webhook_hmac_secret" (string)
//   - "payload_template" (string, v0.65.0)
//
// Devuelve ErrNotFound si el tenant no existe. Invalida el cache tras un
// update exitoso.
func (r *Resolver) Patch(ctx context.Context, ownerTag string, fields map[string]any) (*Config, error) {
	if ownerTag == "" {
		return nil, errors.New("tenant: owner_tag required")
	}
	if len(fields) == 0 {
		// Sin campos = no-op; devuelve el tenant actual.
		return r.Get(ctx, ownerTag)
	}
	allowed := map[string]bool{
		"downstream_base_url":   true,
		"downstream_api_token":  true,
		"downstream_account_id": true,
		"downstream_inbox_id":   true,
		"webhook_hmac_secret":   true,
		"payload_template":      true,
	}
	var sets []string
	var args []any
	for k, v := range fields {
		if !allowed[k] {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", k, len(args)+1))
		args = append(args, v)
	}
	if len(sets) == 0 {
		return r.Get(ctx, ownerTag)
	}
	// updated_at se refresca siempre que haya cambios
	args = append(args, ownerTag)
	query := fmt.Sprintf(`
		UPDATE bridge_tenant
		SET %s, updated_at = NOW()
		WHERE owner_tag = $%d
		RETURNING owner_tag, downstream_base_url, downstream_api_token,
		          downstream_account_id, downstream_inbox_id,
		          created_at, updated_at,
		          COALESCE(webhook_hmac_secret, ''),
		          COALESCE(payload_template, '')
	`, strings.Join(sets, ", "), len(args))
	var loaded Config
	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&loaded.OwnerTag, &loaded.DownstreamBaseURL, &loaded.DownstreamAPIToken,
		&loaded.DownstreamAccountID, &loaded.DownstreamInboxID,
		&loaded.CreatedAt, &loaded.UpdatedAt,
		&loaded.WebhookHMACSecret, &loaded.PayloadTemplate)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("patch tenant: %w", err)
	}
	r.mu.Lock()
	r.cache[ownerTag] = &loaded
	r.mu.Unlock()
	return &loaded, nil
}

// Delete remueve el tenant. Invalida el cache.
func (r *Resolver) Delete(ctx context.Context, ownerTag string) error {
	res, err := r.pool.Exec(ctx, `DELETE FROM bridge_tenant WHERE owner_tag=$1`, ownerTag)
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	r.mu.Lock()
	delete(r.cache, ownerTag)
	r.mu.Unlock()
	return nil
}

// List devuelve todos los tenants con timestamps. Sin tokens.
func (r *Resolver) List(ctx context.Context) ([]Config, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT owner_tag, downstream_base_url, downstream_account_id,
		       downstream_inbox_id, created_at, updated_at
		FROM bridge_tenant ORDER BY owner_tag
	`)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()
	var out []Config
	for rows.Next() {
		var c Config
		if err := rows.Scan(&c.OwnerTag, &c.DownstreamBaseURL,
			&c.DownstreamAccountID, &c.DownstreamInboxID,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
