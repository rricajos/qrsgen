// Package manager gestiona el ciclo de vida de múltiples instancias whatsmeow.
package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

var ErrNotFound = errors.New("instance not found")

type Manager struct {
	mu        sync.RWMutex
	container *sqlstore.Container
	instances map[string]*wameow.Conn
	pool      *pgxpool.Pool
	logger    *slog.Logger
	onMsg     wameow.MessageHandler

	// waiters: suscripciones a "esta instancia está ready". Cada canal
	// recibe una señal cuando la instancia transiciona a ready y se cierra.
	waitersMu sync.Mutex
	waiters   map[string][]chan struct{}

	// disconnectNotified: per-instance flag — true si ya emitimos
	// EventDisconnected post-grace. El próximo EventConnected debe emitirse
	// como EventReconnected en su lugar, y limpiar el flag.
	// pendingReconnected: per-instance flag — true entre el Connected y la
	// confirmación a los 5s de estabilidad antes de emitir EventReconnected.
	// Si llega un Disconnected en esos 5s, se cancela (probable flap).
	reconMu            sync.Mutex
	disconnectNotified map[string]bool
	pendingReconnected map[string]bool

	// pendingUnreachable: per-instance flag — true entre un Disconnected y
	// la decisión final de emitir o no `unreachable` (grace 60s).
	// Si Connected llega antes de que se emita, blip silencioso: ni
	// `unreachable`, ni `reconnected`. Mantiene el panel limpio cuando
	// hay micro-blips de red (lo normal).
	unreachMu          sync.Mutex
	pendingUnreachable map[string]bool

	// connectedEmitted: per-instance flag — true si ya emitimos el evento
	// `connected` para la sesión actual. Evita el bug de "🎉 espurio"
	// cuando whatsmeow fires EventConnected múltiples veces sin un
	// EventDisconnected intermedio (re-handshake, session renewal).
	// Se limpia en disconnect / logged_out / al emitir unreachable.
	connEmitMu       sync.Mutex
	connectedEmitted map[string]bool

	// bootstrapWindowUntil: durante el arranque suprimimos los webhooks de
	// Connected/Reconnected (ruido — n8n los renderizaría como pills por inbox
	// cada vez que arranca qrsgen). En su lugar emitimos UN backend_started
	// por instancia desde main.go cuando Bootstrap termina.
	bootstrapWindowUntil time.Time

	// usage es el contador opcional. Si nil, los lifecycle events no se cuentan.
	usage UsageRecorder

	// audit es el logger opcional para eventos lifecycle. Si nil, no se
	// registran transiciones (paired / logged_out / strike) en el audit log.
	audit AuditRecorder

	// ownerTagResolver es opcional. Si nil, las métricas con label
	// `owner_tag` lo dejan vacío. Inyectado en main.go con el Registry.
	ownerTagResolver OwnerTagResolver

	// version es el tag del binario qrsgen (inyectado vía SetVersion desde
	// main). Se incluye en cada payload lifecycle para que el orquestador
	// pueda mostrar "QRsGEN vX.X.X" en sus mensajes. Si vacío, no se añade.
	version string
}

// SetVersion attaches the running qrsgen version to lifecycle event payloads.
// Pass "" to omit. Default is empty (events go out without the field).
func (m *Manager) SetVersion(v string) { m.version = v }

// UsageRecorder es la interfaz mínima que el manager usa para incrementar
// contadores de lifecycle events. Inyectable vía SetUsage; nil → no-op.
type UsageRecorder interface {
	IncLifecycle(instance string)
}

// SetUsage attaches a usage recorder. Pass nil to disable.
func (m *Manager) SetUsage(u UsageRecorder) { m.usage = u }

// AuditRecorder es la interfaz mínima que el manager usa para registrar
// eventos forenses (paired, logged_out). Inyectable vía SetAudit; nil → no-op.
type AuditRecorder interface {
	Record(ctx context.Context, actor, action, instance, target string, metadata map[string]any)
}

// SetAudit attaches an audit recorder. Pass nil to disable.
func (m *Manager) SetAudit(a AuditRecorder) { m.audit = a }

// OwnerTagResolver es la interfaz mínima que el manager usa para obtener
// el owner_tag de una instancia (label per-tenant en métricas Prometheus).
// Implementada por `downstream.Router` (Registry + Client). Inyectable
// vía SetOwnerTagResolver; si nil, el label sale como "".
type OwnerTagResolver interface {
	OwnerTagFor(ctx context.Context, instance string) string
}

// SetOwnerTagResolver attaches a resolver for owner_tag lookups. Pass nil to disable.
func (m *Manager) SetOwnerTagResolver(r OwnerTagResolver) { m.ownerTagResolver = r }

// ownerTag devuelve el owner_tag o "" si no hay resolver / instancia.
func (m *Manager) ownerTag(name string) string {
	if m.ownerTagResolver == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return m.ownerTagResolver.OwnerTagFor(ctx, name)
}

func New(ctx context.Context, dsn string, pool *pgxpool.Pool, logger *slog.Logger, onMsg wameow.MessageHandler) (*Manager, error) {
	container, err := wameow.NewContainer(ctx, dsn)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		container:          container,
		instances:          map[string]*wameow.Conn{},
		pool:               pool,
		logger:             logger,
		onMsg:              onMsg,
		waiters:            map[string][]chan struct{}{},
		disconnectNotified: map[string]bool{},
		pendingReconnected: map[string]bool{},
		pendingUnreachable: map[string]bool{},
		connectedEmitted:   map[string]bool{},
	}
	return m, nil
}

