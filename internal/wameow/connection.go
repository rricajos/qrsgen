// Package wameow envuelve whatsmeow (go.mau.fi/whatsmeow) y expone una
// interfaz limpia para el resto de la aplicación.
package wameow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registra el driver "pgx" para database/sql usado por whatsmeow's sqlstore

	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
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

// PictureHandler se dispara cuando whatsmeow emite *events.Picture (un usuario
// cambia su foto o un grupo su imagen). Permite al callsite reaccionar en
// tiempo real — útil para sincronizar avatares con el downstream sin
// esperar al siguiente mensaje. removed=true indica que la foto fue eliminada.
type PictureHandler func(ctx context.Context, instance string, jid types.JID, pictureID string, removed bool, r WAResolver)

// ChatPresenceHandler se dispara cuando whatsmeow emite *events.ChatPresence
// (un usuario está escribiendo o paró de escribir en un chat o grupo).
// composing=true indica typing activo, false indica que paró. media
// distingue text ("") vs audio ("audio"). Útil para propagar typing
// indicators al downstream.
type ChatPresenceHandler func(ctx context.Context, instance string, chat types.JID, sender types.JID, composing bool, media string, r WAResolver)

// ReceiptHandler se dispara cuando whatsmeow emite *events.Receipt
// (acknowledgement de delivery o read sobre mensajes que ENVIASTE).
// kind es el ReceiptType: "" (delivered), "read", "read-self", "played".
// Para "read" / "read-self" → el contacto abrió la conv y vio el msg.
// chat es el JID del chat/grupo; sender es quién leyó (en 1-on-1 es
// chat=sender). messageIDs son los WAIDs de los msgs marcados read.
type ReceiptHandler func(ctx context.Context, instance string, chat types.JID, sender types.JID, kind string, messageIDs []string, ts time.Time, r WAResolver)

// ContactHandler se dispara cuando whatsmeow emite *events.Contact (un
// contacto fue añadido, renombrado o eliminado desde otro device — vía
// la sincronización del address book del cliente principal).
//
// fullName / firstName: nuevos valores del contacto (vacío si fue
// eliminado o si solo se cambió el otro campo). El caller debe consultar
// el contact store via WAResolver.ContactName() para tener la verdad
// canónica — los campos aquí son una hint del Action.
type ContactHandler func(ctx context.Context, instance string, jid types.JID, fullName, firstName string, fromFullSync bool, r WAResolver)

// HistorySyncHandler se dispara cuando whatsmeow emite
// *events.HistorySync. Contiene blob de conversaciones+mensajes que
// el phone empuja al cliente Web (al parear, recents periódicos, o
// respuestas on-demand). qrsgen v0.46.0 lo usa para backfill de
// mensajes históricos a Chatwoot.
type HistorySyncHandler func(ctx context.Context, instance string, data *waHistorySync.HistorySync, r WAResolver)

// GroupInfoHandler se dispara cuando whatsmeow emite
// *events.GroupInfo (cambio de nombre/topic/locked/announce, miembros
// añadidos/expulsados, promote/demote, etc.). v0.47.0.
type GroupInfoHandler func(ctx context.Context, instance string, evt *events.GroupInfo, r WAResolver)

// JoinedGroupHandler se dispara cuando whatsmeow emite
// *events.JoinedGroup (te añaden a un grupo nuevo, o lo creas). v0.47.0.
type JoinedGroupHandler func(ctx context.Context, instance string, evt *events.JoinedGroup, r WAResolver)

// IdentityChangeHandler se dispara cuando whatsmeow emite
// *events.IdentityChange (otro usuario cambió su primary device y
// el código de seguridad ha cambiado). v0.47.0.
type IdentityChangeHandler func(ctx context.Context, instance string, evt *events.IdentityChange, r WAResolver)

