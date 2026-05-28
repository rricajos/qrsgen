package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// InboxResolver resuelve el inbox_id de downstream para una instancia dada.
type InboxResolver func(instance string) int

// Incoming sincroniza un mensaje observado en WhatsApp (incoming o fromMe outgoing)
// a downstream.
//
// El `ds` es un Router (interfaz) — puede ser un *downstream.Client directo
// (single-downstream) o un *downstream.Registry (multi-downstream con
// routing por owner_tag). El código de los handlers usa `ds.For(ctx, instance)`
// uniformemente.
type Incoming struct {
	ds      downstream.Router
	dedup   *Deduper
	logger  *slog.Logger
	resolve InboxResolver
	usage   UsageRecorder

	// groupPrefixSender controla si los mensajes incoming de un grupo se
	// posteán a downstream con prefijo de remitente ("**~Name** · _+CC ..._").
	// Default true porque sin él, multi-sender en una misma conv del downstream
	// son indistinguibles. Se puede desactivar con QRSGEN_GROUP_PREFIX_SENDER=false.
	groupPrefixSender bool

	// groupTracker decide si emitir el header en cada mensaje del grupo.
	// Suprime el header si el sender es el mismo del mensaje anterior
	// (dentro del TTL configurable). nil = desactivado, header siempre.
	groupTracker *groupSenderTracker

	// avatarSync controla si qrsgen intenta sincronizar la foto de perfil
	// de WhatsApp al avatar del downstream. Aplica tanto al crear el
	// contact (primer sync) como al re-chequear contactos existentes
	// (refresh tras cambio en WA). Fire-and-forget, no bloquea el
	// flujo del mensaje. Default true.
	avatarSync bool

	// avatarTracker decide cuándo re-chequear el avatar de un JID
	// (TTL por defecto 24h) y evita races entre goroutines concurrentes
	// que reciben mensajes del mismo grupo en burst. nil = solo se sincroniza
	// al crear el contacto (sin refresh, modo v0.31.0).
	avatarTracker *avatarTracker

	// reactionsSync controla si las reacciones a mensajes de WhatsApp se
	// propagan al downstream como mensajes nuevos con formato
	// "**~Name** reaccionó con 👍". Default true. Desde v0.33.0.
	reactionsSync bool
}

// NewIncomingDynamic crea un handler con resolución dinámica de inbox por instancia.
func NewIncomingDynamic(ds downstream.Router, dedup *Deduper, logger *slog.Logger, resolve InboxResolver) *Incoming {
	return &Incoming{ds: ds, dedup: dedup, logger: logger, resolve: resolve, groupPrefixSender: true, avatarSync: true, reactionsSync: true}
}

// SetReactionsSync activa o desactiva la propagación de reacciones WhatsApp
// al downstream. Default true. Setear a false ignora todos los eventos
// de reacción — no se postea nada en la conv del downstream.
func (i *Incoming) SetReactionsSync(v bool) { i.reactionsSync = v }

// SetUsage attaches a usage recorder. Pass nil to disable.
func (i *Incoming) SetUsage(u UsageRecorder) { i.usage = u }

// SetGroupPrefixSender controla el prefijo de remitente en mensajes de grupo.
func (i *Incoming) SetGroupPrefixSender(v bool) { i.groupPrefixSender = v }

// SetGroupHeaderTTL activa la supresión de header en mensajes consecutivos
// del mismo sender dentro del TTL dado. ttl=0 desactiva la feature
// (header siempre).
func (i *Incoming) SetGroupHeaderTTL(ttl time.Duration) {
	if ttl <= 0 {
		i.groupTracker = nil
		return
	}
	i.groupTracker = newGroupSenderTracker(ttl)
}

// SetAvatarSync controla si tras crear un contacto en downstream qrsgen
// dispara una sincronización (background) de la foto WhatsApp como avatar
// del contacto. Default true.
func (i *Incoming) SetAvatarSync(v bool) { i.avatarSync = v }

// SetAvatarRefreshTTL activa el refresh periódico del avatar (mismo TTL
// para todos los JIDs). Si > 0, contactos existentes se re-chequean
// cada TTL para detectar cambios de foto en WhatsApp; 0 = sin refresh
// (solo sync al crear contact, como en v0.31.0).
func (i *Incoming) SetAvatarRefreshTTL(ttl time.Duration) {
	if ttl <= 0 {
		i.avatarTracker = nil
		return
	}
	i.avatarTracker = newAvatarTracker(ttl)
}