// EnsureSchema crea la tabla bridge_instance y aplica migraciones incrementales.
// Cada ALTER es idempotente: si la columna ya existe, no-op.
func (m *Manager) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bridge_instance (
			name TEXT PRIMARY KEY,
			jid TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// migrations v0.8 — state machine timestamps
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS paired_at TIMESTAMPTZ`,
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS ready_at TIMESTAMPTZ`,
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ`,
		// migrations v0.14 — events webhook callback for lifecycle notifications
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS events_webhook_url TEXT`,
		// migrations v0.15 — per-instance inbox_id (multi-tenant routing)
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS inbox_id INT`,
		// migrations v0.18 — spamguard config + reconnect tracking
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS spamguard_enabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS spamguard_window_ms INT NOT NULL DEFAULT 30000`,
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS spamguard_min_chars INT NOT NULL DEFAULT 10`,
		// migrations v0.19 — QR auto-refresh: id del mensaje del downstream que tiene
		// el último PNG posteado, para que el notifier lo borre antes de postear
		// el siguiente cuando whatsmeow rota el código.
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS last_qr_msg_id INT`,
		// migrations v0.23 — owner_tag opcional para que el integrador correlacione
		// instancias con su modelo de tenants (string libre, qrsgen no lo interpreta).
		`ALTER TABLE bridge_instance ADD COLUMN IF NOT EXISTS owner_tag TEXT`,
	}
	for _, s := range stmts {
		if _, err := m.pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("schema: %w (stmt=%s)", err, s)
		}
	}
	return nil
}

// Bootstrap carga todas las instancias persistidas y arranca su conexión.
// Se llama una vez al inicio del servicio.
func (m *Manager) Bootstrap(ctx context.Context) error {
	rows, err := m.pool.Query(ctx, `SELECT name, COALESCE(jid, '') FROM bridge_instance`)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()

	type entry struct{ name, jid string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.jid); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Marcamos ventana de bootstrap: durante 15s suprimimos los webhooks de
	// Connected (la avalancha por reconectar todas las instancias). Después
	// emitiremos un único backend_started por instancia.
	m.bootstrapWindowUntil = time.Now().Add(15 * time.Second)

	for _, e := range entries {
		if _, err := m.startLocked(ctx, e.name, e.jid); err != nil {
			m.logger.Error("bootstrap instance failed", "name", e.name, "err", err)
		}
	}
	return nil
}

// inBootstrapWindow indica si estamos dentro de la ventana de arranque donde
// los webhooks de Connected se suprimen.
func (m *Manager) inBootstrapWindow() bool {
	return !m.bootstrapWindowUntil.IsZero() && time.Now().Before(m.bootstrapWindowUntil)
}

// CreateOpts permite personalizar la creación de una instancia.
type CreateOpts struct {
	// EventsWebhookURL: si no es vacío, qrsgen POSTea un payload a esta URL
	// en cada transición lifecycle (paired/connected/disconnected/logged_out).
	// Si es nil → se mantiene el valor previo en DB (no se sobrescribe).
	// Si es "" explícito → no se notifica (se borra el valor previo).
	EventsWebhookURL *string
	// InboxID: id del inbox del downstream al que pertenece esta instancia.
	// Si nil → se mantiene el valor previo en DB.
	// Si 0 explícito → se borra el valor previo.
	InboxID *int
	// OwnerTag: string libre que el integrador usa para correlacionar la
	// instancia con su modelo de tenants/facturación. qrsgen no lo interpreta.
	// Si nil → mantiene valor previo. Si "" explícito → borra.
	OwnerTag *string
}

// Create crea (o reusa) la instancia con el name dado y arranca su conexión.
// Si ya existe pero está desconectada y sin JID (QR caducado, primer pareado
// fallido), la rearranca para regenerar el canal QR.
func (m *Manager) Create(ctx context.Context, name string) (*wameow.Conn, error) {
	return m.CreateWithOpts(ctx, name, CreateOpts{})
}

// CreateWithOpts es como Create pero acepta opciones (events_webhook_url, etc.)
func (m *Manager) CreateWithOpts(ctx context.Context, name string, opts CreateOpts) (*wameow.Conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// upsert + opcionalmente set events_webhook_url
	if _, err := m.pool.Exec(ctx, `
		INSERT INTO bridge_instance(name) VALUES ($1)
		ON CONFLICT (name) DO NOTHING
	`, name); err != nil {
		return nil, fmt.Errorf("persist instance: %w", err)
	}
	if opts.EventsWebhookURL != nil {
		if _, err := m.pool.Exec(ctx, `UPDATE bridge_instance SET events_webhook_url=$2 WHERE name=$1`, name, *opts.EventsWebhookURL); err != nil {
			return nil, fmt.Errorf("set events_webhook_url: %w", err)
		}
	}
	if opts.InboxID != nil {
		var val any = *opts.InboxID
		if *opts.InboxID == 0 {
			val = nil
		}
		if _, err := m.pool.Exec(ctx, `UPDATE bridge_instance SET inbox_id=$2 WHERE name=$1`, name, val); err != nil {
			return nil, fmt.Errorf("set inbox_id: %w", err)
		}
	}
	if opts.OwnerTag != nil {
		var val any = *opts.OwnerTag
		if *opts.OwnerTag == "" {
			val = nil
		}
		if _, err := m.pool.Exec(ctx, `UPDATE bridge_instance SET owner_tag=$2 WHERE name=$1`, name, val); err != nil {
			return nil, fmt.Errorf("set owner_tag: %w", err)
		}
	}

	if c, ok := m.instances[name]; ok {
		if c.State() == "disconnected" && c.JID() == "" {
			m.logger.Info("create on stale instance, restarting to refresh QR", "name", name)
			c.Disconnect()
			delete(m.instances, name)
		} else {
			return c, nil
		}
	}

	var jid string
	if err := m.pool.QueryRow(ctx, `SELECT COALESCE(jid,'') FROM bridge_instance WHERE name=$1`, name).Scan(&jid); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return m.startLocked(ctx, name, jid)
}

