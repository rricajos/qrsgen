package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// msgHistoryTracker recuerda los mensajes incoming posteados al downstream,
// con todos los datos necesarios para reconstruir su contenido si la info
// del sender (nombre / saved status) cambia más tarde.
//
// Permite el feature de retroactive name update (v0.40.0): cuando el dueño
// del bot añade un contacto a su agenda de WhatsApp tras haber recibido
// mensajes de él, qrsgen puede ir y reescribir los mensajes ya posteados
// para que el nuevo nombre / sin tilde aparezca también en el histórico.
//
// Desde v0.41.0: si se invoca SetPool, el estado se persiste en
// `bridge_msg_history` y sobrevive a restarts. Sin pool (e.g. en tests),
// el comportamiento sigue siendo in-memory only.
type msgHistoryTracker struct {
	mu sync.Mutex

	// Cap por sender — los más viejos se descartan FIFO.
	capPerSender int

	// data: key = instance + "|" + senderJID (no-AD)
	data map[string][]trackedMsg

	pool   *pgxpool.Pool
	logger *slog.Logger
}

// trackedMsg captura toda la info necesaria para reconstruir el content
// del mensaje cuando el sender cambia de saved-status o de nombre,
// y para soportar reply-to outgoing (v0.44.0 mapeo Chatwoot↔WhatsApp).
type trackedMsg struct {
	convID    int       // conversation_id en el downstream
	msgID     int       // message_id en el downstream
	phone     string    // teléfono formateado (E.164 con +) usado en el header
	nameUsed  string    // nombre usado al postear
	wasSaved  bool      // saved status al postear
	body      string    // body del mensaje (sin el prefix de header)
	postedAt  time.Time // timestamp del post
	waid      string    // v0.44.0: WAID (WhatsApp message id) — para reply-to outgoing
	hasPrefix bool      // v0.44.0: true si el msg se posteó con prefix de grupo (afecta retroactive update)
}

func newMsgHistoryTracker(capPerSender int) *msgHistoryTracker {
	if capPerSender <= 0 {
		capPerSender = 100
	}
	return &msgHistoryTracker{
		capPerSender: capPerSender,
		data:         make(map[string][]trackedMsg),
	}
}

// SetPool activa persistencia en DB. Llamar a Warmup() después para
// cargar estado existente. logger puede ser nil.
func (t *msgHistoryTracker) SetPool(pool *pgxpool.Pool, logger *slog.Logger) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pool = pool
	t.logger = logger
}

// EnsureMsgHistorySchema crea la tabla `bridge_msg_history` y aplica
// migraciones incrementales. Idempotente. Llamar al boot antes de
// procesar mensajes.
//
// Migraciones:
//   - v0.41.0: tabla inicial
//   - v0.44.0: ADD COLUMN waid TEXT, has_prefix BOOLEAN (default TRUE
//     para preservar el comportamiento de rows pre-v0.44.0 — todas
//     eran prefix rows en v0.40-v0.43)
func EnsureMsgHistorySchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_msg_history (
			instance    TEXT NOT NULL,
			sender_jid  TEXT NOT NULL,
			conv_id     INT NOT NULL,
			msg_id      INT NOT NULL,
			phone       TEXT NOT NULL DEFAULT '',
			name_used   TEXT NOT NULL DEFAULT '',
			was_saved   BOOLEAN NOT NULL DEFAULT FALSE,
			body        TEXT NOT NULL DEFAULT '',
			posted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (instance, msg_id)
		)`,
		`CREATE INDEX IF NOT EXISTS bridge_msg_history_sender_idx
		 ON bridge_msg_history (instance, sender_jid, posted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS bridge_msg_history_posted_idx
		 ON bridge_msg_history (posted_at)`,
		// v0.44.0 migrations.
		`ALTER TABLE bridge_msg_history ADD COLUMN IF NOT EXISTS waid TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE bridge_msg_history ADD COLUMN IF NOT EXISTS has_prefix BOOLEAN NOT NULL DEFAULT TRUE`,
		`CREATE INDEX IF NOT EXISTS bridge_msg_history_waid_idx
		 ON bridge_msg_history (instance, msg_id)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("msg_history schema: %w", err)
		}
	}
	return nil
}