// WAResolver expone consultas al estado local del cliente whatsmeow.
type WAResolver interface {
	// ContactName devuelve el nombre cacheado para un JID, o "" si no hay info.
	ContactName(jid types.JID) string
	// IsContactSaved devuelve true si el JID está guardado en la libreta de
	// contactos del dueño del bot — es decir, si tiene FullName o FirstName
	// en el contact store de whatsmeow. PushName solo (self-set por el
	// propio usuario) NO cuenta como "guardado". La cadena es:
	// Google Contacts → libreta del móvil → WhatsApp app → whatsmeow.
	IsContactSaved(jid types.JID) bool
	// GroupSubject devuelve el subject (nombre visible) de un grupo, o ("", false)
	// si no es un grupo o no se pudo resolver. Se cachea con TTL para evitar
	// pegarle al server WA en cada mensaje.
	GroupSubject(jid types.JID) (string, bool)
	// GetProfilePicture descarga la foto de perfil de un JID (usuario o grupo).
	// Devuelve (bytes, mime, nil) si hay foto, ([], "", nil) si no hay (caso
	// común, no es error), o (nil, "", err) si falla la consulta o descarga.
	// Hace round-trip al server WA + HTTP GET — usar con timeout corto.
	GetProfilePicture(ctx context.Context, jid types.JID) ([]byte, string, error)
	// GetProfilePictureID devuelve solo el ID (hash/version) de la foto
	// actual del JID, o ("", nil) si no hay foto. Cheap call — solo
	// metadata via GetProfilePictureInfo, no descarga la imagen. Útil
	// para comparar con un ID cacheado y decidir si toca re-sincronizar.
	GetProfilePictureID(ctx context.Context, jid types.JID) (string, error)
	// PNForLID intenta resolver el JID PN equivalente a un LID. Devuelve el JID y true si lo conoce.
	PNForLID(lid types.JID) (types.JID, bool)
	// LIDForPN intenta resolver el JID LID equivalente a un PN. Devuelve el JID y true si lo conoce.
	LIDForPN(pn types.JID) (types.JID, bool)
	// DownloadAny descarga el media adjunto a un mensaje E2E. Devuelve los bytes
	// desencriptados. Devuelve error si no hay media o falla el download.
	DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error)
	// GetSavedContacts devuelve todos los contactos saved (FullName o
	// FirstName no vacío) del contact store local, keyed por PN JID.
	// Valor: nombre canónico (FullName preferred, FirstName fallback).
	// Usado por el bulk reconcile del retroactive name update (v0.43.0)
	// para iterar la agenda al boot o vía endpoint admin.
	GetSavedContacts(ctx context.Context) (map[types.JID]string, error)
	// RedactedPhone devuelve el phone redactado (`+1∙∙∙∙∙∙∙∙80`)
	// que WhatsApp expone para LIDs en grupos cuando el contacto no
	// ha compartido su PN completo. Se almacena en
	// `ContactInfo.RedactedPhone` de whatsmeow. v0.53.1.
	RedactedPhone(jid types.JID) string
	// RefreshGroupLIDs fuerza un `GetGroupInfo` para que whatsmeow
	// actualice su LID store con las mappings de los participantes.
	// Side effect: tras esta llamada, `PNForLID` de los participantes
	// puede empezar a funcionar. v0.53.1.
	RefreshGroupLIDs(ctx context.Context, group types.JID) error
	// RequestHistorySync envía a la primary device del usuario una
	// petición de N msgs más de histórico para `chat`. La respuesta
	// llega como *events.HistorySync con type ON_DEMAND. v0.46.0.
	// `count` máximo recomendado por WA: 50.
	RequestHistorySync(ctx context.Context, chat types.JID, lastMsgID string, lastMsgFromMe bool, lastMsgTimestamp time.Time, count int) error
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
	name           string
	client         *whatsmeow.Client
	logger         *slog.Logger
	onMessage      MessageHandler
	onLifecycle    LifecycleCallback
	onPicture      PictureHandler
	onChatPresence ChatPresenceHandler
	onReceipt      ReceiptHandler
	onContact      ContactHandler
	onHistorySync  HistorySyncHandler
	onGroupInfo    GroupInfoHandler
	onJoinedGroup  JoinedGroupHandler
	onIdentityChg  IdentityChangeHandler

	mu        sync.RWMutex
	lastQRPNG []byte
	lastQRAt  time.Time
	qrCancel  context.CancelFunc

	// lifecycleCtx se cancela en Disconnect(). Lo usan los handlers de
	// eventos que disparan trabajo asíncrono (notablemente HistorySync,
	// que itera miles de msgs en goroutine separada) para que al borrar
	// o desconectar la instancia los goroutines vivos paren sin spammear
	// el downstream con 404s. v0.53.3.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	groupSubjMu    sync.RWMutex
	groupSubjCache map[string]groupSubjectEntry
}