// startLocked requiere m.mu en lock-write.
func (m *Manager) startLocked(ctx context.Context, name, jidStr string) (*wameow.Conn, error) {
	device, err := m.acquireDevice(ctx, jidStr)
	if err != nil {
		return nil, fmt.Errorf("acquire device: %w", err)
	}
	conn := wameow.NewConn(name, device, m.logger, m.onMsg, m.onLifecycle)
	if err := conn.Connect(ctx); err != nil {
		return nil, err
	}
	m.instances[name] = conn
	return conn, nil
}

func (m *Manager) acquireDevice(ctx context.Context, jidStr string) (*store.Device, error) {
	if jidStr == "" {
		return m.container.NewDevice(), nil
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return nil, fmt.Errorf("parse jid %q: %w", jidStr, err)
	}
	device, err := m.container.GetDevice(ctx, jid)
	if err != nil {
		return nil, err
	}
	if device == nil {
		// JID en bridge_instance pero el device fue borrado del container → nuevo
		m.logger.Warn("stored jid not present in container, creating new device", "jid", jidStr)
		return m.container.NewDevice(), nil
	}
	return device, nil
}

// onLifecycle persiste transiciones de la conexión en bridge_instance:
//   - paired: set jid + paired_at
//   - connected: set ready_at (si paired_at ya está) + last_event_at
//   - disconnected: set last_event_at
//   - logged_out: clear jid + paired_at + ready_at (deja la instancia lista para re-emparejar)
func (m *Manager) onLifecycle(ctx context.Context, name string, ev wameow.LifecycleEvent, jid string) {
	now := time.Now()
	var (
		query string
		args  []any
	)
	switch ev {
	case wameow.EventQRGenerated:
		// Incluimos last_qr_msg_id (si lo hay) para que el notifier sepa qué
		// mensaje del downstream borrar antes de postear el QR nuevo. State vive en DB
		// para sobrevivir restarts del backend.
		prev := m.LastQRMsgID(ctx, name)
		extras := map[string]any{}
		if prev > 0 {
			extras["last_qr_msg_id"] = prev
		}
		// Fire-and-forget: el ctx del callsite no es relevante porque el webhook
		// vive más allá del frame (retry exponencial). gosec G118 false positive.
		go m.emitCustomWebhook(name, ev, extras, now) //nolint:gosec
		return
	case wameow.EventPaired:
		query = `UPDATE bridge_instance SET jid=$2, paired_at=COALESCE(paired_at, $3), last_event_at=$3 WHERE name=$1`
		args = []any{name, jid, now}
		// Registrar el escaneo en el audit log (histórico inmutable; el endpoint
		// /api/public/stats lo cuenta para "QRs Escaneados").
		if m.audit != nil {
			m.audit.Record(ctx, "system", "instance.paired", name, jid, nil)
		}
	case wameow.EventConnected:
		// Conectado → el PNG anterior ya no sirve. Limpia el puntero para
		// no intentar borrar un mensaje que el notifier no debe tocar.
		_ = m.SetLastQRMsgID(ctx, name, 0)
		if jid != "" {
			query = `UPDATE bridge_instance SET jid=COALESCE(NULLIF(jid,''), $2), ready_at=COALESCE(ready_at, $3), last_event_at=$3 WHERE name=$1`
			args = []any{name, jid, now}
			defer m.notifyReady(name)
		} else {
			query = `UPDATE bridge_instance SET last_event_at=$2 WHERE name=$1`
			args = []any{name, now}
		}
	case wameow.EventDisconnected:
		query = `UPDATE bridge_instance SET last_event_at=$2 WHERE name=$1`
		args = []any{name, now}
	case wameow.EventLoggedOut:
		_ = m.SetLastQRMsgID(ctx, name, 0)
		query = `UPDATE bridge_instance SET jid=NULL, paired_at=NULL, ready_at=NULL, last_event_at=$2 WHERE name=$1`
		args = []any{name, now}
	default:
		return
	}
	if _, err := m.pool.Exec(ctx, query, args...); err != nil {
		m.logger.Error("persist lifecycle failed", "name", name, "event", string(ev), "err", err)
		return
	}
	m.logger.Info("lifecycle", "name", name, "event", string(ev), "jid", jid)

	// Para EventDisconnected: marca pendingUnreachable y programa la decisión
	// con 60s de grace. Si reconecta antes de eso → silencio total. Si sigue
	// caído tras 60s → emite unreachable y se cuenta para reconnect/disconnect
	// posteriores.
	// Para EventConnected:
	//   - bootstrap window: suprimido (avalancha de arranque)
	//   - disconnectNotified set → emite reconnected (limpia flag)
	//   - pendingUnreachable set → blip resuelto, silencio (limpia flag)
	//   - default → emite connected (primer conexión real)
	if ev == wameow.EventDisconnected {
		m.unreachMu.Lock()
		m.pendingUnreachable[name] = true
		m.unreachMu.Unlock()
		// Cancela cualquier `reconnected` pendiente: probable flap, no
		// queremos confirmar la reconexión si justo ahora se cae otra vez.
		m.reconMu.Lock()
		delete(m.pendingReconnected, name)
		m.reconMu.Unlock()
		// Sesión cae → próximo Connected será una nueva sesión y puede
		// emitir 'connected'/'reconnected' otra vez según corresponda.
		m.connEmitMu.Lock()
		delete(m.connectedEmitted, name)
		m.connEmitMu.Unlock()
		// Both goroutines outlive the callsite ctx by design. gosec G118 false positives.
		go m.emitUnreachableAfterGrace(name, jid, now)            //nolint:gosec
		go m.emitLifecycleWebhook(name, wameow.EventDisconnected, jid, now) //nolint:gosec
		return
	}
	if ev == wameow.EventLoggedOut {
		// LoggedOut también cierra la sesión actual.
		m.connEmitMu.Lock()
		delete(m.connectedEmitted, name)
		m.connEmitMu.Unlock()
	}
	if ev == wameow.EventConnected {
		if m.inBootstrapWindow() {
			m.logger.Debug("connected suppressed during bootstrap window", "name", name)
			// El backend_started broadcast (post-bootstrap) será el único
			// indicador de conexión. Marcamos connectedEmitted para que
			// subsiguientes EventConnected espurios no emitan 🎉.
			m.connEmitMu.Lock()
			m.connectedEmitted[name] = true
			m.connEmitMu.Unlock()
			return
		}
		// Blip puntual: si tenemos pendingUnreachable activo significa que
		// nunca llegamos a notificar el problema → tampoco notificamos la
		// recuperación. Panel queda limpio.
		m.unreachMu.Lock()
		blipResolved := m.pendingUnreachable[name]
		if blipResolved {
			delete(m.pendingUnreachable, name)
		}
		m.unreachMu.Unlock()
		if blipResolved {
			// Sesión recuperada silenciosamente — marcamos conectado para
			// suprimir EventConnected espurios siguientes.
			m.connEmitMu.Lock()
			m.connectedEmitted[name] = true
			m.connEmitMu.Unlock()
			m.logger.Info("brief blip resolved within grace, silent",
				"name", name)
			return
		}
		m.reconMu.Lock()
		wasNotifiedDown := m.disconnectNotified[name]
		if wasNotifiedDown {
			delete(m.disconnectNotified, name)
			m.pendingReconnected[name] = true
		}
		m.reconMu.Unlock()
		if wasNotifiedDown {
			// Reconnected con stabilize-delay: si en 5s sigue conectado,
			// emitimos el evento. Si cae otra vez (flap), el handler de
			// Disconnected limpia pendingReconnected y este goroutine
			// silencia.
			m.connEmitMu.Lock()
			m.connectedEmitted[name] = true
			m.connEmitMu.Unlock()
			go m.emitReconnectedAfterStabilize(name, jid, now)
			return
		}
		// Default → "primer connected" para esta sesión. PERO si ya
		// emitimos uno (whatsmeow re-handshake / session renewal silent),
		// lo suprimimos para evitar el "🎉 espurio".
		m.connEmitMu.Lock()
		alreadyEmitted := m.connectedEmitted[name]
		if !alreadyEmitted {
			m.connectedEmitted[name] = true
		}
		m.connEmitMu.Unlock()
		if alreadyEmitted {
			m.logger.Info("connected suppressed (already emitted for this session)",
				"name", name)
			return
		}
	}
	go m.emitLifecycleWebhook(name, ev, jid, now) //nolint:gosec
}

