// Package wameow envuelve whatsmeow (go.mau.fi/whatsmeow) y expone una
// interfaz limpia para el resto de la aplicación.
package wameow

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registra el driver "pgx" para database/sql usado por whatsmeow's sqlstore

	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// MessageHandler procesa eventos de mensaje. Recibe el nombre de la instancia
// y un WAResolver para consultas al estado local de whatsmeow (libreta de
// contactos del propio usuario y mapping LID↔PN).
type MessageHandler func(ctx context.Context, instance string, msg *events.Message, r WAResolver)

// WAResolver expone consultas al estado local del cliente whatsmeow.
type WAResolver interface {
	// ContactName devuelve el nombre cacheado para un JID, o "" si no hay info.
	ContactName(jid types.JID) string
	// PNForLID intenta resolver el JID PN equivalente a un LID. Devuelve el JID y true si lo conoce.
	PNForLID(lid types.JID) (types.JID, bool)
	// LIDForPN intenta resolver el JID LID equivalente a un PN. Devuelve el JID y true si lo conoce.
	LIDForPN(pn types.JID) (types.JID, bool)
	// DownloadAny descarga el media adjunto a un mensaje E2E. Devuelve los bytes
	// desencriptados. Devuelve error si no hay media o falla el download.
	DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error)
}

// NameResolver alias para compatibilidad (un solo método); el bridge ya usa WAResolver.
type NameResolver = func(jid types.JID) string

// LifecycleEvent representa transiciones de la conexión que el manager debe persistir.
type LifecycleEvent string

const (
	EventQRGenerated  LifecycleEvent = "qr_generated" // nuevo QR PNG disponible para escanear
	EventPaired       LifecycleEvent = "paired"       // PairSuccess: device vinculado, JID disponible
	EventConnected    LifecycleEvent = "connected"    // WebSocket activo y session válida (primera vez tras pair)
	EventReconnected  LifecycleEvent = "reconnected"  // WebSocket recuperado tras un Disconnected post-grace
	EventUnreachable  LifecycleEvent = "unreachable"  // pérdida inmediata del WebSocket (antes del grace)
	EventDisconnected LifecycleEvent = "disconnected" // pérdida de WebSocket confirmada tras 2-min grace
	EventLoggedOut    LifecycleEvent = "logged_out"   // session invalidada en servidor (no recoverable)
	EventSpamBlocked  LifecycleEvent = "spam_blocked" // mensaje outgoing bloqueado por filtro spamguard
	EventStrike       LifecycleEvent = "strike"       // WhatsApp emitió warning/ban contra la cuenta
)

// LifecycleCallback se invoca en cada transición. jid es "" para events sin JID conocido.
type LifecycleCallback func(ctx context.Context, instance string, ev LifecycleEvent, jid string)

// PairCallback alias legacy — usar LifecycleCallback. (Deprecado, mantener compat.)
type PairCallback func(ctx context.Context, instance, jid string)

// Conn representa una conexión whatsmeow para una instancia con nombre.
type Conn struct {
	name        string
	client      *whatsmeow.Client
	logger      *slog.Logger
	onMessage   MessageHandler
	onLifecycle LifecycleCallback

	mu        sync.RWMutex
	lastQRPNG []byte
	lastQRAt  time.Time
	qrCancel  context.CancelFunc
}

// NewContainer crea el sqlstore.Container compartido por todas las instancias.
func NewContainer(ctx context.Context, dsn string) (*sqlstore.Container, error) {
	container, err := sqlstore.New(ctx, "pgx", dsn, waLog.Stdout("Database", "INFO", true))
	if err != nil {
		return nil, fmt.Errorf("sqlstore.New: %w", err)
	}
	return container, nil
}

// NewConn construye una conexión a partir de un *store.Device ya existente
// (nuevo o recuperado del container).
func NewConn(name string, device *store.Device, logger *slog.Logger, onMsg MessageHandler, onLifecycle LifecycleCallback) *Conn {
	client := whatsmeow.NewClient(device, waLog.Stdout("Client:"+name, "INFO", true))
	// Auto-reconnect agresivo: si el WebSocket se cae, whatsmeow lo reabre solo.
	// Sin esto, una pérdida transitoria deja la instancia disconnected hasta
	// que algo externo (watchdog, restart) la rearranque.
	client.EnableAutoReconnect = true
	client.InitialAutoReconnect = true
	c := &Conn{
		name:        name,
		client:      client,
		logger:      logger.With("instance", name),
		onMessage:   onMsg,
		onLifecycle: onLifecycle,
	}
	client.AddEventHandler(c.handle)
	return c
}