// groupSubjectEntry guarda el subject resuelto + el deadline del TTL.
// Cacheamos también las negativas (name=="") para no machacar el server
// si el grupo no resuelve por X motivo, con TTL más corto.
type groupSubjectEntry struct {
	name  string
	until time.Time
}

// Group subject cache TTLs. Tras un cambio de nombre en el grupo, los
// mensajes posteriores pueden seguir mostrando el nombre antiguo durante
// hasta groupSubjectTTL. Vale la pena por la reducción de round-trips.
//
// groupSubjectFetchTimeout limita lo que esperamos al server WA por un
// GetGroupInfo. Si vence, cacheamos negativo (TTL corto) y dejamos que
// el mensaje siga sin subject — mejor que bloquear el handler.
const (
	groupSubjectTTL          = 10 * time.Minute
	groupSubjectNegTTL       = 1 * time.Minute
	groupSubjectFetchTimeout = 2 * time.Second
)

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
	// v0.53.3: envolvemos el waLog para filtrar el ruido upstream conocido
	// (ver noisyWarnPatterns en filtered_log.go). Todo lo demás pasa intacto.
	client := whatsmeow.NewClient(device, newFilteredWALog(waLog.Stdout("Client:"+name, "INFO", true)))
	// Auto-reconnect agresivo: si el WebSocket se cae, whatsmeow lo reabre solo.
	// Sin esto, una pérdida transitoria deja la instancia disconnected hasta
	// que algo externo (watchdog, restart) la rearranque.
	client.EnableAutoReconnect = true
	client.InitialAutoReconnect = true
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	c := &Conn{
		name:            name,
		client:          client,
		logger:          logger.With("instance", name),
		onMessage:       onMsg,
		onLifecycle:     onLifecycle,
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		groupSubjCache:  make(map[string]groupSubjectEntry),
	}
	client.AddEventHandler(c.handle)
	return c
}

// Name devuelve el nombre lógico de la instancia.
func (c *Conn) Name() string { return c.name }

// SetPictureHandler registra el callback para *events.Picture
// (cambios de foto de perfil de usuarios o grupos). Llamar antes de
// Connect — no es seguro modificarlo en runtime sin sincronización.
func (c *Conn) SetPictureHandler(h PictureHandler) { c.onPicture = h }

// SetChatPresenceHandler registra el callback para *events.ChatPresence
// (typing/composing indicators). Llamar antes de Connect.
func (c *Conn) SetChatPresenceHandler(h ChatPresenceHandler) { c.onChatPresence = h }

// SetReceiptHandler registra el callback para *events.Receipt
// (delivery / read receipts sobre mensajes que envió este device).
func (c *Conn) SetReceiptHandler(h ReceiptHandler) { c.onReceipt = h }

// SetContactHandler registra el callback para *events.Contact
// (cambios en el address book del cliente principal sincronizados via
// Multi-Device). Útil para retroactive update de mensajes ya posteados
// al downstream cuando el dueño del bot añade un contacto a su agenda.
func (c *Conn) SetContactHandler(h ContactHandler) { c.onContact = h }

// SetHistorySyncHandler registra el callback para *events.HistorySync
// (v0.46.0 history import). Llamar antes de Connect/Bootstrap.
func (c *Conn) SetHistorySyncHandler(h HistorySyncHandler) { c.onHistorySync = h }

// SetGroupInfoHandler registra el callback para *events.GroupInfo
// (v0.47.0 group events). Llamar antes de Connect/Bootstrap.
func (c *Conn) SetGroupInfoHandler(h GroupInfoHandler) { c.onGroupInfo = h }

// SetJoinedGroupHandler registra el callback para *events.JoinedGroup.
// Llamar antes de Connect/Bootstrap.
func (c *Conn) SetJoinedGroupHandler(h JoinedGroupHandler) { c.onJoinedGroup = h }