// emitReconnectedAfterStabilize espera 5s para confirmar que la reconexión es
// estable antes de notificar. Evita spam visual cuando hay flaps en cadena.
const reconnectedStabilizeSeconds = 5

func (m *Manager) emitReconnectedAfterStabilize(name, jid string, occurredAt time.Time) {
	time.Sleep(reconnectedStabilizeSeconds * time.Second)
	conn, ok := m.Get(name)
	if !ok {
		m.reconMu.Lock()
		delete(m.pendingReconnected, name)
		m.reconMu.Unlock()
		return
	}
	m.reconMu.Lock()
	stillPending := m.pendingReconnected[name]
	delete(m.pendingReconnected, name)
	m.reconMu.Unlock()
	if !stillPending {
		// Disconnect happened during the 5s window → silent.
		return
	}
	if !conn.IsConnected() {
		// Race: flag still set but connection dropped after the Disconnected
		// handler ran. Treat as flap.
		return
	}
	m.emitLifecycleWebhook(name, wameow.EventReconnected, jid, occurredAt)
}

// emitUnreachableAfterGrace espera la ventana de grace antes de decidir si
// emite el evento unreachable. Si la instancia reconectó durante la espera
// (pendingUnreachable se limpió desde el handler de Connected), no emite nada.
const unreachableGraceSeconds = 60

func (m *Manager) emitUnreachableAfterGrace(name, jid string, occurredAt time.Time) {
	time.Sleep(unreachableGraceSeconds * time.Second)
	// Check if instance recovered while we were waiting
	conn, ok := m.Get(name)
	if !ok {
		// instance was removed entirely
		m.unreachMu.Lock()
		delete(m.pendingUnreachable, name)
		m.unreachMu.Unlock()
		return
	}
	if conn.IsConnected() {
		// Recovered before grace expired → silent (the Connected handler
		// will / has already cleared the flag).
		return
	}
	// Still down → claim the pending slot and emit unreachable
	m.unreachMu.Lock()
	stillPending := m.pendingUnreachable[name]
	delete(m.pendingUnreachable, name)
	m.unreachMu.Unlock()
	if !stillPending {
		// Race: pending was cleared (probably by a Connected that arrived
		// between our IsConnected check and the lock). Stay silent.
		return
	}
	m.emitLifecycleWebhook(name, wameow.EventUnreachable, jid, occurredAt)
}