// Name devuelve el nombre lógico de la instancia.
func (c *Conn) Name() string { return c.name }

// Connect arranca la conexión. Si el device aún no está pareado, abre canal QR.
func (c *Conn) Connect(ctx context.Context) error {
	if c.client.Store.ID == nil {
		qrCtx, cancel := context.WithCancel(context.Background())
		c.mu.Lock()
		c.qrCancel = cancel
		c.mu.Unlock()
		qrChan, _ := c.client.GetQRChannel(qrCtx)
		if err := c.client.Connect(); err != nil {
			cancel()
			return fmt.Errorf("connect: %w", err)
		}
		go c.listenQR(qrChan)
	} else {
		if err := c.client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
	}
	return nil
}

func (c *Conn) listenQR(qrChan <-chan whatsmeow.QRChannelItem) {
	for evt := range qrChan {
		if evt.Event == "code" {
			png, err := qrcode.Encode(evt.Code, qrcode.Medium, 1024)
			if err != nil {
				c.logger.Error("qr encode failed", "err", err)
				continue
			}
			c.mu.Lock()
			c.lastQRPNG = png
			c.lastQRAt = time.Now()
			c.mu.Unlock()
			c.logger.Info("new QR available")
			c.emit(EventQRGenerated, "")
		} else {
			c.logger.Info("qr event", "event", evt.Event)
		}
	}
}

// ContactName devuelve el nombre cacheado para un JID en el contact store local
// (FullName > FirstName > PushName > BusinessName). "" si no hay registro.
func (c *Conn) ContactName(jid types.JID) string {
	if c.client == nil || c.client.Store == nil || c.client.Store.Contacts == nil {
		return ""
	}
	info, err := c.client.Store.Contacts.GetContact(context.Background(), jid.ToNonAD())
	if err != nil || !info.Found {
		return ""
	}
	switch {
	case info.FullName != "":
		return info.FullName
	case info.FirstName != "":
		return info.FirstName
	case info.PushName != "":
		return info.PushName
	case info.BusinessName != "":
		return info.BusinessName
	}
	return ""
}

// PNForLID intenta mapear un JID LID a su PN. Devuelve la JID y true si lo conoce.
func (c *Conn) PNForLID(lid types.JID) (types.JID, bool) {
	if c.client == nil || c.client.Store == nil || c.client.Store.LIDs == nil {
		return types.JID{}, false
	}
	if lid.Server != types.HiddenUserServer {
		return types.JID{}, false
	}
	pn, err := c.client.Store.LIDs.GetPNForLID(context.Background(), lid.ToNonAD())
	if err != nil || pn.IsEmpty() {
		return types.JID{}, false
	}
	return pn, true
}

// DownloadAny descarga el primer media (image/audio/video/document/sticker)
// presente en el mensaje. Devuelve los bytes en claro.
func (c *Conn) DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	if c.client == nil || msg == nil {
		return nil, fmt.Errorf("download: cliente o mensaje nil")
	}
	return c.client.DownloadAny(ctx, msg)
}

// LIDForPN intenta mapear un JID PN a su LID. Devuelve la JID y true si lo conoce.
func (c *Conn) LIDForPN(pn types.JID) (types.JID, bool) {
	if c.client == nil || c.client.Store == nil || c.client.Store.LIDs == nil {
		return types.JID{}, false
	}
	if pn.Server != types.DefaultUserServer {
		return types.JID{}, false
	}
	lid, err := c.client.Store.LIDs.GetLIDForPN(context.Background(), pn.ToNonAD())
	if err != nil || lid.IsEmpty() {
		return types.JID{}, false
	}
	return lid, true
}