// SetIdentityChangeHandler registra el callback para *events.IdentityChange.
// Llamar antes de Connect/Bootstrap.
func (c *Conn) SetIdentityChangeHandler(h IdentityChangeHandler) { c.onIdentityChg = h }

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

// RedactedPhone consulta el contact store local y devuelve
// `RedactedPhone` (`+1∙∙∙∙∙∙∙∙80`) si está disponible. WhatsApp lo
// expone para LIDs en grupos cuando el contacto opta por privacidad.
// v0.53.1.
func (c *Conn) RedactedPhone(jid types.JID) string {
	if c.client == nil || c.client.Store == nil || c.client.Store.Contacts == nil {
		return ""
	}
	info, err := c.client.Store.Contacts.GetContact(context.Background(), jid.ToNonAD())
	if err != nil || !info.Found {
		return ""
	}
	return info.RedactedPhone
}

// RefreshGroupLIDs llama a GetGroupInfo cuyo side effect es popular
// el LID store con los participantes. Tras esto, PNForLID puede
// resolver LIDs que antes faltaban. v0.53.1.
func (c *Conn) RefreshGroupLIDs(ctx context.Context, group types.JID) error {
	if c.client == nil {
		return fmt.Errorf("refresh group lids: client nil")
	}
	if group.Server != types.GroupServer {
		return nil
	}
	_, err := c.client.GetGroupInfo(ctx, group)
	return err
}

// GetSavedContacts itera el contact store local y devuelve un map
// {JID PN → nombre canónico} solo con entries que tienen FullName o
// FirstName (el criterio de "saved" usado en todo el bridge —
// equivalente a `IsContactSaved` aplicado en bulk). Usado por el
// bulk reconcile del retroactive name update (v0.43.0).
//
// Llamada potencialmente cara — el store puede tener miles de entries.
// El caller debe usar timeout razonable + correr en background.
func (c *Conn) GetSavedContacts(ctx context.Context) (map[types.JID]string, error) {
	if c.client == nil || c.client.Store == nil || c.client.Store.Contacts == nil {
		return nil, fmt.Errorf("no contact store")
	}
	all, err := c.client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[types.JID]string)
	for jid, info := range all {
		name := info.FullName
		if name == "" {
			name = info.FirstName
		}
		if name == "" {
			continue
		}
		out[jid] = name
	}
	return out, nil
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

// GroupSubject devuelve el nombre del grupo cacheado o lo resuelve via
// client.GetGroupInfo() si no está en cache (o expiró). Cachea positivos
// y negativos con TTLs distintos.
//
// Para JIDs que no son @g.us devuelve ("", false) sin tocar el server.
func (c *Conn) GroupSubject(jid types.JID) (string, bool) {
	if c.client == nil || jid.Server != types.GroupServer {
		return "", false
	}
	key := jid.ToNonAD().String()

	c.groupSubjMu.RLock()
	if e, ok := c.groupSubjCache[key]; ok && time.Now().Before(e.until) {
		c.groupSubjMu.RUnlock()
		return e.name, e.name != ""
	}
	c.groupSubjMu.RUnlock()

	fetchCtx, cancel := context.WithTimeout(context.Background(), groupSubjectFetchTimeout)
	defer cancel()
	info, err := c.client.GetGroupInfo(fetchCtx, jid.ToNonAD())
	if err != nil || info == nil {
		c.groupSubjMu.Lock()
		c.groupSubjCache[key] = groupSubjectEntry{name: "", until: time.Now().Add(groupSubjectNegTTL)}
		c.groupSubjMu.Unlock()
		return "", false
	}
	// info.Name es el subject del grupo (promoted field de GroupName embebido en GroupInfo).
	name := info.Name
	ttl := groupSubjectTTL
	if name == "" {
		ttl = groupSubjectNegTTL
	}
	c.groupSubjMu.Lock()
	c.groupSubjCache[key] = groupSubjectEntry{name: name, until: time.Now().Add(ttl)}
	c.groupSubjMu.Unlock()
	return name, name != ""
}