// emitLifecycleWebhook dispara el HTTP POST al events_webhook_url configurado
// para la instancia. Async, best-effort, no bloquea la lib whatsmeow.
//
// Para EventDisconnected aplica grace period de 2 min: si la instancia
// reconecta antes, se cancela la notificación. Esto evita ruido en blips
// transitorios que el auto-reconnect de whatsmeow recupera solo.
func (m *Manager) emitLifecycleWebhook(name string, ev wameow.LifecycleEvent, jid string, occurredAt time.Time) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Error("panic in emitLifecycleWebhook", "panic", fmt.Sprintf("%v", r))
		}
	}()

	// Lookup events_webhook_url
	var url string
	if err := m.pool.QueryRow(context.Background(), `SELECT COALESCE(events_webhook_url, '') FROM bridge_instance WHERE name=$1`, name).Scan(&url); err != nil {
		return
	}
	if url == "" {
		return
	}

	// Grace period para Disconnected: esperar 2 min y verificar si sigue desconectado
	if ev == wameow.EventDisconnected {
		time.Sleep(2 * time.Minute)
		conn, ok := m.Get(name)
		if !ok {
			return // instance gone, no notif
		}
		if conn.IsConnected() {
			m.logger.Info("disconnect was transient, skipping notification", "name", name)
			return
		}
		// Sigue caído tras 2 min, notificar
	}

	// Marcamos disconnectNotified al emitir Unreachable — el próximo Connected
	// se convertirá automáticamente en Reconnected (ver onLifecycle).
	if ev == wameow.EventUnreachable {
		m.reconMu.Lock()
		m.disconnectNotified[name] = true
		m.reconMu.Unlock()
	}

	payload := map[string]any{
		"instance":    name,
		"event":       string(ev),
		"jid":         jid,
		"occurred_at": occurredAt.UTC().Format(time.RFC3339),
	}
	if m.version != "" {
		payload["version"] = m.version
	}
	body, _ := json.Marshal(payload)
	m.postWebhookWithRetry(name, string(ev), url, body)
	metrics.LifecycleEvents.WithLabelValues(name, string(ev), m.ownerTag(name)).Inc()
	if m.usage != nil {
		m.usage.IncLifecycle(name)
	}
}

// InboxIDFor devuelve el inbox_id (downstream) asociado a la instancia, o 0 si no hay.
// Útil para que el bridge enrute eventos al inbox correcto en multi-tenant.
func (m *Manager) InboxIDFor(ctx context.Context, name string) int {
	var id sql.NullInt32
	err := m.pool.QueryRow(ctx, `SELECT inbox_id FROM bridge_instance WHERE name=$1`, name).Scan(&id)
	if err != nil || !id.Valid {
		return 0
	}
	return int(id.Int32)
}

// SpamguardConfig devuelve la config de spamguard para la instancia.
// Si la fila no existe o hay error → (false, 30s, 10) como defaults.
func (m *Manager) SpamguardConfig(ctx context.Context, name string) (enabled bool, windowMs int, minChars int) {
	enabled = false
	windowMs = 30000
	minChars = 10
	_ = m.pool.QueryRow(ctx,
		`SELECT spamguard_enabled, spamguard_window_ms, spamguard_min_chars
		 FROM bridge_instance WHERE name=$1`, name).
		Scan(&enabled, &windowMs, &minChars)
	return
}