func (c *Conn) handle(rawEvt any) {
	// recover() defensivo: whatsmeow tiene panics conocidos en parseo binario
	// (binary/encoder.go). Si la lib pánica aquí, sin este recover la goroutine
	// muere y la instancia queda zombie. Capturamos, logueamos y dejamos vivir.
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("PANIC in event handler — recovered to keep instance alive",
				"panic", fmt.Sprintf("%v", r),
				"event_type", fmt.Sprintf("%T", rawEvt),
				"stack", string(debug.Stack()),
			)
		}
	}()
	switch evt := rawEvt.(type) {
	case *events.Message:
		if c.onMessage != nil {
			c.onMessage(context.Background(), c.name, evt, c)
		}
	case *events.Connected:
		c.logger.Info("connected to whatsapp")
		c.mu.Lock()
		c.lastQRPNG = nil
		c.mu.Unlock()
		jid := ""
		if c.client.Store.ID != nil {
			jid = c.client.Store.ID.String()
		}
		c.emit(EventConnected, jid)
	case *events.PairSuccess:
		c.logger.Info("pair success", "jid", evt.ID.String())
		c.emit(EventPaired, evt.ID.String())
	case *events.Disconnected:
		c.logger.Warn("disconnected from whatsapp")
		c.emit(EventDisconnected, "")
	case *events.LoggedOut:
		c.logger.Warn("logged out — session invalidated")
		c.emit(EventLoggedOut, "")
	case *events.TemporaryBan:
		// WhatsApp aplicó un ban temporal a la cuenta — el "strike" típico antes
		// de un bloqueo permanente. Causas comunes: enviar a muchos no-contactos,
		// reportes de usuarios, patrones de spam. Avisamos al agente para que
		// frene el ritmo / revise comportamiento del técnico.
		c.logger.Warn("temporary ban from WhatsApp",
			"code", int(evt.Code), "expires_in", evt.Expire.String())
		c.emit(EventStrike, evt.String())
	case *events.ConnectFailure:
		// Reconectar falló — algunos códigos indican ban (e.g. 401, 403).
		c.logger.Warn("connect failure", "reason", evt.Reason.String(), "message", evt.Message)
		if evt.Reason == 401 || evt.Reason == 403 || evt.Reason == 405 {
			c.emit(EventStrike, fmt.Sprintf("connect_failure_%d", int(evt.Reason)))
		}
	}
}

func (c *Conn) emit(ev LifecycleEvent, jid string) {
	if c.onLifecycle == nil {
		return
	}
	c.onLifecycle(context.Background(), c.name, ev, jid)
}

// LatestQR devuelve el último QR generado como PNG, o nil si no hay.
func (c *Conn) LatestQR() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.lastQRPNG) == 0 {
		return nil
	}
	out := make([]byte, len(c.lastQRPNG))
	copy(out, c.lastQRPNG)
	return out
}

// LastQRAt devuelve cuándo se generó el último QR (zero si nunca).
func (c *Conn) LastQRAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastQRAt
}

// HasQR indica si hay un QR disponible para escanear ahora mismo.
func (c *Conn) HasQR() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.lastQRPNG) > 0
}

// IsConnected indica si la sesión está activa.
func (c *Conn) IsConnected() bool {
	return c.client.IsConnected() && c.client.IsLoggedIn()
}

// State devuelve "connected" | "connecting" | "disconnected".
func (c *Conn) State() string {
	switch {
	case c.IsConnected():
		return "connected"
	case c.client.IsConnected():
		return "connecting"
	default:
		return "disconnected"
	}
}

// JID devuelve el JID del device, o cadena vacía si aún no parado.
func (c *Conn) JID() string {
	if c.client.Store.ID == nil {
		return ""
	}
	return c.client.Store.ID.String()
}

// SendText envía un mensaje de texto a remoteJid y retorna el WAID.
func (c *Conn) SendText(ctx context.Context, remoteJid, content string) (string, error) {
	jid, err := parseJID(remoteJid)
	if err != nil {
		return "", err
	}
	resp, err := c.client.SendMessage(ctx, jid, simpleTextMessage(content))
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	return resp.ID, nil
}

// SendMedia sube un blob al servidor de WhatsApp y lo envía como
// ImageMessage / AudioMessage / VideoMessage / DocumentMessage según `kind`.
//
// kind:
//   - "image"  → ImageMessage (caption opcional)
//   - "audio"  → AudioMessage (sin caption; si es voice note usar isPTT=true vía mimetype "audio/ogg; codecs=opus")
//   - "video"  → VideoMessage (caption opcional)
//   - "document" / "file" → DocumentMessage (caption + filename)
//
// data son los bytes en claro (whatsmeow se encarga de cifrar y subir).
func (c *Conn) SendMedia(ctx context.Context, remoteJid, kind, mimetype, filename, caption string, data []byte) (string, error) {
	jid, err := parseJID(remoteJid)
	if err != nil {
		return "", err
	}
	msg, err := buildMediaMessage(ctx, c.client, kind, mimetype, filename, caption, data)
	if err != nil {
		return "", fmt.Errorf("build media: %w", err)
	}
	resp, err := c.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("send media: %w", err)
	}
	return resp.ID, nil
}

// Logout desvincula el dispositivo en WhatsApp pero conserva el row del device.
func (c *Conn) Logout(ctx context.Context) error {
	if c.client.Store.ID == nil {
		return nil
	}
	return c.client.Logout(ctx)
}

// Disconnect cierra la conexión limpiamente sin desvincular.
func (c *Conn) Disconnect() {
	c.mu.Lock()
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	c.mu.Unlock()
	c.client.Disconnect()
}
