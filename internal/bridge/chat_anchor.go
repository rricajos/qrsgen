package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// chatAnchorTracker registra, para cada (instance, chatJID), el
// último msg incoming conocido (WAID + timestamp). Distinto del
// `msg_history` tracker porque:
//
//   - msg_history indexa por SENDER (cada participante de un grupo
//     es un entry distinto). Útil para retroactive name update,
//     que opera sobre msgs del sender.
//   - chat_anchor indexa por CHAT. Útil para history import on-demand
//     que necesita UN anchor por chat (sin importar quién mandó).
//
// Persistido en `bridge_chat_anchor` desde v0.49.0. Sobrevive a
// restarts → el bulk import resuelve anchor desde aquí en vez del
// msg_history (que solo cubre los chats con prefix de grupo).
type chatAnchorTracker struct {
	mu sync.Mutex

	// in-memory cache: key = "instance|chatJID_nonAD"
	data map[string]chatAnchorEntry

	pool   *pgxpool.Pool
	logger *slog.Logger
}

type chatAnchorEntry struct {
	WAID      string
	Timestamp time.Time
}

func newChatAnchorTracker() *chatAnchorTracker {
	return &chatAnchorTracker{data: make(map[string]chatAnchorEntry)}
}

// SetPool habilita persistencia en `bridge_chat_anchor`. logger
// puede ser nil.
func (t *chatAnchorTracker) SetPool(pool *pgxpool.Pool, logger *slog.Logger) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pool = pool
	t.logger = logger
}

// EnsureChatAnchorSchema crea la tabla. Idempotente.
func EnsureChatAnchorSchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_chat_anchor (
			instance   TEXT NOT NULL,
			chat_jid   TEXT NOT NULL,
			waid       TEXT NOT NULL,
			ts         TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (instance, chat_jid)
		)`,
		`CREATE INDEX IF NOT EXISTS bridge_chat_anchor_updated_idx
		 ON bridge_chat_anchor (updated_at DESC)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("chat_anchor schema: %w", err)
		}
	}
	return nil
}

// Warmup carga el cache in-memory desde la DB. Llamar al boot tras
// SetPool. keep limita la edad de las entries cargadas (anchors muy
// viejos no son útiles para BuildHistorySyncRequest).
func (t *chatAnchorTracker) Warmup(ctx context.Context, keep time.Duration) error {
	if t.pool == nil {
		return nil
	}
	rows, err := t.pool.Query(ctx, `
		SELECT instance, chat_jid, waid, ts
		FROM bridge_chat_anchor
		WHERE updated_at > NOW() - $1::interval
	`, fmt.Sprintf("%d seconds", int(keep.Seconds())))
	if err != nil {
		return fmt.Errorf("warmup query: %w", err)
	}
	defer rows.Close()
	loaded := 0
	t.mu.Lock()
	for rows.Next() {
		var inst, jid, waid string
		var ts time.Time
		if err := rows.Scan(&inst, &jid, &waid, &ts); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("scan: %w", err)
		}
		t.data[inst+"|"+jid] = chatAnchorEntry{WAID: waid, Timestamp: ts}
		loaded++
	}
	t.mu.Unlock()
	if err := rows.Err(); err != nil {
		return err
	}
	if t.logger != nil {
		t.logger.Info("chat_anchor warmup done", "loaded", loaded)
	}
	return nil
}

// Record actualiza el anchor de un chat. Si ya hay un entry para
// (instance, chatJID), solo lo reemplaza si el nuevo timestamp es
// más reciente — protege contra reordering de eventos.
func (t *chatAnchorTracker) Record(instance, chatJID, waid string, ts time.Time) {
	if waid == "" || ts.IsZero() {
		return
	}
	key := instance + "|" + chatJID
	t.mu.Lock()
	cur, ok := t.data[key]
	if ok && !ts.After(cur.Timestamp) {
		t.mu.Unlock()
		return
	}
	t.data[key] = chatAnchorEntry{WAID: waid, Timestamp: ts}
	pool := t.pool
	logger := t.logger
	t.mu.Unlock()

	if pool == nil {
		return
	}
	// Persistir async para no bloquear el flujo del mensaje.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, `
			INSERT INTO bridge_chat_anchor (instance, chat_jid, waid, ts)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (instance, chat_jid) DO UPDATE SET
				waid       = EXCLUDED.waid,
				ts         = EXCLUDED.ts,
				updated_at = NOW()
			WHERE bridge_chat_anchor.ts < EXCLUDED.ts
		`, instance, chatJID, waid, ts)
		if err != nil && logger != nil {
			logger.Warn("chat_anchor persist", "err", err,
				"instance", instance, "chat", chatJID)
		}
	}()
}

// Find devuelve el anchor (waid, ts, ok) para un chat. Memory first.
// Si miss in-memory pero pool != nil, fallback a DB.
func (t *chatAnchorTracker) Find(ctx context.Context, instance, chatJID string) (string, time.Time, bool) {
	t.mu.Lock()
	if e, ok := t.data[instance+"|"+chatJID]; ok {
		t.mu.Unlock()
		return e.WAID, e.Timestamp, true
	}
	pool := t.pool
	t.mu.Unlock()
	if pool == nil {
		return "", time.Time{}, false
	}
	var waid string
	var ts time.Time
	err := pool.QueryRow(ctx, `
		SELECT waid, ts FROM bridge_chat_anchor
		WHERE instance = $1 AND chat_jid = $2
	`, instance, chatJID).Scan(&waid, &ts)
	if err != nil {
		return "", time.Time{}, false
	}
	// Hidrate el cache para futuras consultas.
	t.mu.Lock()
	t.data[instance+"|"+chatJID] = chatAnchorEntry{WAID: waid, Timestamp: ts}
	t.mu.Unlock()
	return waid, ts, true
}

// CleanupOld borra anchors más viejos que `keep`.
func (t *chatAnchorTracker) CleanupOld(ctx context.Context, keep time.Duration) (int64, error) {
	if t.pool == nil {
		return 0, nil
	}
	tag, err := t.pool.Exec(ctx,
		`DELETE FROM bridge_chat_anchor WHERE updated_at < NOW() - $1::interval`,
		fmt.Sprintf("%d seconds", int(keep.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
