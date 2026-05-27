// Package bridge implementa la lógica de sincronización Downstream <-> WhatsApp.
package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Deduper detecta y descarta:
//   1) El "twin" de un mensaje cuando WhatsApp Server hace dispatch dual
//      (LID + PN del mismo destinatario) — clave por (instance, jid_user, content_normalized).
//   2) Reenvíos del MISMO mensaje saliente del downstream (doble-clic del agente o
//      webhook retry) — vía clave por msg_id (en SeenIncomingMsg).
//
// Limitación de (1): esto solo limpia lo que sincronizamos al downstream. El
// destinatario sigue recibiendo 2 mensajes si WhatsApp ya hizo dispatch dual.
type Deduper struct {
	pool    *pgxpool.Pool
	window  time.Duration
	enabled bool
}

// NewDeduper crea el Deduper. El segundo parámetro (instance) está deprecated:
// la instancia ahora se pasa por llamada para soportar multi-tenant.
func NewDeduper(pool *pgxpool.Pool, _ string, windowMs int, enabled bool) *Deduper {
	return &Deduper{
		pool:    pool,
		window:  time.Duration(windowMs) * time.Millisecond,
		enabled: enabled,
	}
}

// normalizeJid extrae la parte de usuario del JID, sin device suffix ni server.
//   34604021705:92@s.whatsapp.net → 34604021705
//   41961931190522@lid            → 41961931190522
func normalizeJid(jid string) string {
	user := jid
	if i := strings.Index(jid, "@"); i >= 0 {
		user = jid[:i]
	}
	if i := strings.Index(user, ":"); i >= 0 {
		user = user[:i]
	}
	return user
}