// ResyncResult resume el bulk-resync de avatars.
type ResyncResult struct {
	Instance string `json:"instance"`
	Scanned  int    `json:"scanned"`  // contactos totales iterados
	Skipped  int    `json:"skipped"`  // identifier no parseable como JID
	Queued   int    `json:"queued"`   // syncs lanzados en background
	Pages    int    `json:"pages"`    // páginas consultadas en downstream
}

// ResyncInstanceAvatars itera todos los contactos del inbox asociado a la
// instancia y dispara un avatar sync por cada uno que tenga identifier
// parseable como JID. BYPASS del tracker — cada contacto se chequea sí o sí.
//
// Útil para backfillear contactos viejos (creados antes de v0.31.0) que
// todavía tienen letter-avatar autogenerado y no han recibido mensajes
// recientes que disparen el sync vía sync().
//
// La iteración bloquea hasta terminar pero los syncs por contacto son
// goroutines fire-and-forget. Para inboxes grandes (>1k contactos)
// considera invocar con cuidado — N goroutines concurrentes pueden
// estresar tanto al server WA como al downstream.
func (i *Incoming) ResyncInstanceAvatars(ctx context.Context, instance string, r wameow.WAResolver, inboxID int) (ResyncResult, error) {
	result := ResyncResult{Instance: instance}
	if !i.avatarSync || r == nil {
		return result, fmt.Errorf("avatar sync disabled")
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return result, fmt.Errorf("downstream not configured for instance %s", instance)
	}
	if inboxID <= 0 {
		return result, fmt.Errorf("inbox id required (got %d)", inboxID)
	}

	page := 1
	for {
		contacts, hasMore, err := ds.ListContactsByInbox(ctx, inboxID, page)
		if err != nil {
			return result, fmt.Errorf("list contacts page %d: %w", page, err)
		}
		result.Pages++
		for _, contact := range contacts {
			result.Scanned++
			jid, parseErr := types.ParseJID(contact.Identifier)
			if parseErr != nil {
				result.Skipped++
				continue
			}
			// Forzar sync ignorando tracker — copiar contact.ID a variable
			// local para que la goroutine no comparta el loop var (Go 1.22+
			// ya no lo necesita pero por claridad).
			cid := contact.ID
			j := jid
			if i.avatarTracker != nil {
				// Forzar re-check: limpiar LastID para que syncAvatar
				// detecte cambio y descargue.
				i.avatarTracker.UpdateID(instance, j.String(), "")
			}
			go i.syncAvatar(ds, r, cid, j, instance)
			result.Queued++
		}
		if !hasMore {
			break
		}
		page++
		// Safety: no más de 200 páginas (3000 contactos). Si tienes más,
		// implementa cursor en lugar de page-based.
		if page > 200 {
			i.logger.Warn("resync avatars: page cap reached, stopping",
				"instance", instance, "pages", page, "scanned", result.Scanned)
			break
		}
	}
	i.logger.Info("resync avatars done",
		"instance", instance, "scanned", result.Scanned,
		"skipped", result.Skipped, "queued", result.Queued, "pages", result.Pages)
	return result, nil
}