// Warmup carga el histórico tracked desde DB a memoria. Llamar al
// boot tras SetPool. Solo carga filas con posted_at > NOW() - keep
// (entries más viejas se ignoran — el retroactive update probablemente
// ya no es valioso para mensajes muy antiguos).
//
// Respeta el cap per sender al cargar — si una sender tiene más
// entries en DB que el cap, carga las más recientes.
func (t *msgHistoryTracker) Warmup(ctx context.Context, keep time.Duration) error {
	if t.pool == nil {
		return nil
	}
	rows, err := t.pool.Query(ctx, `
		SELECT instance, sender_jid, conv_id, msg_id, phone, name_used, was_saved, body, posted_at, waid, has_prefix
		FROM bridge_msg_history
		WHERE posted_at > NOW() - $1::interval
		ORDER BY posted_at ASC
	`, fmt.Sprintf("%d seconds", int(keep.Seconds())))
	if err != nil {
		return fmt.Errorf("warmup query: %w", err)
	}
	defer rows.Close()
	loaded := 0
	t.mu.Lock()
	for rows.Next() {
		var inst, jid string
		var m trackedMsg
		if err := rows.Scan(&inst, &jid, &m.convID, &m.msgID, &m.phone, &m.nameUsed, &m.wasSaved, &m.body, &m.postedAt, &m.waid, &m.hasPrefix); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("scan: %w", err)
		}
		key := inst + "|" + jid
		entries := t.data[key]
		entries = append(entries, m)
		if len(entries) > t.capPerSender {
			entries = entries[len(entries)-t.capPerSender:]
		}
		t.data[key] = entries
		loaded++
	}
	t.mu.Unlock()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	if t.logger != nil {
		t.logger.Info("msg_history warmup done", "loaded", loaded)
	}
	return nil
}

