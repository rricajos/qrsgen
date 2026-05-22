// Package bridge implementa la lógica de sincronización Downstream <-> WhatsApp.
package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// SpamguardTracker mantiene en memoria el historial de los últimos 2 mensajes
// salientes por (instance, jid_user) y un contador acumulado de bloqueos por
// instancia. Política: si el contenido del nuevo outgoing coincide con
// CUALQUIERA de los 2 hashes guardados → bloquea.
//
// Lifetime: in-memory; se resetea al reiniciar qrsgen. El contador (n) sirve
// como "número de bloqueos en esta sesión" visible al agente vía evento.
type SpamguardTracker struct {
	mu      sync.Mutex
	history map[string][2]string // key="instance|jidUser" → [latest, prev]
	counter map[string]int       // key=instance → bloqueos acumulados
}

func NewSpamguardTracker() *SpamguardTracker {
	return &SpamguardTracker{
		history: map[string][2]string{},
		counter: map[string]int{},
	}
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
	defer t.mu.Unlock()
	if h, ok := t.history[key]; ok {
		if h[0] == hash || h[1] == hash {
			t.counter[instance]++
			return true, t.counter[instance]
		}
		t.history[key] = [2]string{hash, h[0]}
	} else {
		t.history[key] = [2]string{hash, ""}
	}
	return false, t.counter[instance]
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