// GetProfilePicture descarga la foto de perfil completa del JID (user o group).
// Hace dos calls: GetProfilePictureInfo (round-trip al server WA) + HTTP GET
// a la URL devuelta. Timeout interno de 10s — el caller decide su propio TTL.
//
// Para JIDs sin foto, devuelve ([], "", nil) — no es error, es estado válido.
// Errores solo cuando la query o el download fallan inesperadamente.
func (c *Conn) GetProfilePicture(ctx context.Context, jid types.JID) ([]byte, string, error) {
	if c.client == nil {
		return nil, "", fmt.Errorf("get profile picture: client nil")
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	info, err := c.client.GetProfilePictureInfo(fetchCtx, jid.ToNonAD(), &whatsmeow.GetProfilePictureParams{})
	if err != nil {
		return nil, "", fmt.Errorf("get profile picture info: %w", err)
	}
	if info == nil || info.URL == "" {
		return nil, "", nil // no avatar configurado
	}

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, info.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}

	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/jpeg" // default para fotos WA
	}
	return data, mime, nil
}

// IsContactSaved devuelve true si el JID está guardado en la libreta de
// contactos del bot — es decir, si tiene FullName o FirstName en el
// contact store de whatsmeow. El PushName (auto-asignado por el propio
// usuario en su WhatsApp) NO cuenta como "guardado".
//
// La cadena para que esto funcione es:
// Google Contacts → libreta del móvil → app WhatsApp → whatsmeow store.
// Si en algún punto la sincronización falla, devolverá false aunque
// el contacto exista en Google Contacts.
//
// Para JIDs de grupos siempre devuelve false (los grupos no tienen
// FullName/FirstName en el contact store, ese campo es para personas).
func (c *Conn) IsContactSaved(jid types.JID) bool {
	if c.client == nil || c.client.Store == nil || c.client.Store.Contacts == nil {
		return false
	}
	info, err := c.client.Store.Contacts.GetContact(context.Background(), jid.ToNonAD())
	if err != nil || !info.Found {
		return false
	}
	return info.FullName != "" || info.FirstName != ""
}

