// Package outbox persists outgoing WhatsApp messages that arrive at qrsgen
// while the target instance is not connected (typically during a restart or
// a transient reconnect). A drainer goroutine flushes the queue as soon as
// the instance becomes connected again.
//
// Messages have a tight TTL (default 5 minutes). If they cannot be delivered
// within that window we mark them "expired" and emit a lifecycle event so
// the integrator can decide what to do (notify the agent, re-post with
// updated text, archive). qrsgen does not retry indefinitely — a stale
// chat message is worse than a missed one.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sender is the interface satisfied by bridge.Outgoing — the outbox calls
// HandleFor to actually send a queued message.
type Sender interface {
	HandleForRaw(ctx context.Context, instance string, raw json.RawMessage) error
}

// Connector tells the drainer whether an instance is currently reachable.
// Manager satisfies this trivially via Get + Conn.IsConnected.
type Connector interface {
	IsConnected(instance string) bool
}

// LifecycleEmitter is the same hook used by banwatch.
type LifecycleEmitter interface {
	EmitLifecycle(name, event string, extras map[string]any)
}

// AuditRecorder is the same hook used by main.go.
type AuditRecorder interface {
	Record(ctx context.Context, actor, action, instance, target string, metadata map[string]any)
}

// Config holds outbox tunables.
type Config struct {
	// TTL is the maximum lifetime of a queued message. Default 5 minutes.
	TTL time.Duration
	// MaxAttempts caps retries per message. Default 5.
	MaxAttempts int
	// DrainInterval is how often the drainer goroutine wakes up to flush
	// pending messages. Default 5 seconds.
	DrainInterval time.Duration
	// ExpireInterval is how often we sweep for expired messages. Default 30s.
	ExpireInterval time.Duration
	// MaxQueueDepth — per-instance hard cap to avoid runaway buffering on a
	// permanently-disconnected instance. New enqueues past this fail fast.
	// Default 200.
	MaxQueueDepth int
}

// DefaultConfig returns sane defaults.
func DefaultConfig() Config {
	return Config{
		TTL:            5 * time.Minute,
		MaxAttempts:    5,
		DrainInterval:  5 * time.Second,
		ExpireInterval: 30 * time.Second,
		MaxQueueDepth:  200,
	}
}

// Outbox is the public type wiring everything together.
type Outbox struct {
	cfg       Config
	pool      *pgxpool.Pool
	sender    Sender
	connector Connector
	emitter   LifecycleEmitter
	audit     AuditRecorder
	logger    *slog.Logger

	// encryptionKey opcional (AES-256). Si len==32, Enqueue cifra los payloads
	// nuevos con AES-GCM y persiste `nonce` + `payload_enc`. DrainOnce/ExpireOnce
	// descifran las filas que tienen nonce no-NULL; las legacy con nonce NULL
	// se entregan tal cual (backward compat).
	encryptionKey []byte

	// metrics hooks — left as plain func to keep the package decoupled from
	// the concrete prometheus types. Set by main.go after construction.
	onEnqueue func(instance string)
	onSent    func(instance string)
	onExpired func(instance string)
	onFailed  func(instance string)
	depthFn   func(instance string, depth int)

	mu sync.Mutex
}

// SetEncryptionKey activa cifrado AES-GCM de payloads nuevos. La key debe
// tener EncryptionKeySize bytes; pasar nil/empty para opt-out.
func (o *Outbox) SetEncryptionKey(key []byte) error {
	if len(key) == 0 {
		o.encryptionKey = nil
		return nil
	}
	if len(key) != EncryptionKeySize {
		return fmt.Errorf("outbox: encryption key must be %d bytes, got %d", EncryptionKeySize, len(key))
	}
	o.encryptionKey = key
	return nil
}

