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

	// typingSync controla si los eventos *events.ChatPresence (composing/
	// paused) se propagan al downstream como toggle_typing_status.
	// Default true. Desde v0.34.0.
	typingSync bool

	// typingTracker dedupea calls al downstream — eventos ChatPresence
	// pueden llegar varios por segundo durante typing activo. Default
	// minInterval 4s (Chatwoot UI normalmente expira typing tras ~5s).
	typingTracker *typingTracker

	// readReceiptsSync controla si los read receipts WhatsApp (cliente
	// abrió el chat y vio el msg del agente) actualizan el last_seen
	// del downstream. Default true. Desde v0.34.1.
	readReceiptsSync bool

	// waids es el tracker de WAIDs incoming por conv. Se popula desde
	// sync() y se drena desde el outgoing handler cuando llega un
	// evento conversation_updated del downstream. Si nil, no se rastrea
	// (feature mark-as-read desactivado). Desde v0.39.0.
	waids *waidTracker
}

// NewIncomingDynamic crea un handler con resolución dinámica de inbox por instancia.
func NewIncomingDynamic(ds downstream.Router, dedup *Deduper, logger *slog.Logger, resolve InboxResolver) *Incoming {
	return &Incoming{
		ds: ds, dedup: dedup, logger: logger, resolve: resolve,
		groupPrefixSender: true,
		avatarSync:        true,
		reactionsSync:     true,
		typingSync:        true,
		typingTracker:     newTypingTracker(4 * time.Second),
		readReceiptsSync:  true,
	}
}

// SetReactionsSync activa o desactiva la propagación de reacciones WhatsApp
// al downstream. Default true. Setear a false ignora todos los eventos
// de reacción — no se postea nada en la conv del downstream.
func (i *Incoming) SetReactionsSync(v bool) { i.reactionsSync = v }

// SetTypingSync activa o desactiva la propagación de ChatPresence (typing
// indicators) al downstream. Default true. Setear a false ignora todos
// los eventos — la UI del downstream no muestra "está escribiendo".
func (i *Incoming) SetTypingSync(v bool) { i.typingSync = v }

// SetReadReceiptsSync activa o desactiva la propagación de read receipts
// WhatsApp (cliente abrió el chat) al downstream. Default true.
func (i *Incoming) SetReadReceiptsSync(v bool) { i.readReceiptsSync = v }

// EnableMarkAsRead activa el tracker de WAIDs y devuelve un puntero al
// mismo para que el outgoing handler pueda drenarlo cuando llegan los
// eventos conversation_updated del downstream. Llamar antes de procesar
// mensajes — si no se llama, el tracker queda nil y los WAIDs no se
// rastrean (feature mark-as-read desactivado).
func (i *Incoming) EnableMarkAsRead() *waidTracker {
	if i.waids == nil {
		i.waids = newWAIDTracker(50)
	}
	return i.waids
}

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
		metrics.RealtimeEventsTotal.WithLabelValues("reaction", "ds_error", instance).Inc()
		return
	}
	if contact == nil {
		// Sin contact = sin conv. No creamos contactos para reacciones
		// sueltas — esperamos al primer mensaje real.
		metrics.RealtimeEventsTotal.WithLabelValues("reaction", "no_contact", instance).Inc()
		return
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("reaction", "no_conv", instance).Inc()
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

	// v0.39.7: align con el formato del prefix de grupo (v0.39.6).
	// Code block + teléfono primero + middle dot + tilde solo si no saved.
	// Aplica siempre que tengamos phone disponible (no solo en grupos)
	// para mantener consistencia visual entre todos los headers de sender.
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
	nameMark := name
	if !saved {
		nameMark = "~" + name
	}
	phoneStr := ""
	if phone != "" {
		phoneStr = formatE164(phone) + " · "
	}
	verb := "reaccionó con " + emoji
	if emoji == "" {
		verb = "quitó su reacción"
	}
	content := "`" + phoneStr + nameMark + " " + verb + "`"

	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    "incoming",
		SourceID:       "WAID:reaction:" + msg.Info.ID,
	})
	if err != nil {
		i.logger.Warn("reaction sync: post failed",
			"err", err, "conv_id", conv.ID, "target_msg_id", targetMsgID)
		metrics.RealtimeEventsTotal.WithLabelValues("reaction", "ds_error", instance).Inc()
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("reaction", "ok", instance).Inc()
	i.logger.Info("reaction synced",
		"conv_id", conv.ID, "emoji", emoji, "target_msg_id", targetMsgID,
		"sender", name, "instance", instance)
}

