package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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
	// posteán a downstream con prefijo de remitente ("+34 ... - Name:\n<body>").
	// Default true porque sin él, multi-sender en una misma conv del downstream
	// son indistinguibles. Se puede desactivar con QRSGEN_GROUP_PREFIX_SENDER=false.
	groupPrefixSender bool
}

// NewIncomingDynamic crea un handler con resolución dinámica de inbox por instancia.
func NewIncomingDynamic(ds downstream.Router, dedup *Deduper, logger *slog.Logger, resolve InboxResolver) *Incoming {
	return &Incoming{ds: ds, dedup: dedup, logger: logger, resolve: resolve, groupPrefixSender: true}
}

// SetUsage attaches a usage recorder. Pass nil to disable.
func (i *Incoming) SetUsage(u UsageRecorder) { i.usage = u }

// SetGroupPrefixSender controla el prefijo de remitente en mensajes de grupo.
func (i *Incoming) SetGroupPrefixSender(v bool) { i.groupPrefixSender = v }

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
	if i.groupPrefixSender && isGroup && !fromMe {
		content = applyGroupSenderPrefix(content, msg, r)
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
// remitente dentro del grupo. Formato: "+<phone> - <name>:\n<body>" cuando
// hay phone (PN) y push name; degrada a solo name o solo phone si falta uno.
// Si no hay identificación posible (ni phone ni name ni LID), devuelve el
// body sin tocar — mejor sin prefijo que con basura.
func applyGroupSenderPrefix(body string, msg *events.Message, r wameow.WAResolver) string {
	sender := msg.Info.Sender
	phone := ""
	switch sender.Server {
	case types.DefaultUserServer:
		phone = "+" + sender.User
	case types.HiddenUserServer:
		if r != nil {
			if pn, ok := r.PNForLID(sender.ToNonAD()); ok {
				phone = "+" + pn.User
			}
		}
	}

	name := ""
	if r != nil {
		// Preferimos el contacto resuelto (FullName/FirstName del store) si
		// existe; cae al push name del evento si no hay nada.
		name = r.ContactName(sender.ToNonAD())
	}
	if name == "" {
		name = msg.Info.PushName
	}

	var prefix string
	switch {
	case phone != "" && name != "":
		prefix = phone + " - " + name + ":"
	case name != "":
		prefix = name + ":"
	case phone != "":
		prefix = phone + ":"
	default:
		return body
	}
	if body == "" {
		return prefix
	}
	return prefix + "\n" + body
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