// normalizeContent reduce variaciones cosméticas que WhatsApp introduce al
// echo de un mensaje vía LID/PN: distinta cantidad de asteriscos para énfasis,
// case, whitespace. Sin esto, el dispatch dual del mismo mensaje genera dos
// content_hash distintos y dedup no caza.
func normalizeContent(content string) string {
	s := strings.ToLower(content)
	s = strings.NewReplacer("*", "", "_", "", "`", "", "~", "").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

// ShouldDrop devuelve true si este mensaje es duplicado dentro de la ventana.
// Llamar solo para mensajes con FromMe=true (echo outgoing vía multi-device).
//
// Clave (instance, jid_user, content_normalized_hash).
func (d *Deduper) ShouldDrop(ctx context.Context, instance, remoteJid, content string) (bool, error) {
	if !d.enabled {
		return false, nil
	}
	jid := normalizeJid(remoteJid)
	hash := hashContent(normalizeContent(content))
	windowSec := int(d.window.Seconds())
	if windowSec < 1 {
		windowSec = 1
	}

	// Limpieza oportunista
	_, _ = d.pool.Exec(ctx,
		fmt.Sprintf(`DELETE FROM bridge_dedup WHERE seen_at < NOW() - INTERVAL '%d seconds'`, windowSec*6))

	var exists int
	err := d.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT 1 FROM bridge_dedup
		             WHERE instance_name=$1 AND remote_jid=$2 AND content_hash=$3
		               AND seen_at > NOW() - INTERVAL '%d seconds'`, windowSec),
		instance, jid, hash).Scan(&exists)
	if err == nil && exists == 1 {
		return true, nil
	}

	_, err = d.pool.Exec(ctx,
		`INSERT INTO bridge_dedup(instance_name, remote_jid, content_hash)
		 VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`,
		instance, jid, hash)
	if err != nil {
		return false, fmt.Errorf("insert dedup: %w", err)
	}
	return false, nil
}

// SpamguardTracker mantiene el historial de los últimos 2 mensajes salientes
// por (instance, jid_user) y un contador acumulado de bloqueos por instancia.
// Política: si el contenido del nuevo outgoing coincide con CUALQUIERA de los
// 2 hashes guardados → bloquea.
//
// Desde v0.28.0, el estado se persiste en `bridge_spamguard_recent` +
// `bridge_spamguard_counter` (vía SetPool) — sobrevive a restarts. Sin pool,
// el comportamiento sigue siendo in-memory only (compat con tests).
type SpamguardTracker struct {
	mu      sync.Mutex
	history map[string][2]string // key="instance|jidUser" → [latest, prev]
	counter map[string]int       // key=instance → bloqueos acumulados

	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewSpamguardTracker() *SpamguardTracker {
	return &SpamguardTracker{
		history: map[string][2]string{},
		counter: map[string]int{},
	}
}

// SetPool activa persistencia en DB. Llamar a Warmup() después para cargar
// estado existente. logger puede ser nil.
func (t *SpamguardTracker) SetPool(pool *pgxpool.Pool, logger *slog.Logger) {
	t.pool = pool
	t.logger = logger
}

// EnsureSpamguardSchema crea las tablas de persistencia. Idempotente.
func EnsureSpamguardSchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_spamguard_recent (
			instance     TEXT NOT NULL,
			jid_user     TEXT NOT NULL,
			hash_latest  TEXT NOT NULL,
			hash_prev    TEXT NOT NULL DEFAULT '',
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (instance, jid_user)
		)`,
		`CREATE INDEX IF NOT EXISTS bridge_spamguard_recent_updated_idx
		 ON bridge_spamguard_recent (updated_at)`,
		`CREATE TABLE IF NOT EXISTS bridge_spamguard_counter (
			instance     TEXT PRIMARY KEY,
			count        BIGINT NOT NULL DEFAULT 0,
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("spamguard schema: %w", err)
		}
	}
	return nil
}

// Warmup carga el historial reciente y los contadores en memoria. Llamar al
// boot tras SetPool. Solo carga filas con updated_at < 1h (older rows are
// considered stale — la ventana de relevancia es corta para spamguard).
func (t *SpamguardTracker) Warmup(ctx context.Context) error {
	if t.pool == nil {
		return nil
	}
	rows, err := t.pool.Query(ctx, `
		SELECT instance, jid_user, hash_latest, hash_prev
		FROM bridge_spamguard_recent
		WHERE updated_at > NOW() - INTERVAL '1 hour'
	`)
	if err != nil {
		return fmt.Errorf("warmup history: %w", err)
	}
	defer rows.Close()
	t.mu.Lock()
	for rows.Next() {
		var inst, jid, latest, prev string
		if err := rows.Scan(&inst, &jid, &latest, &prev); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("scan history: %w", err)
		}
		t.history[inst+"|"+jid] = [2]string{latest, prev}
	}
	t.mu.Unlock()
	if err := rows.Err(); err != nil {
		return err
	}
	crows, err := t.pool.Query(ctx, `SELECT instance, count FROM bridge_spamguard_counter`)
	if err != nil {
		return fmt.Errorf("warmup counter: %w", err)
	}
	defer crows.Close()
	t.mu.Lock()
	for crows.Next() {
		var inst string
		var n int64
		if err := crows.Scan(&inst, &n); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("scan counter: %w", err)
		}
		t.counter[inst] = int(n)
	}
	t.mu.Unlock()
	return crows.Err()
}

// BlockCount lectura del contador acumulado de bloqueos para una instancia.
func (t *SpamguardTracker) BlockCount(instance string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counter[instance]
}

// CheckAndRecord chequea si content (normalizado) coincide con alguno de los
// últimos 2 envíos para (instance, jid). Si sí → blocked=true e incrementa el
// contador de la instancia. Si no → registra el nuevo hash (shift LRU de 2) y
// devuelve blocked=false. count siempre refleja el total acumulado actual.
func (t *SpamguardTracker) CheckAndRecord(instance, jid, content string) (blocked bool, count int) {
	normalized := normalizeContent(content)
	if normalized == "" {
		// Sin contenido textual → no participa en dedup (los adjuntos puros
		// no se hashean, semántica: "spamguard solo aplica a texto").
		return false, t.counter[instance]
	}
	hash := hashContent(normalized)
	jidUser := normalizeJid(jid)
	key := instance + "|" + jidUser
	t.mu.Lock()
	var newHist [2]string
	var blockedOut bool
	if h, ok := t.history[key]; ok {
		if h[0] == hash || h[1] == hash {
			t.counter[instance]++
			blockedOut = true
			newHist = h // no shift en bloqueo
		} else {
			newHist = [2]string{hash, h[0]}
			t.history[key] = newHist
		}
	} else {
		newHist = [2]string{hash, ""}
		t.history[key] = newHist
	}
	cnt := t.counter[instance]
	t.mu.Unlock()
	// Persistir best-effort. Hot path tolera fallos (in-memory ya correcto).
	if t.pool != nil {
		if blockedOut {
			t.persistCounter(instance, cnt)
		} else {
			t.persistHistory(instance, jidUser, newHist[0], newHist[1])
		}
	}
	return blockedOut, cnt
}

// persistHistory hace UPSERT del par (latest, prev) para (instance, jid_user).
// Best-effort: errores se loggean y se ignoran (en memoria ya es correcto).
func (t *SpamguardTracker) persistHistory(instance, jidUser, latest, prev string) {
	if t.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO bridge_spamguard_recent (instance, jid_user, hash_latest, hash_prev, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (instance, jid_user) DO UPDATE SET
			hash_latest = EXCLUDED.hash_latest,
			hash_prev   = EXCLUDED.hash_prev,
			updated_at  = NOW()
	`, instance, jidUser, latest, prev)
	if err != nil && t.logger != nil {
		t.logger.Warn("spamguard persist history", "instance", instance, "err", err)
	}
}