// SetSpamguard actualiza el flag enabled. window/minChars se mantienen.
func (m *Manager) SetSpamguard(ctx context.Context, name string, enabled bool) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE bridge_instance SET spamguard_enabled=$2 WHERE name=$1`,
		name, enabled)
	return err
}

// IsSpamguardEnabled — lectura barata del flag enabled (ignora window/minChars).
func (m *Manager) IsSpamguardEnabled(ctx context.Context, name string) bool {
	enabled, _, _ := m.SpamguardConfig(ctx, name)
	return enabled
}

// SetOwnerTag actualiza el owner_tag de una instancia. Pasa "" para borrar.
// Devuelve ErrNotFound si la instancia no existe.
func (m *Manager) SetOwnerTag(ctx context.Context, name, tag string) error {
	var val any = tag
	if tag == "" {
		val = nil
	}
	res, err := m.pool.Exec(ctx,
		`UPDATE bridge_instance SET owner_tag=$2 WHERE name=$1`, name, val)
	if err != nil {
		return fmt.Errorf("set owner_tag: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LastQRMsgID lectura del id del último mensaje del downstream que contiene el QR
// PNG actual. 0 si no hay (primer QR de la sesión o ya limpiado).
func (m *Manager) LastQRMsgID(ctx context.Context, name string) int {
	var id sql.NullInt32
	_ = m.pool.QueryRow(ctx,
		`SELECT last_qr_msg_id FROM bridge_instance WHERE name=$1`, name).Scan(&id)
	if !id.Valid {
		return 0
	}
	return int(id.Int32)
}

// SetLastQRMsgID actualiza el id del último PNG posteado. Pasa 0 para limpiar
// (e.g. tras connected/logged_out cuando ya no hay QR vigente).
func (m *Manager) SetLastQRMsgID(ctx context.Context, name string, msgID int) error {
	var arg any
	if msgID == 0 {
		arg = nil
	} else {
		arg = msgID
	}
	_, err := m.pool.Exec(ctx,
		`UPDATE bridge_instance SET last_qr_msg_id=$2 WHERE name=$1`, name, arg)
	return err
}

// EmitCustomLifecycle dispara un evento de lifecycle "no canónico" (e.g.
// "spam_blocked") al webhook configurado, con campos extras opcionales
// (count, preview, etc) que se fusionan al payload base.
func (m *Manager) EmitCustomLifecycle(name, event string, extras map[string]any) {
	go m.emitCustomWebhook(name, wameow.LifecycleEvent(event), extras, time.Now())
}

// BroadcastBackendStarted emite "backend_started" a cada instancia tras
// completar Bootstrap. Sustituye a la avalancha de Connected que de otra forma
// vería el usuario en cada QR-X cuando qrsgen arranca.
func (m *Manager) BroadcastBackendStarted() {
	m.mu.RLock()
	names := make([]string, 0, len(m.instances))
	for n := range m.instances {
		names = append(names, n)
	}
	m.mu.RUnlock()
	// Por instancia: esperamos a state=ready con timeout. Si llega → connected:true.
	// Si timeout sin ready → connected:false. Más fiable que el snapshot fijo
	// a los 8s post-boot porque WhatsApp handshakes lentos no se reportan como
	// falsos negativos.
	const readyTimeout = 20 * time.Second
	for _, name := range names {
		conn, ok := m.Get(name)
		connected := false
		if ok {
			if conn.IsConnected() {
				// Atajo: ya está connected ahora mismo, no esperar.
				connected = true
			} else {
				// Espera bloqueante hasta ready o timeout
				waitCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
				if _, err := m.WaitReady(waitCtx, name); err == nil {
					connected = true
				}
				cancel()
			}
		}
		extras := map[string]any{"connected": connected}
		m.emitCustomWebhook(name, wameow.LifecycleEvent("backend_started"), extras, time.Now())
	}
}

// BroadcastBackendRestarting emite un evento "backend_restarting" SÍNCRONO a
// todas las instancias activas. Se llama justo antes del shutdown para que el
// usuario en QR-X vea que la pérdida de conexión es esperada (deploy/restart).
// Es síncrono porque queremos que los webhooks lleguen antes de cerrar.
func (m *Manager) BroadcastBackendRestarting() {
	m.mu.RLock()
	names := make([]string, 0, len(m.instances))
	for n := range m.instances {
		names = append(names, n)
	}
	m.mu.RUnlock()
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			m.emitCustomWebhook(name, wameow.LifecycleEvent("backend_restarting"), nil, time.Now())
		}(n)
	}
	// timeout corto: si los webhooks no responden en 5s, seguimos con shutdown
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// criticalLifecycleEvents son los eventos que deben tener retry exponencial
// porque su pérdida tiene consecuencias operativas reales (estado del agente,
// facturación, decisiones de pausa de envíos). Los demás (qr_generated,
// connected, paired) NO se reintentan porque WhatsApp re-emite el estado o
// son tan frecuentes que la pérdida es irrelevante.
var criticalLifecycleEvents = map[string]bool{
	"strike":             true, // WhatsApp baneó/restringió — acción inmediata
	"ban_risk":           true, // Detector cruzó threshold — preventivo
	"outgoing_expired":   true, // Mensaje en outbox no entregado — integrator decide
	"logged_out":         true, // Sesión inválida server-side — re-pairing
	"spam_blocked":       true, // Bloqueo del spamguard — contador para usuario
	"backend_restarting": true, // Aviso pre-shutdown — agente debe verlo
}

// retryBackoffs son los delays para los 3 reintentos en caso de fallo del POST.
var retryBackoffs = []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}

// postWebhookOnce hace un POST único y devuelve nil si llegó (2xx) o un error
// describiendo qué falló. No hace ningún reintento.
func (m *Manager) postWebhookOnce(url string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

// postWebhookWithRetry hace el POST inicial y, si falla y el evento es
// crítico, reintenta con backoff exponencial en una goroutine separada (para
// no bloquear el emisor). Útil para eventos que NO deben perderse silenciosamente
// cuando el orquestador está caído.
//
// El primer intento es síncrono. Los reintentos son async.
func (m *Manager) postWebhookWithRetry(name string, event string, url string, body []byte) {
	err := m.postWebhookOnce(url, body)
	if err == nil {
		return
	}
	if !criticalLifecycleEvents[event] {
		m.logger.Warn("webhook POST failed (no retry, non-critical event)",
			"name", name, "event", event, "url", url, "err", err)
		return
	}
	// Critical event — async retry with exponential backoff.
	go m.retryWebhookLoop(name, event, url, body, err)
}

// retryWebhookLoop ejecuta los reintentos según retryBackoffs.
func (m *Manager) retryWebhookLoop(name, event, url string, body []byte, lastErr error) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Error("panic in retryWebhookLoop", "panic", fmt.Sprintf("%v", r))
		}
	}()
	for i, delay := range retryBackoffs {
		m.logger.Warn("lifecycle webhook retry scheduled",
			"name", name, "event", event, "attempt", i+1, "delay", delay, "last_err", lastErr.Error())
		time.Sleep(delay)
		if err := m.postWebhookOnce(url, body); err != nil {
			lastErr = err
			continue
		}
		metrics.LifecycleWebhookRetries.WithLabelValues(event, "success").Inc()
		m.logger.Info("lifecycle webhook retry succeeded",
			"name", name, "event", event, "attempt", i+1)
		return
	}
	metrics.LifecycleWebhookRetries.WithLabelValues(event, "exhausted").Inc()
	m.logger.Error("lifecycle webhook exhausted retries — event dropped",
		"name", name, "event", event, "last_err", lastErr.Error())
}

// emitCustomWebhook variante de emitLifecycleWebhook que acepta extras.
func (m *Manager) emitCustomWebhook(name string, ev wameow.LifecycleEvent, extras map[string]any, occurredAt time.Time) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Error("panic in emitCustomWebhook", "panic", fmt.Sprintf("%v", r))
		}
	}()
	var url string
	if err := m.pool.QueryRow(context.Background(), `SELECT COALESCE(events_webhook_url, '') FROM bridge_instance WHERE name=$1`, name).Scan(&url); err != nil {
		return
	}
	if url == "" {
		return
	}
	payload := map[string]any{
		"instance":    name,
		"event":       string(ev),
		"occurred_at": occurredAt.UTC().Format(time.RFC3339),
	}
	if m.version != "" {
		payload["version"] = m.version
	}
	for k, v := range extras {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	m.postWebhookWithRetry(name, string(ev), url, body)
	// Las métricas y usage se cuentan en el emit (no en el retry success):
	// representan "qrsgen intentó emitir el evento", no "el orquestador lo
	// recibió".
	metrics.LifecycleEvents.WithLabelValues(name, string(ev), m.ownerTag(name)).Inc()
	if m.usage != nil {
		m.usage.IncLifecycle(name)
	}
}

// Get devuelve la instancia activa por nombre.
func (m *Manager) Get(name string) (*wameow.Conn, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.instances[name]
	return c, ok
}

// IsConnected reports whether the instance is in memory and its WhatsApp
// WebSocket is up. False for unknown instances. Satisfies outbox.Connector.
func (m *Manager) IsConnected(name string) bool {
	c, ok := m.Get(name)
	if !ok {
		return false
	}
	return c.IsConnected()
}

// List devuelve los nombres de todas las instancias activas (en memoria).
func (m *Manager) List() []InstanceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]InstanceInfo, 0, len(m.instances))
	active := 0
	for name, c := range m.instances {
		state := c.State()
		out = append(out, InstanceInfo{Name: name, State: state, JID: c.JID()})
		if c.IsConnected() {
			active++
		}
	}
	// Side-effect: actualizamos gauges Prometheus en cada List() (que es lo
	// que Prometheus llama cuando hace scrape al /api/instances o /metrics).
	metrics.ActiveInstances.Set(float64(active))
	metrics.TotalInstances.Set(float64(len(out)))
	return out
}

type InstanceInfo struct {
	Name  string `json:"name"`
	State string `json:"state"`
	JID   string `json:"jid,omitempty"`
}

// QRInfo describe el estado del canal QR para una instancia.
type QRInfo struct {
	Available       bool       `json:"available"`
	GeneratedAt     *time.Time `json:"generated_at,omitempty"`
	ExpiresInSecond int        `json:"expires_in_seconds,omitempty"`
}

// InstanceStatus es el shape rico que un orquestador necesita para tomar decisiones.
// Combina el estado live de la conexión (memoria) con timestamps persistidos (DB).
type InstanceStatus struct {
	Name        string     `json:"name"`
	State       string     `json:"state"` // provisioning | qr_pending | paired | ready | disconnected
	JID         string     `json:"jid,omitempty"`
	Phone       string     `json:"phone,omitempty"` // user part del JID si es @s.whatsapp.net
	QR          QRInfo     `json:"qr"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	PairedAt    *time.Time `json:"paired_at,omitempty"`
	ReadyAt     *time.Time `json:"ready_at,omitempty"`
	LastEventAt *time.Time `json:"last_event_at,omitempty"`
	OwnerTag    string     `json:"owner_tag,omitempty"`
}