// Record persiste un mensaje en el tracker. Llamar tras un PostMessage
// exitoso. El cap por sender se enforce con FIFO — los más viejos caen
// primero al desbordar. Si hay pool, también escribe a DB (la cleanup
// cron borra las viejas en DB; el cap in-memory es solo para limitar
// el footprint en memoria por sender).
func (t *msgHistoryTracker) Record(instance, senderJID string, m trackedMsg) {
	t.mu.Lock()
	key := instance + "|" + senderJID
	entries := t.data[key]
	entries = append(entries, m)
	if len(entries) > t.capPerSender {
		entries = entries[len(entries)-t.capPerSender:]
	}
	t.data[key] = entries
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
			INSERT INTO bridge_msg_history
				(instance, sender_jid, conv_id, msg_id, phone, name_used, was_saved, body, posted_at, waid, has_prefix)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (instance, msg_id) DO UPDATE SET
				sender_jid = EXCLUDED.sender_jid,
				conv_id    = EXCLUDED.conv_id,
				phone      = EXCLUDED.phone,
				name_used  = EXCLUDED.name_used,
				was_saved  = EXCLUDED.was_saved,
				body       = EXCLUDED.body,
				posted_at  = EXCLUDED.posted_at,
				waid       = EXCLUDED.waid,
				has_prefix = EXCLUDED.has_prefix
		`, instance, senderJID, m.convID, m.msgID, m.phone, m.nameUsed, m.wasSaved, m.body, m.postedAt, m.waid, m.hasPrefix)
		if err != nil && logger != nil {
			logger.Warn("msg_history persist record",
				"err", err, "instance", instance, "msg_id", m.msgID)
		}
	}()
}

// ListBySender devuelve una copia de los mensajes tracked para un sender.
// Útil para el retroactive update: iteramos esta lista y por cada uno
// chequeamos si el name actual difiere del nameUsed al postear.
func (t *msgHistoryTracker) ListBySender(instance, senderJID string) []trackedMsg {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + senderJID
	entries := t.data[key]
	if len(entries) == 0 {
		return nil
	}
	out := make([]trackedMsg, len(entries))
	copy(out, entries)
	return out
}

// UpdateAfterPatch actualiza el nameUsed/wasSaved/etc de una entry tras
// haberla reescrito via PATCH en el downstream. Mantiene el tracker
// coherente para que un cambio futuro (otra rename) detecte la diff
// contra el nombre nuevo, no el antiguo.
func (t *msgHistoryTracker) UpdateAfterPatch(instance, senderJID string, msgID int, newName string, newSaved bool) {
	t.mu.Lock()
	key := instance + "|" + senderJID
	entries := t.data[key]
	for i, e := range entries {
		if e.msgID == msgID {
			entries[i].nameUsed = newName
			entries[i].wasSaved = newSaved
			break
		}
	}
	t.data[key] = entries
	pool := t.pool
	logger := t.logger
	t.mu.Unlock()

	if pool == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, `
			UPDATE bridge_msg_history
			SET name_used = $1, was_saved = $2
			WHERE instance = $3 AND msg_id = $4
		`, newName, newSaved, instance, msgID)
		if err != nil && logger != nil {
			logger.Warn("msg_history persist update",
				"err", err, "instance", instance, "msg_id", msgID)
		}
	}()
}

// FindByChatwootMsgID busca un trackedMsg por su Chatwoot msgID
// (busca primero in-memory iterando por sender; si pool != nil
// y no se encuentra, hace fallback SELECT). Devuelve también el
// senderJID (key del tracker, útil para reply-to outgoing en
// grupos donde el ContextInfo necesita Participant). Usado por
// reply-to outgoing (v0.44.0).
func (t *msgHistoryTracker) FindByChatwootMsgID(ctx context.Context, instance string, msgID int) (trackedMsg, string, bool) {
	t.mu.Lock()
	prefix := instance + "|"
	for key, entries := range t.data {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, e := range entries {
			if e.msgID == msgID {
				sender := strings.TrimPrefix(key, prefix)
				t.mu.Unlock()
				return e, sender, true
			}
		}
	}
	pool := t.pool
	t.mu.Unlock()

	if pool == nil {
		return trackedMsg{}, "", false
	}
	var m trackedMsg
	var sender string
	err := pool.QueryRow(ctx, `
		SELECT sender_jid, conv_id, msg_id, phone, name_used, was_saved, body, posted_at, waid, has_prefix
		FROM bridge_msg_history
		WHERE instance = $1 AND msg_id = $2
		LIMIT 1
	`, instance, msgID).Scan(&sender, &m.convID, &m.msgID, &m.phone, &m.nameUsed, &m.wasSaved, &m.body, &m.postedAt, &m.waid, &m.hasPrefix)
	if err != nil {
		return trackedMsg{}, "", false
	}
	return m, sender, true
}

// FindLastForChat busca el msg más reciente tracked para un chat
// concreto (instance + chatJID). Para 1:1, el chat coincide con el
// senderJID. Para grupos, buscamos cualquier msg del grupo (los
// senders son los participantes). Usado por history import on-demand
// (v0.46.2) para tener un anchor real para BuildHistorySyncRequest.
//
// Devuelve (waid, timestamp, ok). ok=false si no hay msg tracked.
func (t *msgHistoryTracker) FindLastForChat(ctx context.Context, instance, chatJID string) (string, time.Time, bool) {
	t.mu.Lock()
	prefix := instance + "|"
	var best trackedMsg
	found := false
	for key, entries := range t.data {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		senderJID := strings.TrimPrefix(key, prefix)
		// 1:1: senderJID == chatJID. Grupos: chatJID viene como
		// `<groupid>@g.us` y los entries están por participante. Sin
		// indexar por chat directamente, el msg_history tracker no
		// distingue chats de grupos — caemos al fallback DB.
		if senderJID != chatJID {
			continue
		}
		for _, e := range entries {
			if !found || e.postedAt.After(best.postedAt) {
				best = e
				found = true
			}
		}
	}
	pool := t.pool
	t.mu.Unlock()
	if found && best.waid != "" {
		// Para timestamp usamos posted_at como aproximación al ts
		// del msg en WA. En la práctica son casi iguales (postedAt =
		// poco después de la llegada del msg WA).
		return best.waid, best.postedAt, true
	}
	if pool == nil {
		return "", time.Time{}, false
	}
	// Fallback DB: el cache in-memory puede no tenerlo (chat tracked
	// pero ya evictado), o ser un grupo donde sender != chat. Buscar
	// en la tabla por sender_jid = chatJID (1:1) Y limit 1 desc.
	// Para grupos, en una fase futura podríamos añadir un índice
	// alternativo. v0.46.2 solo cubre 1:1 fiable.
	var waid string
	var postedAt time.Time
	err := pool.QueryRow(ctx, `
		SELECT waid, posted_at
		FROM bridge_msg_history
		WHERE instance = $1 AND sender_jid = $2 AND waid <> ''
		ORDER BY posted_at DESC
		LIMIT 1
	`, instance, chatJID).Scan(&waid, &postedAt)
	if err != nil {
		return "", time.Time{}, false
	}
	return waid, postedAt, true
}

// CleanupOld borra entries más viejas que `keep` de la tabla DB.
// Las entries in-memory NO se tocan (caen via cap FIFO). Llamar
// periódicamente (cron) para mantener la tabla acotada.
func (t *msgHistoryTracker) CleanupOld(ctx context.Context, keep time.Duration) (int64, error) {
	if t.pool == nil {
		return 0, nil
	}
	tag, err := t.pool.Exec(ctx,
		`DELETE FROM bridge_msg_history WHERE posted_at < NOW() - $1::interval`,
		fmt.Sprintf("%d seconds", int(keep.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