// HandleReceipt reacciona a *events.Receipt. Solo procesamos kind="read"
// (cliente abrió el chat y vio los msgs enviados por el agente). Otros
// tipos (delivered, played, sender, read-self) los logueamos pero no
// los propagamos al downstream — son menos accionables.
//
// Para read, actualizamos contact_last_seen_at del conv en el downstream
// usando el timestamp del receipt. La UI del downstream renderiza el
// "doble check azul" en los mensajes del agente que están dentro de
// ese rango temporal.
func (i *Incoming) HandleReceipt(ctx context.Context, instance string, chat types.JID, sender types.JID, kind string, messageIDs []string, ts time.Time, r wameow.WAResolver) {
	if !i.readReceiptsSync {
		return
	}
	// Solo procesamos read y read-self. Los demás (delivered, played,
	// sender) tienen menos valor accionable para el agente.
	if kind != string(types.ReceiptTypeRead) && kind != string(types.ReceiptTypeReadSelf) {
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "filtered", instance).Inc()
		return
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}

	identifier := chat.ToNonAD().String()
	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil || contact == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "no_contact", instance).Inc()
		return
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "no_conv", instance).Inc()
		return
	}

	if err := ds.UpdateContactLastSeen(ctx, conv.ID, ts); err != nil {
		i.logger.Warn("read receipt: update_last_seen failed",
			"err", err, "conv_id", conv.ID, "ts", ts)
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "ds_error", instance).Inc()
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "ok", instance).Inc()
	i.logger.Debug("read receipt synced",
		"conv_id", conv.ID, "ts", ts, "messages_count", len(messageIDs),
		"kind", kind, "instance", instance)
}