// handleReaction procesa un *events.Message que contiene un ReactionMessage.
// Postea un mensaje incoming al downstream con formato:
//
//	**~Jean Paul** reaccionó con 👍
//
// o, si la reacción fue eliminada (text vacío):
//
//	**~Jean Paul** quitó su reacción
//
// Aplica el mismo resolver de nombre que applyGroupSenderPrefix (incluyendo
// IsContactSaved). Si no encuentra la conv (porque el contacto no existe
// aún en el downstream — no había mandado mensajes), no hace nada.
//
// Reacciones del propio bot (fromMe) se ignoran: el agente reaccionando
// desde el downstream debería postearse vía el flujo outgoing si en algún
// momento se añade soporte.
func (i *Incoming) handleReaction(ctx context.Context, instance string, msg *events.Message, r wameow.WAResolver) {
	if !i.reactionsSync {
		return
	}
	if msg.Info.IsFromMe {
		return
	}
	reaction := msg.Message.GetReactionMessage()
	if reaction == nil {
		return
	}
	emoji := reaction.GetText()
	targetMsgID := reaction.GetKey().GetID()

	rs := resolveSender(msg, r)
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}

	identifier := rs.primaryJID
	contact, err := findContactByIdentifier(ctx, ds, identifier, rs.phone)
	if err != nil {
		i.logger.Warn("reaction sync: find contact failed",
			"err", err, "jid", identifier, "instance", instance)
		return
	}
	if contact == nil {
		// Sin contact = sin conv. No creamos contactos para reacciones
		// sueltas — esperamos al primer mensaje real.
		return
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		return
	}

	// Resolver nombre del sender con la misma lógica que el group prefix.
	name := ""
	saved := false
	if r != nil {
		name = r.ContactName(msg.Info.Sender.ToNonAD())
		saved = r.IsContactSaved(msg.Info.Sender.ToNonAD())
		if msg.Info.Sender.Server == types.HiddenUserServer && (name == "" || !saved) {
			if pn, ok := r.PNForLID(msg.Info.Sender.ToNonAD()); ok {
				if name == "" {
					name = r.ContactName(pn)
				}
				if !saved {
					saved = r.IsContactSaved(pn)
				}
			}
		}
	}
	if name == "" {
		name = msg.Info.PushName
	}
	if name == "" {
		name = "alguien"
	}

	var content string
	isGroup := msg.Info.Chat.Server == types.GroupServer
	prefix := "**~" + name + "**"
	if !saved && isGroup {
		// En grupos con sender desconocido, incluir teléfono para context.
		// 1-on-1 no lo necesita porque la conv ya es ese contacto.
		phone := ""
		switch msg.Info.Sender.Server {
		case types.DefaultUserServer:
			phone = msg.Info.Sender.User
		case types.HiddenUserServer:
			if r != nil {
				if pn, ok := r.PNForLID(msg.Info.Sender.ToNonAD()); ok {
					phone = pn.User
				}
			}
		}
		if phone != "" {
			prefix = prefix + " `" + formatE164(phone) + "`"
		}
	}

	if emoji == "" {
		content = prefix + " _quitó su reacción_"
	} else {
		content = prefix + " reaccionó con " + emoji
	}

	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    "incoming",
		SourceID:       "WAID:reaction:" + msg.Info.ID,
	})
	if err != nil {
		i.logger.Warn("reaction sync: post failed",
			"err", err, "conv_id", conv.ID, "target_msg_id", targetMsgID)
		return
	}
	i.logger.Info("reaction synced",
		"conv_id", conv.ID, "emoji", emoji, "target_msg_id", targetMsgID,
		"sender", name, "instance", instance)
}

// HandlePictureChange reacciona a *events.Picture: alguien (user o grupo)
// cambió su foto de perfil. Encuentra el contact correspondiente en el
// downstream y dispara avatar sync. A diferencia de maybeAvatarSync,
// IGNORA el TTL — el evento es la señal canónica de que cambió.
//
// Si el contact no existe en downstream todavía, no hace nada: al primer
// mensaje del JID, sync()→CreateContact→maybeAvatarSync hará el sync inicial.
func (i *Incoming) HandlePictureChange(ctx context.Context, instance string, jid types.JID, pictureID string, removed bool, r wameow.WAResolver) {
	if !i.avatarSync || r == nil {
		return
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}
	identifier := jid.ToNonAD().String()

	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil {
		i.logger.Warn("picture event: find contact failed",
			"err", err, "jid", identifier)
		return
	}
	if contact == nil {
		// El contact no existe en downstream. Al primer mensaje de este
		// JID, sync() lo creará y disparará el avatar sync inicial.
		return
	}

	// Reset del LastID en el tracker para forzar re-descarga: el evento
	// nos dice que cambió, así que el cached ID está stale.
	if i.avatarTracker != nil {
		i.avatarTracker.UpdateID(instance, identifier, "")
	}

	i.logger.Info("picture changed event — forcing avatar resync",
		"jid", identifier, "new_picture_id", pictureID, "removed", removed,
		"contact_id", contact.ID)

	go i.syncAvatar(ds, r, contact.ID, jid, instance)
}

// maybeAvatarSync decide si lanzar el sync de avatar para un JID. Si el
// tracker dice que toca (primera vez, o TTL expirado), spawnea goroutine
// fire-and-forget. Si no, no hace nada.
//
// Llamarlo tanto al crear contacto nuevo COMO al encontrar contacto
// existente — el tracker se encarga de la lógica de "cuándo".
func (i *Incoming) maybeAvatarSync(ds *downstream.Client, r wameow.WAResolver, contactID int, jid types.JID, instance string) {
	if !i.avatarSync || r == nil || ds == nil {
		return
	}
	// Sin tracker = modo v0.31.0 sin refresh — solo cuando se crea contacto
	// (este callsite). Si el caller distingue create/found, gate ahí.
	if i.avatarTracker != nil && !i.avatarTracker.ShouldCheck(instance, jid.String()) {
		return
	}
	go i.syncAvatar(ds, r, contactID, jid, instance)
}