// persistCounter actualiza el contador acumulado para una instancia.
func (t *SpamguardTracker) persistCounter(instance string, count int) {
	if t.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO bridge_spamguard_counter (instance, count, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (instance) DO UPDATE SET
			count      = EXCLUDED.count,
			updated_at = NOW()
	`, instance, int64(count))
	if err != nil && t.logger != nil {
		t.logger.Warn("spamguard persist counter", "instance", instance, "err", err)
	}
}

// CleanupOldRecent elimina filas más viejas que `keep`. Llamar periódicamente
// (cron) para mantener la tabla acotada. Borrar > 1h es seguro: la ventana de
// dedup spamguard es de 2 últimos mensajes; tras 1h sin mensajes no es
// realista re-disparar el bloqueo.
func (t *SpamguardTracker) CleanupOldRecent(ctx context.Context, keep time.Duration) error {
	if t.pool == nil {
		return nil
	}
	_, err := t.pool.Exec(ctx,
		`DELETE FROM bridge_spamguard_recent WHERE updated_at < NOW() - $1::interval`,
		fmt.Sprintf("%d seconds", int(keep.Seconds())),
	)
	return err
}

// SeenIncomingMsg devuelve true si el msgID ya fue procesado en la
// ventana de dedupe. Si no: lo marca como visto y devuelve false.
// Usado por outgoing.HandleFor para idempotencia ante webhook retry.
func (d *Deduper) SeenIncomingMsg(ctx context.Context, instance string, msgID int) (bool, error) {
	if !d.enabled || msgID <= 0 {
		return false, nil
	}
	windowSec := int(d.window.Seconds())
	if windowSec < 1 {
		windowSec = 1
	}
	// Reutilizamos bridge_dedup con un namespacing prefijo en remote_jid: "_cw_msg".
	// El "hash" guarda el msg_id.
	key := fmt.Sprintf("%d", msgID)
	var exists int
	err := d.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT 1 FROM bridge_dedup
		             WHERE instance_name=$1 AND remote_jid='_cw_msg' AND content_hash=$2
		               AND seen_at > NOW() - INTERVAL '%d seconds'`, windowSec*3),
		instance, key).Scan(&exists)
	if err == nil && exists == 1 {
		return true, nil
	}
	_, err = d.pool.Exec(ctx,
		`INSERT INTO bridge_dedup(instance_name, remote_jid, content_hash)
		 VALUES ($1, '_cw_msg', $2)
		 ON CONFLICT DO NOTHING`,
		instance, key)
	if err != nil {
		return false, fmt.Errorf("insert cw_msg dedup: %w", err)
	}
	return false, nil
}