// qrLifetimeSeconds es el TTL aproximado de cada QR generado por whatsmeow.
const qrLifetimeSeconds = 20

// Status devuelve el shape rico para una instancia (live + DB).
// Si la instancia no existe ni en memoria ni en DB, devuelve ErrNotFound.
func (m *Manager) Status(ctx context.Context, name string) (InstanceStatus, error) {
	m.mu.RLock()
	conn, inMem := m.instances[name]
	m.mu.RUnlock()

	st := InstanceStatus{Name: name}

	// timestamps persistidos
	var createdAt, pairedAt, readyAt, lastEvent *time.Time
	var jidDB, ownerTag string
	err := m.pool.QueryRow(ctx, `
		SELECT created_at, paired_at, ready_at, last_event_at, COALESCE(jid,''), COALESCE(owner_tag,'')
		FROM bridge_instance WHERE name=$1
	`, name).Scan(&createdAt, &pairedAt, &readyAt, &lastEvent, &jidDB, &ownerTag)
	if errors.Is(err, pgx.ErrNoRows) {
		if !inMem {
			return InstanceStatus{}, ErrNotFound
		}
	} else if err != nil {
		return InstanceStatus{}, fmt.Errorf("query status: %w", err)
	}

	st.CreatedAt = createdAt
	st.PairedAt = pairedAt
	st.ReadyAt = readyAt
	st.LastEventAt = lastEvent
	st.OwnerTag = ownerTag

	// live state
	if inMem {
		st.JID = conn.JID()
		if conn.HasQR() {
			t := conn.LastQRAt()
			elapsed := int(time.Since(t).Seconds())
			remaining := qrLifetimeSeconds - elapsed
			if remaining < 0 {
				remaining = 0
			}
			st.QR = QRInfo{Available: true, GeneratedAt: &t, ExpiresInSecond: remaining}
		}
	}
	if st.JID == "" {
		st.JID = jidDB
	}
	st.Phone = phoneFromJID(st.JID)

	// state machine derivation
	switch {
	case st.ReadyAt != nil && inMem && conn.IsConnected():
		st.State = "ready"
	case st.PairedAt != nil && inMem:
		st.State = "paired"
	case st.QR.Available:
		st.State = "qr_pending"
	case inMem:
		st.State = "provisioning"
	default:
		st.State = "disconnected"
	}

	return st, nil
}

// BulkStatus devuelve status de varias instancias en una sola llamada.
// Las que no existen aparecen con State="not_found".
func (m *Manager) BulkStatus(ctx context.Context, names []string) []InstanceStatus {
	out := make([]InstanceStatus, 0, len(names))
	for _, n := range names {
		st, err := m.Status(ctx, n)
		if errors.Is(err, ErrNotFound) {
			out = append(out, InstanceStatus{Name: n, State: "not_found"})
			continue
		}
		if err != nil {
			out = append(out, InstanceStatus{Name: n, State: "error"})
			continue
		}
		out = append(out, st)
	}
	return out
}

// BulkCreateResult es el resultado por instancia de un POST /api/instances/bulk.
type BulkCreateResult struct {
	Name  string `json:"name"`
	State string `json:"state"`
	JID   string `json:"jid,omitempty"`
	Error string `json:"error,omitempty"`
}