// syncAvatar es el worker que corre en goroutine. Lógica:
//   1. Get current ID via GetProfilePictureID (cheap, solo metadata).
//   2. Si ID == lastKnownID → no descarga, solo registra "checked".
//   3. Si ID == "" → no hay foto. Cachea y termina.
//   4. Si ID distinto → download + upload + update tracker.
//
// Errores se loguean como warning; el tracker ya marcó el timestamp
// en ShouldCheck para no retry inmediato.
func (i *Incoming) syncAvatar(ds *downstream.Client, r wameow.WAResolver, contactID int, jid types.JID, instance string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	currentID, err := r.GetProfilePictureID(ctx, jid)
	if err != nil {
		i.logger.Warn("avatar sync: get id failed",
			"err", err, "contact_id", contactID, "jid", jid.String())
		return
	}
	// Sin foto WA: nada que subir, cacheamos el "" para no chequear hasta TTL.
	if currentID == "" {
		if i.avatarTracker != nil {
			i.avatarTracker.UpdateID(instance, jid.String(), "")
		}
		return
	}
	// Avatar idéntico al último sincronizado: no re-descargar.
	if i.avatarTracker != nil {
		if last := i.avatarTracker.LastID(instance, jid.String()); last == currentID {
			return
		}
	}

	data, mime, err := r.GetProfilePicture(ctx, jid)
	if err != nil {
		i.logger.Warn("avatar sync: download failed",
			"err", err, "contact_id", contactID, "jid", jid.String())
		return
	}
	if len(data) == 0 {
		// Edge: tenía ID pero descarga vacía. Cacheamos como sin foto.
		if i.avatarTracker != nil {
			i.avatarTracker.UpdateID(instance, jid.String(), "")
		}
		return
	}
	if err := ds.UploadContactAvatar(ctx, contactID, data, mime); err != nil {
		i.logger.Warn("avatar sync: upload to downstream failed",
			"err", err, "contact_id", contactID, "size", len(data))
		return
	}
	if i.avatarTracker != nil {
		i.avatarTracker.UpdateID(instance, jid.String(), currentID)
	}
	i.logger.Info("avatar synced",
		"contact_id", contactID, "size", len(data), "mime", mime,
		"avatar_id", currentID, "jid", jid.String())
}

func (i *Incoming) incUsageIn(instance string) {
	if i.usage != nil {
		i.usage.IncIn(instance)
	}
}

// resolvedSender captura el shape real del remitente WhatsApp Multi-Device:
// puede llegar como PN (teléfono) o LID (identificador anónimo). Conservamos
// ambos sin enmascarar para que downstream refleje la realidad.
type resolvedSender struct {
	addressingMode string // "pn" | "lid" | ""
	primaryJID     string // el JID por el que enrutamos (canonical: PN si lo conocemos, si no LID)
	pnJID          string // JID PN (..@s.whatsapp.net) si está disponible
	lidJID         string // JID LID (..@lid) si está disponible
	phone          string // teléfono REAL en E.164 (sin +) si existe PN, vacío en caso LID-only
}

func resolveSender(info *events.Message, r wameow.WAResolver) resolvedSender {
	src := info.Info.MessageSource
	chat := src.Chat
	alt := src.SenderAlt

	mode := string(src.AddressingMode)

	var pn, lid types.JID
	switch chat.Server {
	case types.DefaultUserServer: // s.whatsapp.net
		pn = chat.ToNonAD()
		if alt.Server == types.HiddenUserServer {
			lid = alt.ToNonAD()
		}
	case types.HiddenUserServer: // lid
		lid = chat.ToNonAD()
		if alt.Server == types.DefaultUserServer {
			pn = alt.ToNonAD()
		}
	default:
		return resolvedSender{addressingMode: mode, primaryJID: chat.String()}
	}

	// Si solo conocemos una forma, intentar mapearla a la otra vía LIDStore.
	// Esto da identidad estable entre direcciones (DEVOPS→Ricard llega solo con LID,
	// Ricard→DEVOPS llega con LID+PN; sin este mapeo creamos contactos duplicados).
	if r != nil {
		if pn.IsEmpty() && !lid.IsEmpty() {
			if mapped, ok := r.PNForLID(lid); ok {
				pn = mapped.ToNonAD()
			}
		}
		if lid.IsEmpty() && !pn.IsEmpty() {
			if mapped, ok := r.LIDForPN(pn); ok {
				lid = mapped.ToNonAD()
			}
		}
	}

	rs := resolvedSender{addressingMode: mode}
	// Preferimos PN como identificador canónico cuando lo conocemos.
	if !pn.IsEmpty() {
		rs.pnJID = pn.String()
		rs.primaryJID = rs.pnJID
		rs.phone = pn.User
	} else {
		rs.primaryJID = lid.String()
	}
	if !lid.IsEmpty() {
		rs.lidJID = lid.String()
	}
	return rs
}