// HandleChatPresence reacciona a *events.ChatPresence (typing/paused).
// Propaga al downstream el toggle_typing_status correspondiente, con
// throttle del typingTracker para no saturar.
//
// chat es el JID del chat/grupo. sender es el JID del participante que
// tipea (en grupo) o igual a chat (en 1-on-1). En grupos puede haber
// múltiples participantes typing a la vez — el downstream solo soporta
// un indicator por conv, así que se reciben todos como "alguien está
// escribiendo".
//
// Para JIDs sin contact registrado, no hace nada (no podemos resolver
// la conv).
func (i *Incoming) HandleChatPresence(ctx context.Context, instance string, chat types.JID, sender types.JID, composing bool, media string, r wameow.WAResolver) {
	if !i.typingSync {
		return
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}

	identifier := chat.ToNonAD().String()
	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil || contact == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "no_contact", instance).Inc()
		return
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "no_conv", instance).Inc()
		return
	}

	if i.typingTracker != nil && !i.typingTracker.ShouldEmit(conv.ID, composing) {
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "throttled", instance).Inc()
		return
	}

	if err := ds.SetTypingStatus(ctx, conv.ID, composing); err != nil {
		i.logger.Warn("typing sync: SetTypingStatus failed",
			"err", err, "conv_id", conv.ID, "composing", composing)
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "ds_error", instance).Inc()
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("typing", "ok", instance).Inc()
	i.logger.Debug("typing synced",
		"conv_id", conv.ID, "composing", composing, "media", media,
		"chat", chat.String(), "sender", sender.String(), "instance", instance)
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
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_error", instance).Inc()
		return
	}
	// Sin foto WA: nada que subir, cacheamos el "" para no chequear hasta TTL.
	if currentID == "" {
		if i.avatarTracker != nil {
			i.avatarTracker.UpdateID(instance, jid.String(), "")
		}
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_miss", instance).Inc()
		return
	}
	// Avatar idéntico al último sincronizado: no re-descargar.
	if i.avatarTracker != nil {
		if last := i.avatarTracker.LastID(instance, jid.String()); last == currentID {
			metrics.RealtimeEventsTotal.WithLabelValues("avatar", "throttled", instance).Inc()
			return
		}
	}

	data, mime, err := r.GetProfilePicture(ctx, jid)
	if err != nil {
		i.logger.Warn("avatar sync: download failed",
			"err", err, "contact_id", contactID, "jid", jid.String())
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_error", instance).Inc()
		return
	}
	if len(data) == 0 {
		// Edge: tenía ID pero descarga vacía. Cacheamos como sin foto.
		if i.avatarTracker != nil {
			i.avatarTracker.UpdateID(instance, jid.String(), "")
		}
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_miss", instance).Inc()
		return
	}
	if err := ds.UploadContactAvatar(ctx, contactID, data, mime); err != nil {
		i.logger.Warn("avatar sync: upload to downstream failed",
			"err", err, "contact_id", contactID, "size", len(data))
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "ds_error", instance).Inc()
		return
	}
	if i.avatarTracker != nil {
		i.avatarTracker.UpdateID(instance, jid.String(), currentID)
	}
	metrics.RealtimeEventsTotal.WithLabelValues("avatar", "ok", instance).Inc()
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
	if err == nil && !fromMe && i.waids != nil {
		// Tracker para mark-as-read outgoing (v0.39.0). Solo incoming
		// reales (NO los fromMe que el agente envió por la app móvil).
		i.waids.RecordIncoming(instance, identifier, waID, rs.primaryJID, time.Now())
	}
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
		// Browser compat (v0.38.0):
		// - Mime se sanitiza para quitar el `; codecs=opus` que algunos
		//   downstreams confunden y los reproductores html5 audio
		//   no esperan en el header Content-Type.
		// - Voice notes (PTT) usan filename "voice-note.ogg" en lugar
		//   de "audio.opus" — la extensión .ogg activa el codec en
		//   más browsers que .opus.
		mime := sanitizeMime(am.GetMimetype())
		if mime == "" {
			mime = "audio/ogg"
		}
		prefix := "audio"
		if am.GetPTT() {
			prefix = "voice-note"
		}
		return &mediaInfo{
			kind:     "audio",
			mimetype: mime,
			filename: filenameFromMime(mime, prefix, "ogg"),
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
		// Browser compat (v0.38.0): default mime "image/webp" si WA
		// devuelve vacío — necesario para que browsers detecten cómo
		// renderizar (WebP es WIDELY soportado en navegadores modernos).
		mime := sanitizeMime(sm.GetMimetype())
		if mime == "" {
			mime = "image/webp"
		}
		return &mediaInfo{
			kind:     "sticker",
			mimetype: mime,
			filename: filenameFromMime(mime, "sticker", "webp"),
		}
	}
	return nil
}

// sanitizeMime quita el parámetro de codec del Content-Type. Por
// ejemplo "audio/ogg; codecs=opus" → "audio/ogg". Algunos browsers y
// downstreams se confunden con el codec specifier en el header del
// upload. Devuelve el mime original si no hay `;`.
func sanitizeMime(mime string) string {
	if i := strings.Index(mime, ";"); i >= 0 {
		return strings.TrimSpace(mime[:i])
	}
	return mime
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
		// v0.39.9: si el PN está guardado pero el LID no, preferimos
		// el ContactName del PN (canónico) sobre el nombre del LID
		// (que suele ser un PushName auto-asignado). Antes solo
		// rellenábamos cuando name=="" → quedaba el PushName del
		// LID aunque el contacto estuviera guardado con otro nombre.
		if sender.Server == types.HiddenUserServer && (name == "" || !saved) {
			if pn, ok := r.PNForLID(sender.ToNonAD()); ok {
				pnSaved := r.IsContactSaved(pn)
				if !saved && pnSaved {
					name = r.ContactName(pn)
					saved = true
				} else if name == "" {
					name = r.ContactName(pn)
					if !saved {
						saved = pnSaved
					}
				}
			}
		}
	}
	if name == "" {
		name = msg.Info.PushName
		// PushName NO cuenta como saved (auto-asignado por el remitente,
		// no por el dueño del bot).
	}

	// v0.39.6: formato unificado en code block — teléfono primero (ancho
	// natural por E.164 da columna consistente entre mensajes), middle
	// dot como separador, nombre al final. Tilde solo si no saved.
	// Dentro del code block markdown no procesa formato, así que no
	// usamos `**`; el monospace + background distintivo de Chatwoot
	// hace el contraste visual con el body.
	nameMark := "~" + name
	if saved {
		nameMark = name
	}

	var prefix string
	switch {
	case phoneDigits != "" && name != "":
		prefix = "`" + formatE164(phoneDigits) + " · " + nameMark + "`"
	case name != "":
		prefix = "`" + nameMark + ":`"
	case phoneDigits != "":
		prefix = "`" + formatE164(phoneDigits) + ":`"
	default:
		return body
	}
	if body == "" {
		return prefix
	}
	// v0.39.9: paragraph break (\n\n). El hard break CommonMark ("  \n")
	// que probamos en v0.39.8 no lo renderizaba Chatwoot — quedaba
	// el body inline con el header. \n\n garantiza separación visible
	// en cualquier renderer markdown.
	return prefix + "\n\n" + body
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
	// Location messages se renderizan como link + label legible.
	// Solo aplicamos si el msg trae LocationMessage; live locations
	// (isLive=true) salen igual — el agente ve un link estático con
	// las coords del último update recibido.
	if loc := msg.Message.GetLocationMessage(); loc != nil {
		return formatLocationContent(loc)
	}
	// Polls (encuestas): formato como lista numerada de opciones para
	// que el agente vea la pregunta y opciones. Los votos llegan como
	// PollUpdateMessage y NO se procesan (Chatwoot no tiene widget de
	// polls para reflejarlos correctamente). v0.37.0.
	if poll := msg.Message.GetPollCreationMessage(); poll != nil {
		return formatPollContent(poll)
	}
	if poll := msg.Message.GetPollCreationMessageV3(); poll != nil {
		return formatPollContent(poll)
	}
	return ""
}