// GetProfilePictureID es una variante cheap de GetProfilePicture que solo
// devuelve el ID (versión/hash) de la foto. No descarga la imagen. Útil
// para comparar contra un ID cacheado y decidir si toca re-sincronizar.
func (c *Conn) GetProfilePictureID(ctx context.Context, jid types.JID) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("get profile picture id: client nil")
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := c.client.GetProfilePictureInfo(fetchCtx, jid.ToNonAD(), &whatsmeow.GetProfilePictureParams{})
	if err != nil {
		return "", fmt.Errorf("get profile picture info: %w", err)
	}
	if info == nil {
		return "", nil
	}
	return info.ID, nil
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
//
// Implementado encima de client.Download (la API recomendada por whatsmeow);
// reemplaza el deprecated client.DownloadAny.
func (c *Conn) DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	if c.client == nil || msg == nil {
		return nil, fmt.Errorf("download: cliente o mensaje nil")
	}
	var dl whatsmeow.DownloadableMessage
	switch {
	case msg.ImageMessage != nil:
		dl = msg.ImageMessage
	case msg.AudioMessage != nil:
		dl = msg.AudioMessage
	case msg.VideoMessage != nil:
		dl = msg.VideoMessage
	case msg.DocumentMessage != nil:
		dl = msg.DocumentMessage
	case msg.StickerMessage != nil:
		dl = msg.StickerMessage
	default:
		return nil, fmt.Errorf("download: no hay media descargable en el mensaje")
	}
	return c.client.Download(ctx, dl)
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
	case *events.Picture:
		if c.onPicture != nil {
			c.onPicture(context.Background(), c.name, evt.JID, evt.PictureID, evt.Remove, c)
		}
	case *events.ChatPresence:
		if c.onChatPresence != nil {
			composing := evt.State == types.ChatPresenceComposing
			c.onChatPresence(context.Background(), c.name, evt.Chat, evt.Sender, composing, string(evt.Media), c)
		}
	case *events.Receipt:
		if c.onReceipt != nil {
			ids := make([]string, 0, len(evt.MessageIDs))
			for _, id := range evt.MessageIDs {
				ids = append(ids, string(id))
			}
			c.onReceipt(context.Background(), c.name, evt.Chat, evt.Sender, string(evt.Type), ids, evt.Timestamp, c)
		}
	case *events.Contact:
		if c.onContact != nil {
			full := ""
			first := ""
			if evt.Action != nil {
				full = evt.Action.GetFullName()
				first = evt.Action.GetFirstName()
			}
			c.onContact(context.Background(), c.name, evt.JID, full, first, evt.FromFullSync, c)
		}
	case *events.HistorySync:
		if c.onHistorySync != nil && evt.Data != nil {
			// v0.46.0: blob de mensajes históricos (al parear o
			// respuesta de BuildHistorySyncRequest). El handler decide
			// si procesar según config — puede ser muy grande, así
			// que ejecutamos en goroutine para no bloquear el event
			// loop de whatsmeow.
			// v0.53.3: usar lifecycleCtx en vez de context.Background()
			// para que Disconnect() cancele este goroutine — antes
			// seguía iterando msgs durante minutos tras Delete y
			// generaba 404s en el downstream.
			go c.onHistorySync(c.lifecycleCtx, c.name, evt.Data, c)
		}
	case *events.GroupInfo:
		if c.onGroupInfo != nil {
			c.onGroupInfo(context.Background(), c.name, evt, c)
		}
	case *events.JoinedGroup:
		if c.onJoinedGroup != nil {
			c.onJoinedGroup(context.Background(), c.name, evt, c)
		}
	case *events.IdentityChange:
		if c.onIdentityChg != nil {
			c.onIdentityChg(context.Background(), c.name, evt, c)
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

// SendTextReply envía un mensaje de texto como reply nativo de WhatsApp.
// quotedWAID es el ID del mensaje al que se responde. quotedSenderJID
// es el JID del autor del mensaje citado (vacío en 1:1; obligatorio
// para que el preview del quote enlace bien en grupos). quotedText
// es el contenido del mensaje citado (whatsmeow lo incluye en
// ContextInfo.QuotedMessage para que el cliente receptor renderice
// el preview).
//
// Si quotedWAID es "", se comporta como SendText pelado. v0.44.0.
func (c *Conn) SendTextReply(ctx context.Context, remoteJid, content, quotedWAID, quotedSenderJID, quotedText string) (string, error) {
	if quotedWAID == "" {
		return c.SendText(ctx, remoteJid, content)
	}
	jid, err := parseJID(remoteJid)
	if err != nil {
		return "", err
	}
	msg := replyTextMessage(content, quotedWAID, quotedSenderJID, quotedText)
	resp, err := c.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("send reply: %w", err)
	}
	return resp.ID, nil
}

// GroupInfo encapsula la info de un grupo en formato JSON-friendly.
// Subset de `types.GroupInfo` con solo lo que un cliente HTTP necesita.
type GroupInfo struct {
	JID          string             `json:"jid"`
	Subject      string             `json:"subject"`
	Topic        string             `json:"topic"`
	IsLocked     bool               `json:"is_locked"`
	IsAnnounce   bool               `json:"is_announce"`
	IsEphemeral  bool               `json:"is_ephemeral"`
	Participants []GroupParticipant `json:"participants"`
}

// GroupParticipant es un miembro de un grupo + su rol.
type GroupParticipant struct {
	JID         string `json:"jid"`
	PhoneNumber string `json:"phone_number,omitempty"` // si JID es LID, lo resolvemos
	IsAdmin     bool   `json:"is_admin"`
	IsSuperAdmin bool  `json:"is_super_admin"`
}