// Handle procesa un evento Message de whatsmeow.
func (i *Incoming) Handle(ctx context.Context, instance string, msg *events.Message, r wameow.WAResolver) {
	// Filtro defensivo: solo procesamos chats 1-on-1 (PN/LID) y grupos.
	// Broadcasts (status), newsletter (canales), bot — se ignoran porque
	// no son conversaciones agente↔cliente y crearían contactos basura.
	if !isSupportedChatServer(msg.Info.Chat.Server) {
		i.logger.Debug("event from unsupported chat server — ignorado",
			"id", msg.Info.ID, "instance", instance, "server", msg.Info.Chat.Server)
		return
	}
	// Broadcast: status@broadcast es un caso especial (el sender publica un
	// estado que llega como event aunque chat.Server=="s.whatsapp.net"). Lo
	// detectamos por el método del SDK.
	if msg.Info.IsIncomingBroadcast() {
		i.logger.Debug("incoming broadcast — ignorado",
			"id", msg.Info.ID, "instance", instance, "chat", msg.Info.Chat.String())
		return
	}

	// Reactions: si el mensaje es una reacción a otro mensaje, lo manejamos
	// por un path distinto (postea actividad en la conv, no mensaje normal).
	if msg.Message != nil && msg.Message.GetReactionMessage() != nil {
		i.handleReaction(ctx, instance, msg, r)
		return
	}

	text := extractTextContent(msg)
	media := extractMedia(msg)
	if text == "" && media == nil {
		i.logger.Debug("event sin texto ni media — ignorado", "id", msg.Info.ID, "instance", instance)
		return
	}
	// content efectivo = texto plano O caption del media. Si solo es media sin
	// caption, content queda vacío y downstream muestra solo el adjunto.
	content := text
	if content == "" && media != nil {
		content = media.caption
	}

	rs := resolveSender(msg, r)
	fromMe := msg.Info.IsFromMe
	isGroup := msg.Info.Chat.Server == types.GroupServer

	otherJID := msg.Info.Chat.ToNonAD()
	contactName := ""
	if r != nil {
		if isGroup {
			// Para grupos el "contact" sintético en downstream representa al
			// grupo entero — su nombre visible debe ser el subject del grupo,
			// no el push name del primer participante que mande mensaje.
			if subj, ok := r.GroupSubject(otherJID); ok {
				contactName = subj
			}
		} else {
			contactName = r.ContactName(otherJID)
			// Si chat es LID, intenta también el PN para sacar el nombre (a veces
			// el contact store está poblado solo en una de las dos formas).
			if contactName == "" && otherJID.Server == types.HiddenUserServer {
				if pn, ok := r.PNForLID(otherJID); ok {
					contactName = r.ContactName(pn)
				}
			}
		}
	}
	if contactName == "" && !fromMe && !isGroup {
		// Sin group subject conocido caemos a "" (sync usa pickName con phone/LID).
		// Solo aplicamos push name a 1-on-1 — en grupo el push name es del
		// participante, no del grupo, y daría título incorrecto a la conv.
		contactName = msg.Info.PushName
	}

	// En grupos, prefijar la identidad del participante al body para que
	// múltiples senders dentro de la misma conv del downstream sean
	// distinguibles. Solo aplica a mensajes incoming (los fromMe del agente
	// no necesitan prefijo).
	//
	// Si groupTracker está activo, suprime el header cuando el sender es
	// el mismo del mensaje anterior dentro del TTL — replica el "burst"
	// visual de WhatsApp donde solo el primer msg del grupo lleva header.
	// Siempre registramos el sender (incluyendo fromMe, usando "_bot" como
	// JID sintético) para que el siguiente mensaje real reciba header
	// correctamente tras una intervención del agente.
	if i.groupPrefixSender && isGroup {
		senderKey := msg.Info.Sender.String()
		if fromMe {
			senderKey = "_bot"
		}
		shouldEmit := true
		if i.groupTracker != nil {
			shouldEmit = i.groupTracker.RecordAndCheck(instance, msg.Info.Chat.String(), senderKey)
		}
		if !fromMe && shouldEmit {
			content = applyGroupSenderPrefix(content, msg, r)
		}
	}

	metrics.MessagesTotal.WithLabelValues("in", instance, i.ds.OwnerTagFor(ctx, instance)).Inc()
	i.incUsageIn(instance)
	i.logger.Info("incoming whatsapp",
		"instance", instance,
		"fromMe", fromMe,
		"mode", rs.addressingMode,
		"pn", rs.pnJID,
		"lid", rs.lidJID,
		"chat", msg.Info.Chat.String(),
		"pushNameEvent", msg.Info.PushName,
		"resolvedName", contactName,
		"waID", msg.Info.ID,
	)

	if fromMe {
		drop, err := i.dedup.ShouldDrop(ctx, instance, rs.primaryJID, content)
		if err != nil {
			i.logger.Error("dedup check failed", "err", err)
		} else if drop {
			i.logger.Warn("dropped duplicate (likely LID twin)",
				"instance", instance, "primaryJID", rs.primaryJID, "content_len", len(content))
			return
		}
	}

	inboxID := i.resolve(instance)
	if err := i.sync(ctx, instance, inboxID, rs, content, fromMe, contactName, msg.Info.ID, media, r, msg.Message); err != nil {
		i.logger.Error("sync to downstream failed", "err", err, "instance", instance, "primaryJID", rs.primaryJID)
	}
}