// EnqueueResult is what callers get back when an item lands in the queue.
type EnqueueResult struct {
	QueueID   int64     `json:"queue_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ErrQueueFull is returned by Enqueue when MaxQueueDepth is reached.
var ErrQueueFull = errors.New("outbox queue full for instance")

// EnsureSchema creates bridge_outgoing_queue. Idempotent.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_outgoing_queue (
			id          BIGSERIAL PRIMARY KEY,
			instance    TEXT NOT NULL,
			remote_jid  TEXT NOT NULL,
			payload     JSONB NOT NULL,
			enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at  TIMESTAMPTZ NOT NULL,
			attempts    INT NOT NULL DEFAULT 0,
			last_error  TEXT,
			status      TEXT NOT NULL DEFAULT 'pending',
			sent_at     TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS bridge_outgoing_queue_pending_idx
		 ON bridge_outgoing_queue (instance, id) WHERE status = 'pending'`,
		`CREATE INDEX IF NOT EXISTS bridge_outgoing_queue_expires_idx
		 ON bridge_outgoing_queue (expires_at) WHERE status = 'pending'`,
		// v0.27 — payload encryption opt-in:
		// `nonce IS NULL` → payload (JSONB) en claro (filas legacy, compat).
		// `nonce IS NOT NULL` → payload_enc (bytea ciphertext AES-256-GCM)
		// + nonce (12 bytes); el JSONB payload puede ser NULL.
		`ALTER TABLE bridge_outgoing_queue ADD COLUMN IF NOT EXISTS nonce BYTEA`,
		`ALTER TABLE bridge_outgoing_queue ADD COLUMN IF NOT EXISTS payload_enc BYTEA`,
		// v0.28.1 — permitir NULL en payload cuando la fila está cifrada,
		// en vez del placeholder 'null'::jsonb del v0.27.
		`ALTER TABLE bridge_outgoing_queue ALTER COLUMN payload DROP NOT NULL`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("outbox schema: %w", err)
		}
	}
	return nil
}