// GroupInfo obtiene la info de un grupo desde WhatsApp. Round-trip
// al server. v0.48.0.
func (c *Conn) GroupInfo(ctx context.Context, jid types.JID) (*GroupInfo, error) {
	if c.client == nil {
		return nil, fmt.Errorf("group info: client nil")
	}
	info, err := c.client.GetGroupInfo(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf("get group info: %w", err)
	}
	out := &GroupInfo{
		JID:         info.JID.String(),
		Subject:     info.Name,
		Topic:       info.Topic,
		IsLocked:    info.IsLocked,
		IsAnnounce:  info.IsAnnounce,
		IsEphemeral: info.IsEphemeral,
	}
	for _, p := range info.Participants {
		gp := GroupParticipant{
			JID:          p.JID.String(),
			IsAdmin:      p.IsAdmin,
			IsSuperAdmin: p.IsSuperAdmin,
		}
		if p.JID.Server == types.HiddenUserServer {
			if pn, ok := c.PNForLID(p.JID); ok {
				gp.PhoneNumber = "+" + pn.User
			}
		} else if p.JID.Server == types.DefaultUserServer {
			gp.PhoneNumber = "+" + p.JID.User
		}
		out.Participants = append(out.Participants, gp)
	}
	return out, nil
}

// SetGroupName cambia el nombre (subject) de un grupo. Requiere que
// el bot sea admin del grupo. v0.48.0.
func (c *Conn) SetGroupName(ctx context.Context, jid types.JID, name string) error {
	if c.client == nil {
		return fmt.Errorf("set name: client nil")
	}
	return c.client.SetGroupName(ctx, jid, name)
}

// SetGroupTopic cambia el topic (descripción) de un grupo. v0.50.0.
// `previousID` y `newID` son opcionales (whatsmeow los autogenera si
// vacío). Requiere bot admin.
func (c *Conn) SetGroupTopic(ctx context.Context, jid types.JID, topic string) error {
	if c.client == nil {
		return fmt.Errorf("set topic: client nil")
	}
	return c.client.SetGroupTopic(ctx, jid, "", "", topic)
}

// SetGroupLocked controla si solo admins pueden editar la info del
// grupo. v0.50.0.
func (c *Conn) SetGroupLocked(ctx context.Context, jid types.JID, locked bool) error {
	if c.client == nil {
		return fmt.Errorf("set locked: client nil")
	}
	return c.client.SetGroupLocked(ctx, jid, locked)
}

// SetGroupAnnounce controla el modo anuncio (solo admins envían
// mensajes). v0.50.0.
func (c *Conn) SetGroupAnnounce(ctx context.Context, jid types.JID, announce bool) error {
	if c.client == nil {
		return fmt.Errorf("set announce: client nil")
	}
	return c.client.SetGroupAnnounce(ctx, jid, announce)
}

// CreateGroup crea un grupo nuevo. name max 25 chars, participants
// debe incluir al menos 1 JID (el bot se añade implícitamente).
// v0.50.0. Devuelve el JID del grupo creado.
func (c *Conn) CreateGroup(ctx context.Context, name string, participants []types.JID) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("create group: client nil")
	}
	if len(participants) == 0 {
		return "", fmt.Errorf("create group: at least 1 participant required")
	}
	info, err := c.client.CreateGroup(ctx, whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: participants,
	})
	if err != nil {
		return "", err
	}
	return info.JID.String(), nil
}

// LeaveGroup hace que el bot abandone el grupo. v0.50.0.
func (c *Conn) LeaveGroup(ctx context.Context, jid types.JID) error {
	if c.client == nil {
		return fmt.Errorf("leave group: client nil")
	}
	return c.client.LeaveGroup(ctx, jid)
}

// UpdateGroupParticipants añade, expulsa, promueve o degrada miembros
// de un grupo. action ∈ {"add","remove","promote","demote"}.
// Requiere que el bot sea admin. v0.48.0.
func (c *Conn) UpdateGroupParticipants(ctx context.Context, jid types.JID, action string, participants []types.JID) error {
	if c.client == nil {
		return fmt.Errorf("update participants: client nil")
	}
	var change whatsmeowParticipantChange
	switch action {
	case "add":
		change = "add"
	case "remove":
		change = "remove"
	case "promote":
		change = "promote"
	case "demote":
		change = "demote"
	default:
		return fmt.Errorf("invalid action %q (must be add/remove/promote/demote)", action)
	}
	_, err := c.client.UpdateGroupParticipants(ctx, jid, participants, whatsmeowParticipantChangeProxy(change))
	return err
}

// whatsmeowParticipantChange es alias del tipo de whatsmeow para
// pasarlo correctamente al método público.
type whatsmeowParticipantChange string