func (i *Incoming) sync(ctx context.Context, instance string, inboxID int, rs resolvedSender, content string, fromMe bool, contactName, waID string, media *mediaInfo, r wameow.WAResolver, rawMsg *waE2E.Message) error {
	// Resolvemos el client downstream apropiado para esta instancia
	// (multi-tenant si está configurado, fallback global si no).
	ds := i.ds.For(ctx, instance)
	// La búsqueda usa el JID primario como identifier (LID o PN). Es lo único
	// estable a lo largo del tiempo para un usuario en su modo nativo.
	identifier := rs.primaryJID

	contact, err := findContactByIdentifier(ctx, ds, identifier, rs.phone)
	if err != nil {
		return fmt.Errorf("find contact: %w", err)
	}
	if contact == nil {
		req := downstream.CreateContactReq{
			InboxID:    inboxID,
			Name:       pickName(contactName, rs),
			Identifier: identifier,
		}
		// Solo poblamos phone_number cuando tenemos un PN real.
		if rs.phone != "" {
			req.PhoneNumber = "+" + rs.phone
		}
		contact, err = ds.CreateContact(ctx, req)
		if err != nil {
			return fmt.Errorf("create contact: %w", err)
		}
	}
	// Avatar sync — aplica tanto al contacto recién creado como al
	// existente. El tracker decide cuándo procede (primera vez o TTL).
	// Sin tracker (v0.31.0 mode): solo se llamará para creados nuevos
	// porque la rama de existing pasa sobre contact ya no-nil sin tocar
	// el flow original. Con tracker (v0.31.1+): se chequea en ambos casos.
	if chatJID, parseErr := types.ParseJID(identifier); parseErr == nil {
		i.maybeAvatarSync(ds, r, contact.ID, chatJID, instance)
	}

	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil {
		return fmt.Errorf("find conversation: %w", err)
	}
	if conv == nil {
		conv, err = ds.CreateConversation(ctx, downstream.CreateConversationReq{
			SourceID:  identifier,
			InboxID:   inboxID,
			ContactID: contact.ID,
		})
		if err != nil {
			return fmt.Errorf("create conversation: %w", err)
		}
	}

	msgType := "incoming"
	if fromMe {
		msgType = "outgoing"
	}

	// Si hay media, descargar bytes y postear como multipart attachment.
	// Si la descarga falla, caemos a postear solo el caption + marker.
	if media != nil && r != nil && rawMsg != nil {
		data, err := r.DownloadAny(ctx, rawMsg)
		if err != nil {
			i.logger.Error("download media failed — posting text-only fallback",
				"err", err, "mimetype", media.mimetype, "waID", waID)
			fallback := content
			if fallback == "" {
				fallback = fmt.Sprintf("[adjunto %s no se pudo descargar: %s]", media.kind, media.mimetype)
			} else {
				fallback += fmt.Sprintf("\n\n[adjunto %s no se pudo descargar: %s]", media.kind, media.mimetype)
			}
			_, err := ds.PostMessage(ctx, downstream.PostMessageReq{
				ConversationID: conv.ID,
				Content:        fallback,
				MessageType:    msgType,
				SourceID:       "WAID:" + waID,
			})
			return err
		}
		_, err = ds.PostMessageWithAttachment(ctx, downstream.PostMessageAttachmentReq{
			ConversationID: conv.ID,
			Content:        content,
			MessageType:    msgType,
			SourceID:       "WAID:" + waID,
			FileName:       media.filename,
			MimeType:       media.mimetype,
			Data:           data,
		})
		return err
	}

	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    msgType,
		SourceID:       "WAID:" + waID,
	})
	return err
}