// BulkCreate crea/reusa N instancias. Errores parciales NO abortan el resto.
// Idempotente: si una instancia ya existe activa, devuelve su estado actual sin error.
func (m *Manager) BulkCreate(ctx context.Context, names []string) []BulkCreateResult {
	out := make([]BulkCreateResult, 0, len(names))
	for _, n := range names {
		conn, err := m.Create(ctx, n)
		if err != nil {
			out = append(out, BulkCreateResult{Name: n, State: "error", Error: err.Error()})
			continue
		}
		out = append(out, BulkCreateResult{Name: n, State: conn.State(), JID: conn.JID()})
	}
	return out
}

// RefreshQR fuerza la regeneración del canal QR para una instancia desconectada.
// Si la instancia está ya emparejada/conectada, no-op (no rompemos sesiones válidas).
func (m *Manager) RefreshQR(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.instances[name]
	if !ok {
		return ErrNotFound
	}
	if c.JID() != "" && c.IsConnected() {
		return nil // sesión válida, no tocar
	}
	c.Disconnect()
	delete(m.instances, name)

	var jid string
	_ = m.pool.QueryRow(ctx, `SELECT COALESCE(jid,'') FROM bridge_instance WHERE name=$1`, name).Scan(&jid)
	_, err := m.startLocked(ctx, name, jid)
	return err
}

// notifyReady cierra todos los canales suscritos a "name esta ready".
// Idempotente: si no hay suscriptores, no-op. Se ejecuta cuando el bridge_instance
// se actualiza con ready_at por primera vez tras un Connected con JID.
func (m *Manager) notifyReady(name string) {
	m.waitersMu.Lock()
	chs := m.waiters[name]
	delete(m.waiters, name)
	m.waitersMu.Unlock()
	for _, ch := range chs {
		close(ch)
	}
	if len(chs) > 0 {
		m.logger.Info("notified ready waiters", "name", name, "count", len(chs))
	}
}

// WaitReady bloquea hasta que la instancia esté en state=ready, hasta que ctx
// expire o la instancia entre en error definitivo (logged_out).
//
// Devuelve el InstanceStatus final cuando se cumple ready. Devuelve
// context.DeadlineExceeded si el caller timeout-ea, ErrNotFound si la
// instancia no existe.
//
// Diseñado para long-polling desde orquestadores (n8n). Internamente es
// cero-CPU: no hace polling, suscribe canal que se cierra en el evento.
func (m *Manager) WaitReady(ctx context.Context, name string) (InstanceStatus, error) {
	// Fast path: ya está ready o ya no existe
	st, err := m.Status(ctx, name)
	if err != nil {
		return InstanceStatus{}, err
	}
	if st.State == "ready" {
		return st, nil
	}

	ch := make(chan struct{})
	m.waitersMu.Lock()
	m.waiters[name] = append(m.waiters[name], ch)
	m.waitersMu.Unlock()

	// Defer cleanup: si ctx expira o panic, quitamos el canal del slice.
	defer func() {
		m.waitersMu.Lock()
		w := m.waiters[name]
		for i, c := range w {
			if c == ch {
				m.waiters[name] = append(w[:i], w[i+1:]...)
				break
			}
		}
		m.waitersMu.Unlock()
	}()

	select {
	case <-ch:
		// Canal cerrado por notifyReady → consulta fresca del estado
		return m.Status(ctx, name)
	case <-ctx.Done():
		return InstanceStatus{}, ctx.Err()
	}
}

func phoneFromJID(jid string) string {
	if jid == "" {
		return ""
	}
	atIdx := -1
	for i := range jid {
		if jid[i] == '@' {
			atIdx = i
			break
		}
	}
	if atIdx < 0 {
		return ""
	}
	server := jid[atIdx+1:]
	if server != "s.whatsapp.net" {
		return ""
	}
	user := jid[:atIdx]
	colonIdx := -1
	for i := range user {
		if user[i] == ':' {
			colonIdx = i
			break
		}
	}
	if colonIdx >= 0 {
		user = user[:colonIdx]
	}
	return user
}

// Restart desconecta y vuelve a conectar la instancia.
func (m *Manager) Restart(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.instances[name]
	if !ok {
		return ErrNotFound
	}
	c.Disconnect()
	delete(m.instances, name)

	var jid string
	_ = m.pool.QueryRow(ctx, `SELECT COALESCE(jid,'') FROM bridge_instance WHERE name=$1`, name).Scan(&jid)
	_, err := m.startLocked(ctx, name, jid)
	return err
}

// Logout desvincula la sesión en WhatsApp y limpia el JID en bridge_instance,
// dejando la instancia lista para re-emparejarse.
func (m *Manager) Logout(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.instances[name]
	if !ok {
		return ErrNotFound
	}
	_ = c.Logout(ctx)
	c.Disconnect()
	delete(m.instances, name)
	if _, err := m.pool.Exec(ctx, `UPDATE bridge_instance SET jid=NULL WHERE name=$1`, name); err != nil {
		return fmt.Errorf("clear jid: %w", err)
	}
	// re-arranca con device nuevo para regenerar QR
	if _, err := m.startLocked(ctx, name, ""); err != nil {
		return err
	}
	return nil
}

// Delete elimina la instancia: logout, cierra, borra row.
func (m *Manager) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.instances[name]; ok {
		_ = c.Logout(ctx)
		c.Disconnect()
		delete(m.instances, name)
	}
	if _, err := m.pool.Exec(ctx, `DELETE FROM bridge_instance WHERE name=$1`, name); err != nil {
		return fmt.Errorf("delete row: %w", err)
	}
	return nil
}

// Shutdown cierra todas las conexiones activas.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, c := range m.instances {
		c.Disconnect()
		m.logger.Info("shutdown instance", "name", name)
	}
	m.instances = map[string]*wameow.Conn{}
}