// formatPollContent serializa un PollCreationMessage a un body legible
// con la pregunta, opciones numeradas y un hint del modo (single vs
// multi-select). Los votos posteriores (PollUpdateMessage) NO se
// procesan — Chatwoot no tiene UI nativa para reflejarlos. v0.37.0.
func formatPollContent(poll *waE2E.PollCreationMessage) string {
	name := poll.GetName()
	if name == "" {
		// Sin pregunta no merece la pena propagar — el agente vería
		// solo opciones flotantes sin contexto.
		return ""
	}
	options := poll.GetOptions()
	if len(options) == 0 {
		return ""
	}

	parts := []string{"🗳️ **Encuesta:** " + name}
	for i, opt := range options {
		// Numeración 1-based, formato "N. opción".
		parts = append(parts, fmt.Sprintf("%d. %s", i+1, opt.GetOptionName()))
	}

	// Hint del modo: 1 = single, >1 = multi, 0 = unlimited.
	maxSel := poll.GetSelectableOptionsCount()
	switch {
	case maxSel == 1:
		parts = append(parts, "_(elige 1 opción)_")
	case maxSel > 1:
		parts = append(parts, fmt.Sprintf("_(elige hasta %d opciones)_", maxSel))
	}
	return strings.Join(parts, "\n")
}

// formatLocationContent serializa un LocationMessage a un body legible
// con link a Google Maps. Si el mensaje incluye Name/Address de WhatsApp
// (POI o lugar guardado), los antepone para contexto. Si IsLive, lo marca
// como live location en el header.
func formatLocationContent(loc *waE2E.LocationMessage) string {
	lat := loc.GetDegreesLatitude()
	lng := loc.GetDegreesLongitude()
	// Evita coordenadas 0,0 vacías — formato no útil.
	if lat == 0 && lng == 0 {
		return ""
	}
	header := "📍 Ubicación compartida"
	if loc.GetIsLive() {
		header = "📍 Ubicación en vivo"
	}

	parts := []string{header}
	if name := loc.GetName(); name != "" {
		parts = append(parts, "**"+name+"**")
	}
	if addr := loc.GetAddress(); addr != "" {
		parts = append(parts, addr)
	}
	// Link a Google Maps — formato universal que todos los browsers
	// abren bien. Lat/lng con 6 decimales (precisión ~10cm).
	link := fmt.Sprintf("https://maps.google.com/?q=%.6f,%.6f", lat, lng)
	parts = append(parts, link)

	if cmt := loc.GetComment(); cmt != "" {
		parts = append(parts, "_"+cmt+"_")
	}
	return strings.Join(parts, "\n")
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