// whatsmeowParticipantChangeProxy hace el cast al tipo público de
// whatsmeow. Aislado para evitar import circular del package en
// tests.
func whatsmeowParticipantChangeProxy(c whatsmeowParticipantChange) whatsmeow.ParticipantChange {
	return whatsmeow.ParticipantChange(c)
}

// RequestHistorySync pide a la primary device del usuario `count`
// mensajes más de histórico para el chat dado, anteriores al
// `lastMsgID` indicado. La respuesta llega vía el HistorySyncHandler
// como *events.HistorySync con type ON_DEMAND. v0.46.0.
//
// `lastMsgID` debe ser un msgID conocido en el chat — qrsgen lo
// resuelve normalmente desde el contact store o desde un msg
// reciente del chat. `count` máx recomendado: 50.
func (c *Conn) RequestHistorySync(ctx context.Context, chat types.JID, lastMsgID string, lastMsgFromMe bool, lastMsgTimestamp time.Time, count int) error {
	if c.client == nil {
		return fmt.Errorf("request history: client nil")
	}
	if count <= 0 || count > 200 {
		count = 50
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chat,
			IsFromMe: lastMsgFromMe,
		},
		ID:        types.MessageID(lastMsgID),
		Timestamp: lastMsgTimestamp,
	}
	msg := c.client.BuildHistorySyncRequest(info, count)
	_, err := c.client.SendPeerMessage(ctx, msg)
	return err
}

// MarkRead manda un read receipt a WhatsApp por los message IDs
// indicados. El cliente del otro lado ve doble check azul en esos msgs.
//
// chat es el JID del chat/grupo donde están los msgs. sender es el JID
// del autor de los msgs (en 1-on-1 chat == sender == el contacto;
// en grupos, sender es el JID del participante que envió cada msg).
//
// ts es el timestamp del read receipt — típicamente time.Now() cuando
// el agente abrió la conv en el downstream.
//
// WhatsApp es idempotente: llamar MarkRead dos veces sobre el mismo
// WAID no genera doble notificación al cliente.
//
// Desde v0.39.0.
func (c *Conn) MarkRead(ctx context.Context, chat, sender string, messageIDs []string, ts time.Time) error {
	if c.client == nil {
		return fmt.Errorf("mark read: client nil")
	}
	if len(messageIDs) == 0 {
		return nil
	}
	chatJID, err := parseJID(chat)
	if err != nil {
		return fmt.Errorf("parse chat jid: %w", err)
	}
	senderJID := chatJID
	if sender != "" && sender != chat {
		senderJID, err = parseJID(sender)
		if err != nil {
			return fmt.Errorf("parse sender jid: %w", err)
		}
	}
	ids := make([]types.MessageID, 0, len(messageIDs))
	for _, id := range messageIDs {
		ids = append(ids, types.MessageID(id))
	}
	return c.client.MarkRead(ctx, ids, ts, chatJID, senderJID)
}

// SendMediaReply envía un media como reply nativo a un msg existente.
// Idéntico a SendMedia + populates ContextInfo. v0.51.0.
//
// Si quotedWAID == "", se comporta como SendMedia pelado.
func (c *Conn) SendMediaReply(ctx context.Context, remoteJid, kind, mimetype, filename, caption string, data []byte, quotedWAID, quotedSenderJID, quotedText string) (string, error) {
	jid, err := parseJID(remoteJid)
	if err != nil {
		return "", err
	}
	msg, err := buildMediaMessageWithReply(ctx, c.client, kind, mimetype, filename, caption, data, quotedWAID, quotedSenderJID, quotedText)
	if err != nil {
		return "", fmt.Errorf("build media reply: %w", err)
	}
	resp, err := c.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("send media reply: %w", err)
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
// v0.53.3: cancela también lifecycleCtx tras la desconexión, lo que
// para cualquier goroutine asíncrono lanzado por handlers (notablemente
// HistorySync) que de otra forma seguiría posteando al downstream.
func (c *Conn) Disconnect() {
	c.mu.Lock()
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	c.mu.Unlock()
	c.client.Disconnect()
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
	}
}