// New builds an Outbox. Call Start to launch the drainer.
func New(cfg Config, pool *pgxpool.Pool, sender Sender, connector Connector, emitter LifecycleEmitter, audit AuditRecorder, logger *slog.Logger) *Outbox {
	if cfg.TTL <= 0 {
		cfg.TTL = 5 * time.Minute
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.DrainInterval <= 0 {
		cfg.DrainInterval = 5 * time.Second
	}
	if cfg.ExpireInterval <= 0 {
		cfg.ExpireInterval = 30 * time.Second
	}
	if cfg.MaxQueueDepth <= 0 {
		cfg.MaxQueueDepth = 200
	}
	return &Outbox{
		cfg:       cfg,
		pool:      pool,
		sender:    sender,
		connector: connector,
		emitter:   emitter,
		audit:     audit,
		logger:    logger,
	}
}

// SetMetrics wires per-event hooks. All nil-safe.
func (o *Outbox) SetMetrics(onEnqueue, onSent, onExpired, onFailed func(string), depthFn func(string, int)) {
	o.onEnqueue, o.onSent, o.onExpired, o.onFailed, o.depthFn = onEnqueue, onSent, onExpired, onFailed, depthFn
}

// Enqueue persists a payload for later delivery.
func (o *Outbox) Enqueue(ctx context.Context, instance, remoteJID string, payload json.RawMessage) (EnqueueResult, error) {
	if instance == "" {
		return EnqueueResult{}, errors.New("outbox: missing instance")
	}
	if len(payload) == 0 {
		return EnqueueResult{}, errors.New("outbox: empty payload")
	}

	// Hard cap on per-instance backlog.
	var depth int
	if err := o.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bridge_outgoing_queue WHERE instance=$1 AND status='pending'`,
		instance,
	).Scan(&depth); err != nil {
		return EnqueueResult{}, fmt.Errorf("count pending: %w", err)
	}
	if depth >= o.cfg.MaxQueueDepth {
		return EnqueueResult{}, ErrQueueFull
	}

	expiresAt := time.Now().Add(o.cfg.TTL)
	var id int64
	if len(o.encryptionKey) == EncryptionKeySize {
		// Cifrar: persistimos en payload_enc + nonce. El campo payload
		// (JSONB) queda NULL — desde v0.28.1 la columna lo permite.
		ct, nonce, err := sealPayload(o.encryptionKey, []byte(payload))
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("seal payload: %w", err)
		}
		if err := o.pool.QueryRow(ctx, `
			INSERT INTO bridge_outgoing_queue (instance, remote_jid, payload_enc, nonce, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id
		`, instance, remoteJID, ct, nonce, expiresAt).Scan(&id); err != nil {
			return EnqueueResult{}, fmt.Errorf("insert outbox (encrypted): %w", err)
		}
	} else {
		// Sin key → backward compat (payload JSONB en claro).
		if err := o.pool.QueryRow(ctx, `
			INSERT INTO bridge_outgoing_queue (instance, remote_jid, payload, expires_at)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`, instance, remoteJID, []byte(payload), expiresAt).Scan(&id); err != nil {
			return EnqueueResult{}, fmt.Errorf("insert outbox: %w", err)
		}
	}

	if o.onEnqueue != nil {
		o.onEnqueue(instance)
	}
	if o.depthFn != nil {
		o.depthFn(instance, depth+1)
	}
	if o.audit != nil {
		o.audit.Record(ctx, "system", "outbox.enqueue", instance, remoteJID, map[string]any{"queue_id": id})
	}
	o.logger.Info("outbox enqueued", "instance", instance, "queue_id", id, "expires_at", expiresAt)
	return EnqueueResult{QueueID: id, ExpiresAt: expiresAt}, nil
}

// Start launches the drainer + expirer goroutines. Returns immediately; both
// exit with ctx.
func (o *Outbox) Start(ctx context.Context) {
	go o.drainLoop(ctx)
	go o.expireLoop(ctx)
}

func (o *Outbox) drainLoop(ctx context.Context) {
	t := time.NewTicker(o.cfg.DrainInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := o.DrainOnce(ctx); err != nil {
				o.logger.Warn("outbox drain", "err", err)
			}
		}
	}
}

func (o *Outbox) expireLoop(ctx context.Context) {
	t := time.NewTicker(o.cfg.ExpireInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := o.ExpireOnce(ctx); err != nil {
				o.logger.Warn("outbox expire", "err", err)
			}
		}
	}
}

// DrainOnce delivers all pending items whose target instance is currently
// connected. Items that fail are retried on subsequent ticks; on exhaustion
// (>= MaxAttempts) they get status='failed'.
//
// Exported so integration tests can step the drainer deterministically.
func (o *Outbox) DrainOnce(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	rows, err := o.pool.Query(ctx, `
		SELECT id, instance, remote_jid, payload, payload_enc, nonce, attempts
		FROM bridge_outgoing_queue
		WHERE status='pending'
		ORDER BY id ASC
		LIMIT 50
	`)
	if err != nil {
		return fmt.Errorf("select pending: %w", err)
	}

	type item struct {
		id        int64
		instance  string
		remoteJID string
		payload   []byte
		attempts  int
	}
	var batch []item
	for rows.Next() {
		var it item
		var payloadJSONB, payloadEnc, nonce []byte
		if err := rows.Scan(&it.id, &it.instance, &it.remoteJID, &payloadJSONB, &payloadEnc, &nonce, &it.attempts); err != nil {
			rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		// Si la fila tiene nonce, viene cifrada; sino, payload JSONB es plaintext.
		if len(nonce) > 0 {
			pt, err := openPayload(o.encryptionKey, payloadEnc, nonce)
			if err != nil {
				rows.Close()
				return fmt.Errorf("decrypt outbox id=%d: %w", it.id, err)
			}
			it.payload = pt
		} else {
			it.payload = payloadJSONB
		}
		batch = append(batch, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, it := range batch {
		if o.connector != nil && !o.connector.IsConnected(it.instance) {
			continue // skip silently, try next tick
		}
		err := o.sender.HandleForRaw(ctx, it.instance, it.payload)
		if err == nil {
			if _, err := o.pool.Exec(ctx, `
				UPDATE bridge_outgoing_queue SET status='sent', sent_at=NOW()
				WHERE id=$1
			`, it.id); err != nil {
				o.logger.Warn("mark sent failed", "id", it.id, "err", err)
				continue
			}
			if o.onSent != nil {
				o.onSent(it.instance)
			}
			continue
		}

		newAttempts := it.attempts + 1
		errMsg := truncate(err.Error(), 500)
		if newAttempts >= o.cfg.MaxAttempts {
			if _, uerr := o.pool.Exec(ctx, `
				UPDATE bridge_outgoing_queue
				SET status='failed', attempts=$2, last_error=$3
				WHERE id=$1
			`, it.id, newAttempts, errMsg); uerr != nil {
				o.logger.Warn("mark failed", "id", it.id, "err", uerr)
			}
			if o.onFailed != nil {
				o.onFailed(it.instance)
			}
			o.emitFailed(it.instance, it.id, errMsg)
			continue
		}

		if _, uerr := o.pool.Exec(ctx, `
			UPDATE bridge_outgoing_queue
			SET attempts=$2, last_error=$3
			WHERE id=$1
		`, it.id, newAttempts, errMsg); uerr != nil {
			o.logger.Warn("update attempts", "id", it.id, "err", uerr)
		}
	}
	return nil
}

// ExpireOnce moves pending items past expires_at to status='expired' and
// emits an outgoing_expired lifecycle event with a preview.
func (o *Outbox) ExpireOnce(ctx context.Context) error {
	rows, err := o.pool.Query(ctx, `
		SELECT id, instance, remote_jid, payload, payload_enc, nonce
		FROM bridge_outgoing_queue
		WHERE status='pending' AND expires_at < NOW()
		ORDER BY id ASC
		LIMIT 100
	`)
	if err != nil {
		return fmt.Errorf("select expired: %w", err)
	}
	type item struct {
		id        int64
		instance  string
		remoteJID string
		payload   []byte
	}
	var batch []item
	for rows.Next() {
		var it item
		var payloadJSONB, payloadEnc, nonce []byte
		if err := rows.Scan(&it.id, &it.instance, &it.remoteJID, &payloadJSONB, &payloadEnc, &nonce); err != nil {
			rows.Close()
			return err
		}
		// Para preview necesitamos descifrar si la fila viene cifrada. Si
		// el descifrado falla, dejamos el preview vacío y seguimos — no
		// queremos bloquear la expiración por un error de cripto.
		if len(nonce) > 0 {
			if pt, err := openPayload(o.encryptionKey, payloadEnc, nonce); err == nil {
				it.payload = pt
			} else {
				o.logger.Warn("expire decrypt failed (preview unavailable)", "id", it.id, "err", err)
				it.payload = nil
			}
		} else {
			it.payload = payloadJSONB
		}
		batch = append(batch, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, it := range batch {
		if _, err := o.pool.Exec(ctx,
			`UPDATE bridge_outgoing_queue SET status='expired' WHERE id=$1`, it.id,
		); err != nil {
			o.logger.Warn("mark expired", "id", it.id, "err", err)
			continue
		}
		if o.onExpired != nil {
			o.onExpired(it.instance)
		}
		preview := previewFromPayload(it.payload)
		if o.emitter != nil {
			o.emitter.EmitLifecycle(it.instance, "outgoing_expired", map[string]any{
				"queue_id":   it.id,
				"remote_jid": it.remoteJID,
				"preview":    preview,
			})
		}
		if o.audit != nil {
			o.audit.Record(ctx, "system", "outbox.expire", it.instance, it.remoteJID,
				map[string]any{"queue_id": it.id, "preview": preview})
		}
		o.logger.Warn("outbox message expired", "instance", it.instance, "queue_id", it.id, "remote_jid", it.remoteJID)
	}
	return nil
}

func (o *Outbox) emitFailed(instance string, id int64, lastErr string) {
	if o.audit != nil {
		o.audit.Record(context.Background(), "system", "outbox.failed", instance, "", map[string]any{
			"queue_id":   id,
			"last_error": lastErr,
		})
	}
	o.logger.Error("outbox message failed (attempts exhausted)",
		"instance", instance, "queue_id", id, "last_error", lastErr)
}

// Depth reports the current pending count for an instance. Cheap query —
// safe to call from HTTP handlers if a snapshot is needed.
func (o *Outbox) Depth(ctx context.Context, instance string) (int, error) {
	var n int
	if err := o.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bridge_outgoing_queue WHERE instance=$1 AND status='pending'`,
		instance,
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Stats represents counters useful for ops dashboards.
type Stats struct {
	Pending int `json:"pending"`
	Sent    int `json:"sent"`
	Expired int `json:"expired"`
	Failed  int `json:"failed"`
}

// Stats counts each status for an instance (or globally when instance is "").
func (o *Outbox) Stats(ctx context.Context, instance string) (Stats, error) {
	var rows pgx.Rows
	var err error
	if instance != "" {
		rows, err = o.pool.Query(ctx,
			`SELECT status, COUNT(*) FROM bridge_outgoing_queue WHERE instance=$1 GROUP BY status`, instance)
	} else {
		rows, err = o.pool.Query(ctx,
			`SELECT status, COUNT(*) FROM bridge_outgoing_queue GROUP BY status`)
	}
	if err != nil {
		return Stats{}, fmt.Errorf("stats: %w", err)
	}
	defer rows.Close()
	var s Stats
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return Stats{}, err
		}
		switch status {
		case "pending":
			s.Pending = n
		case "sent":
			s.Sent = n
		case "expired":
			s.Expired = n
		case "failed":
			s.Failed = n
		}
	}
	return s, rows.Err()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// previewFromPayload pulls a 60-char preview of the payload's "content"
// field, if present. Best-effort; returns "" on parse failure.
func previewFromPayload(raw []byte) string {
	var p struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	if len([]rune(p.Content)) <= 60 {
		return p.Content
	}
	r := []rune(p.Content)
	return string(r[:60]) + "…"
}