// findContactByIdentifier intenta primero por phone (si lo tenemos) y luego por
// el identifier exacto. Necesario porque /contacts/search no busca por identifier.
func findContactByIdentifier(ctx context.Context, ds *downstream.Client, identifier, phone string) (*downstream.Contact, error) {
	if phone != "" {
		c, err := ds.FindContactByPhone(ctx, phone)
		if err != nil {
			return nil, err
		}
		if c != nil {
			return c, nil
		}
	}
	// fallback: buscar por el "user" del JID (el identifier raw no es indexable en search)
	user := identifier
	for i := range identifier {
		if identifier[i] == '@' {
			user = identifier[:i]
			break
		}
	}
	c, err := ds.FindContactByPhone(ctx, user)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// mediaInfo describe el adjunto descubierto en un *events.Message.
type mediaInfo struct {
	kind     string // "image" | "audio" | "video" | "document" | "sticker"
	mimetype string
	filename string
	caption  string
}

// extractMedia inspecciona los tipos media conocidos. Devuelve nil si el
// mensaje no contiene ninguno.
func extractMedia(msg *events.Message) *mediaInfo {
	if msg == nil || msg.Message == nil {
		return nil
	}
	m := msg.Message
	if im := m.GetImageMessage(); im != nil {
		return &mediaInfo{
			kind:     "image",
			mimetype: im.GetMimetype(),
			filename: filenameFromMime(im.GetMimetype(), "image", "jpg"),
			caption:  im.GetCaption(),
		}
	}
	if am := m.GetAudioMessage(); am != nil {
		ext := "ogg"
		if am.GetPTT() {
			ext = "opus" // voice note
		}
		return &mediaInfo{
			kind:     "audio",
			mimetype: am.GetMimetype(),
			filename: filenameFromMime(am.GetMimetype(), "audio", ext),
		}
	}
	if vm := m.GetVideoMessage(); vm != nil {
		return &mediaInfo{
			kind:     "video",
			mimetype: vm.GetMimetype(),
			filename: filenameFromMime(vm.GetMimetype(), "video", "mp4"),
			caption:  vm.GetCaption(),
		}
	}
	if dm := m.GetDocumentMessage(); dm != nil {
		fn := dm.GetFileName()
		if fn == "" {
			fn = filenameFromMime(dm.GetMimetype(), "document", "bin")
		}
		return &mediaInfo{
			kind:     "document",
			mimetype: dm.GetMimetype(),
			filename: fn,
			caption:  dm.GetCaption(),
		}
	}
	if sm := m.GetStickerMessage(); sm != nil {
		return &mediaInfo{
			kind:     "sticker",
			mimetype: sm.GetMimetype(),
			filename: filenameFromMime(sm.GetMimetype(), "sticker", "webp"),
		}
	}
	return nil
}

// filenameFromMime sintetiza un filename razonable a partir del mimetype.
// Para "image/jpeg" → "image.jpg", para "audio/ogg; codecs=opus" → "audio.ogg".
func filenameFromMime(mime, prefix, defaultExt string) string {
	ext := defaultExt
	if i := strings.Index(mime, "/"); i >= 0 && i+1 < len(mime) {
		sub := mime[i+1:]
		if j := strings.Index(sub, ";"); j >= 0 {
			sub = sub[:j]
		}
		sub = strings.TrimSpace(sub)
		if sub != "" {
			ext = sub
		}
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	return prefix + "." + ext
}

// applyGroupSenderPrefix añade al body un prefijo identificando al
// remitente dentro del grupo. Formato actual:
//
//	**~Richard** `+34604021705`
//	<body>
//
// Tilde + nombre en bold (foreground, estilo WhatsApp), em-space
// (U+2003, ~4x un espacio normal) que markdown NO colapsa, teléfono
// en code block (background, monospace, fondo distintivo). El em-space
// da respiración visual entre el bold y el code sin necesitar
// separador char explícito.
//
// Degradaciones:
//   - sin teléfono: "**~<name>**:\n<body>"
//   - sin nombre:   "_<phone>_:\n<body>"
//   - sin ninguno:  devuelve el body sin tocar.
//
// El teléfono se formatea E.164 separando solo el CC (formatE164).
func applyGroupSenderPrefix(body string, msg *events.Message, r wameow.WAResolver) string {
	sender := msg.Info.Sender
	phoneDigits := ""
	switch sender.Server {
	case types.DefaultUserServer:
		phoneDigits = sender.User
	case types.HiddenUserServer:
		if r != nil {
			if pn, ok := r.PNForLID(sender.ToNonAD()); ok {
				phoneDigits = pn.User
			}
		}
	}

	name := ""
	saved := false
	if r != nil {
		name = r.ContactName(sender.ToNonAD())
		saved = r.IsContactSaved(sender.ToNonAD())
		// LID sin nombre o sin saved: intentar via PN.
		if sender.Server == types.HiddenUserServer && (name == "" || !saved) {
			if pn, ok := r.PNForLID(sender.ToNonAD()); ok {
				if name == "" {
					name = r.ContactName(pn)
				}
				if !saved {
					saved = r.IsContactSaved(pn)
				}
			}
		}
	}
	if name == "" {
		name = msg.Info.PushName
		// PushName NO cuenta como "saved" — viene del propio sender, no
		// de la libreta del bot owner. saved queda false aunque haya name.
	}

	var prefix string
	switch {
	case saved && name != "":
		// Contacto en agenda: solo nombre, el agente ya sabe quién es.
		prefix = "**~" + name + "**"
	case phoneDigits != "" && name != "":
		// Push name + teléfono code block para identificar al desconocido.
		// em-space (U+2003) entre name bold y phone code para respiración visual.
		prefix = "**~" + name + "** `" + formatE164(phoneDigits) + "`"
	case name != "":
		prefix = "**~" + name + "**:"
	case phoneDigits != "":
		prefix = "`" + formatE164(phoneDigits) + "`:"
	default:
		return body
	}
	if body == "" {
		return prefix
	}
	return prefix + "\n" + body
}

// formatE164 toma "34604021705" → "+34604021705". Devuelve el número
// compacto con `+` delante. (Versiones anteriores separaban CC con
// espacio; resultaba ruido visual al lado del nombre — el `+` ya
// marca el inicio).
func formatE164(digits string) string {
	if digits == "" {
		return ""
	}
	return "+" + digits
}

// isSupportedChatServer indica si procesamos eventos de este tipo de chat.
// Solo aceptamos 1-on-1 (PN/LID) y grupos. Broadcasts, newsletter, bot, etc. se ignoran.
func isSupportedChatServer(server string) bool {
	switch server {
	case types.DefaultUserServer, // s.whatsapp.net
		types.HiddenUserServer, // lid
		types.GroupServer:      // g.us
		return true
	}
	return false
}

func extractTextContent(msg *events.Message) string {
	if msg.Message == nil {
		return ""
	}
	if msg.Message.GetConversation() != "" {
		return msg.Message.GetConversation()
	}
	if ext := msg.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText()
	}
	return ""
}

func pickName(resolvedName string, rs resolvedSender) string {
	if resolvedName != "" {
		return resolvedName
	}
	// Sin info de contacto: NO inventamos identidad. Marcamos como anónimo,
	// dejando explícito si es LID o un teléfono sin asociar.
	if rs.phone != "" {
		return "WhatsApp " + rs.phone
	}
	if rs.lidJID != "" {
		return "WhatsApp LID " + lidShort(rs.lidJID)
	}
	return rs.primaryJID
}

func lidShort(lidJID string) string {
	user := lidJID
	for i := range lidJID {
		if lidJID[i] == '@' {
			user = lidJID[:i]
			break
		}
	}
	if len(user) > 6 {
		return "…" + user[len(user)-6:]
	}
	return user
}
