package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
)

// ErrSpamguardBlocked se devuelve por HandleFor cuando un outgoing fue
// rechazado por la política spamguard (duplicado back-to-back). El handler
// HTTP debe traducirlo a 422 para que Chatwoot marque el mensaje como
// failed (icono rojo) en lugar de sent (verde).
var ErrSpamguardBlocked = errors.New("spamguard: blocked duplicate outgoing")

// Sender es la interfaz mínima del cliente WhatsApp que usamos para mandar
// mensajes. Multi-instance: el primer parámetro es el nombre de la instancia.
type Sender interface {
	SendText(ctx context.Context, instance, remoteJid, content string) (string, error)
	SendMedia(ctx context.Context, instance, remoteJid, kind, mimetype, filename, caption string, data []byte) (string, error)
	// SendTextReply envía un mensaje de texto como reply a un msg
	// existente. quotedWAID es el ID del msg original en WhatsApp.
	// quotedSenderJID es el JID del autor (en grupos: del participante;
	// en 1:1 puede ir vacío y whatsmeow usa el remoteJid). quotedText
	// es el cuerpo del msg citado (whatsmeow lo incluye en el
	// ContextInfo para que el cliente receptor muestre el preview).
	// v0.44.0.
	SendTextReply(ctx context.Context, instance, remoteJid, content, quotedWAID, quotedSenderJID, quotedText string) (string, error)
	// SendMediaReply envía un media como reply a un msg existente.
	// Idéntico a SendMedia + populates ContextInfo con quotedWAID/
	// quotedSenderJID/quotedText. v0.51.0.
	SendMediaReply(ctx context.Context, instance, remoteJid, kind, mimetype, filename, caption string, data []byte, quotedWAID, quotedSenderJID, quotedText string) (string, error)
}

// ReadMarker es la interfaz para marcar mensajes WhatsApp como leídos.
// Permite que el outgoing handler dispare MarkRead cuando el downstream
// notifica que el agente leyó la conv (evento conversation_updated).
// Multi-instance: el primer parámetro es el nombre de la instancia.
// Si nil en la Outgoing struct, el feature mark-as-read está desactivado.
type ReadMarker interface {
	MarkRead(ctx context.Context, instance, chat, sender string, messageIDs []string, ts time.Time) error
}

// BlobDownloader descarga el contenido binario de una URL del downstream (typically active_storage o blob URL).
type BlobDownloader interface {
	DownloadBlob(ctx context.Context, url string) ([]byte, string, error)
}

// WebhookAttachment es el subset del payload del webhook downstream para adjuntos.
type WebhookAttachment struct {
	ID        int    `json:"id"`
	FileType  string `json:"file_type"` // "image" | "audio" | "video" | "file"
	DataURL   string `json:"data_url"`
	Extension string `json:"extension"`
	FileSize  int    `json:"file_size"`
	FileName  string `json:"file_name,omitempty"` // a veces presente, especialmente para documents
}

// WebhookPayload representa el subset del payload del webhook downstream que necesitamos.
type WebhookPayload struct {
	Event             string              `json:"event"`
	ID                int                 `json:"id"`           // message id en eventos message_created
	MessageType       string              `json:"message_type"` // "incoming" | "outgoing" | "activity" | "template"
	Private           bool                `json:"private"`      // notas internas del agente — NO se envían a WhatsApp
	Content           string              `json:"content"`
	SourceID          string              `json:"source_id"`
	Attachments       []WebhookAttachment `json:"attachments"`
	ContentAttributes *struct {
		// InReplyTo es el message_id de Chatwoot al que se está
		// respondiendo cuando el agente hace quote-reply en el composer.
		// Desde v0.44.0 lo usamos para mapear al WAID del incoming
		// original y propagar la respuesta como WA reply nativo.
		InReplyTo int `json:"in_reply_to"`
	} `json:"content_attributes"`
	Conversation *struct {
		ID                int   `json:"id"`
		InboxID           int   `json:"inbox_id"`
		AgentLastSeenAt   int64 `json:"agent_last_seen_at"` // v0.39.0 — timestamp del último read del agente
		ContactLastSeenAt int64 `json:"contact_last_seen_at"`
		Meta              *struct {
			Sender *struct {
				PhoneNumber string `json:"phone_number"`
				Identifier  string `json:"identifier"`
			} `json:"sender"`
		} `json:"meta"`
	} `json:"conversation"`
}

// SpamguardProvider expone lo mínimo del Manager necesario para spamguard.
// Lo abstraemos como interface para evitar dep ciclo bridge→manager.
type SpamguardProvider interface {
	IsSpamguardEnabled(ctx context.Context, instance string) bool
	EmitLifecycle(name, event string, extras map[string]any)
}

// UsageRecorder es la interfaz mínima que outgoing/incoming/manager usan para
// incrementar contadores. Se inyecta como setter — si nil, no-op.
type UsageRecorder interface {
	IncIn(instance string)
	IncOut(instance string)
	IncSpamguardBlock(instance string)
	IncLifecycle(instance string)
}

// BanwatchRecorder es la interfaz mínima que outgoing usa para alimentar el
// detector de ban-risk. Se inyecta como setter — si nil, no-op.
type BanwatchRecorder interface {
	Record(instance, jid string, success bool)
}

// Outgoing maneja el flujo downstream→WhatsApp: recibe webhooks de
// Chatwoot (mensajes salientes del agente) y los traduce a llamadas
// al cliente whatsmeow. Es la contraparte de Incoming.
type Outgoing struct {
	sender   Sender
	marker   ReadMarker
	waids    *waidTracker
	ds       downstream.Router
	dedup    *Deduper
	sg       SpamguardProvider
	tracker  *SpamguardTracker
	logger   *slog.Logger
	usage    UsageRecorder
	banwatch BanwatchRecorder

	// msgHistory: tracker compartido con Incoming para resolver
	// Chatwoot msgID → WAID al hacer reply-to outgoing (v0.44.0).
	// Si nil, reply-to está desactivado y los msgs salen como
	// texto plano aunque content_attributes.in_reply_to esté presente.
	msgHistory *msgHistoryTracker

	// incoming opcionalmente referencia el Incoming para que outgoing
	// pueda resetear el groupTracker de burst al enviar mensajes
	// del bot a un grupo (v0.44.1). Sin esto, el siguiente msg del
	// usuario tras el bot reply hereda la supresión del header.
	incoming *Incoming
}

// NewOutgoing construye el handler con sus dependencias mínimas. Las
// features avanzadas (reply-to, banwatch, retroactive ref) se enchufan
// después con sus respectivos Enable/Set methods para mantener este
// constructor manejable.
func NewOutgoing(sender Sender, ds downstream.Router, dedup *Deduper, sg SpamguardProvider, tracker *SpamguardTracker, logger *slog.Logger) *Outgoing {
	return &Outgoing{sender: sender, ds: ds, dedup: dedup, sg: sg, tracker: tracker, logger: logger}
}

// EnableReplyToOutgoing conecta el msg_history tracker (compartido con
// Incoming) para que el outgoing handler resuelva Chatwoot msgID →
// WAID cuando el webhook trae content_attributes.in_reply_to. Llamar
// tras incoming.EnableRetroactiveNameUpdate; sin esa llamada el
// tracker es nil y la feature queda desactivada.
//
// Si tracker es nil (o no llamamos a este enable), el webhook
// outgoing con in_reply_to se procesa como SendText normal (sin
// quote nativo de WhatsApp). Backward-compatible.
func (o *Outgoing) EnableReplyToOutgoing(in *Incoming) {
	if in == nil {
		return
	}
	o.msgHistory = in.replyToTracker()
	// v0.44.1: la misma referencia sirve para resetear el groupTracker
	// de burst tras un send del bot a un grupo.
	o.incoming = in
}

// EnableMarkAsRead conecta el tracker de WAIDs (compartido con Incoming) y
// el ReadMarker. Cuando llegue un evento `conversation_updated` del downstream
// con `agent_last_seen_at` actualizado, Outgoing drena el tracker para
// esa conv y llama MarkRead. Sin esta llamada, el feature está desactivado
// y los eventos conversation_updated se ignoran. Desde v0.39.0.
func (o *Outgoing) EnableMarkAsRead(waids *waidTracker, marker ReadMarker) {
	o.waids = waids
	o.marker = marker
}

// handleConversationUpdated maneja el evento del downstream que indica
// que el agente leyó la conv. Drena los WAIDs incoming registrados antes
// de agent_last_seen_at y llama MarkRead via wameow en una goroutine
// (fire-and-forget) para que el webhook response no se bloquee.
//
// agent_last_seen_at == 0 → ignorar (no hay info de read).
// tracker vacío para esa conv → no-op (nada que marcar).
//
// El ctx del webhook se cancela rápido (es el del HTTP request); usamos
// context.Background() en la goroutine con timeout propio.
func (o *Outgoing) handleConversationUpdated(_ context.Context, instance string, p WebhookPayload) {
	if p.Conversation == nil || p.Conversation.AgentLastSeenAt == 0 {
		return
	}
	if p.Conversation.Meta == nil || p.Conversation.Meta.Sender == nil {
		return
	}
	chatJID := p.Conversation.Meta.Sender.Identifier
	if chatJID == "" {
		return
	}
	cutoff := time.Unix(p.Conversation.AgentLastSeenAt, 0)
	// Drenar ANTES del spawn, no DENTRO de la goroutine — así si llegan
	// varios conversation_updated seguidos cada uno se lleva su slice
	// y no compiten por el mismo lock fuera del request.
	waids, senderJID := o.waids.DrainBefore(instance, chatJID, cutoff)
	if len(waids) == 0 {
		return
	}
	// Fire-and-forget: MarkRead hace round-trip a WA y no debe bloquear
	// el response del webhook. Si falla, logueamos y los WAIDs ya
	// drenados se pierden — el cliente WA no verá el doble check para
	// esos msgs concretos. Cosmético, no impacta correctness.
	// #nosec G118 -- async mark-as-read intentionally outlives the webhook handler frame
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := o.marker.MarkRead(ctx, instance, chatJID, senderJID, waids, cutoff); err != nil {
			o.logger.Warn("mark-as-read failed",
				"err", err, "instance", instance, "chat", chatJID, "count", len(waids))
			return
		}
		o.logger.Info("mark-as-read sent to WhatsApp",
			"instance", instance, "chat", chatJID, "count", len(waids), "cutoff", cutoff)
	}()
}

// SetUsage attaches a usage recorder for counter increments. Safe to call once
// during bootstrap. Pass nil to disable.
func (o *Outgoing) SetUsage(u UsageRecorder) { o.usage = u }

// SetBanwatch attaches the ban-risk recorder. Pass nil to disable.
func (o *Outgoing) SetBanwatch(b BanwatchRecorder) { o.banwatch = b }

// markBotInGroup notifica al Incoming que el bot acaba de enviar un
// msg a `remoteJid`. Si es un grupo (@g.us), resetea el groupTracker
// para que el próximo msg del usuario vuelva a llevar header.
// v0.44.1. No-op si EnableReplyToOutgoing no fue llamada o si no
// estamos en grupo.
func (o *Outgoing) markBotInGroup(instance, remoteJid string) {
	if o.incoming == nil {
		return
	}
	// Solo grupos llevan header — para 1:1 esto es no-op semánticamente
	// pero igual el chequeo se hace dentro de MarkBotSentInGroup.
	if !strings.HasSuffix(remoteJid, "@g.us") {
		return
	}
	o.incoming.MarkBotSentInGroup(instance, remoteJid)
}

// resolveReplyContext intenta resolver el quote para un outgoing
// que sea quote-reply en Chatwoot. Devuelve (waid, senderJID, text)
// del msg original, o ("", "", "") si:
//   - msgHistory no está habilitado
//   - el webhook no trae content_attributes.in_reply_to
//   - el msgID no se encuentra en el tracker
//   - el trackedMsg no tiene WAID guardado (rows antiguas pre-v0.44.0)
//
// El senderJID identifica al autor del msg citado. Para 1:1 puede ir
// vacío; para grupos lo necesita el cliente WA receptor para enlazar
// el quote al participante correcto.
func (o *Outgoing) resolveReplyContext(ctx context.Context, instance string, p WebhookPayload) (string, string, string) {
	if o.msgHistory == nil {
		return "", "", ""
	}
	if p.ContentAttributes == nil || p.ContentAttributes.InReplyTo == 0 {
		return "", "", ""
	}
	tm, senderJID, ok := o.msgHistory.FindByChatwootMsgID(ctx, instance, p.ContentAttributes.InReplyTo)
	if !ok {
		// v0.52.1: subido de Debug a Info — sin este log, el operador
		// no se entera de por qué la quote no llegó.
		o.logger.Info("reply-to: trackedMsg not found, sending plain (no quote in WA)",
			"instance", instance, "in_reply_to", p.ContentAttributes.InReplyTo,
			"hint", "the quoted msg may pre-date v0.44.0 (no waid tracked) or be from an instance never reset")
		return "", "", ""
	}
	if tm.waid == "" {
		// Row vieja (pre-v0.44.0) sin WAID guardado. No podemos
		// construir el ContextInfo de WhatsApp — degradamos a SendText.
		o.logger.Info("reply-to: trackedMsg found but no WAID (pre-v0.44.0 row), sending plain",
			"instance", instance, "in_reply_to", p.ContentAttributes.InReplyTo,
			"chatwoot_msg_id", tm.msgID)
		return "", "", ""
	}
	return tm.waid, senderJID, tm.body
}

func (o *Outgoing) recordBanwatch(instance, jid string, success bool) {
	if o.banwatch != nil {
		o.banwatch.Record(instance, jid, success)
	}
}

func (o *Outgoing) incUsageOut(instance string) {
	if o.usage != nil {
		o.usage.IncOut(instance)
	}
}

func (o *Outgoing) incUsageSpamguard(instance string) {
	if o.usage != nil {
		o.usage.IncSpamguardBlock(instance)
	}
}

// HandleForRaw deserializa un payload JSON ya almacenado (típico caso: el
// outbox lo persistió en disco como JSONB) y llama a HandleFor. Mantiene
// la firma estable de cara al outbox sin acoplarlo al struct de bridge.
func (o *Outgoing) HandleForRaw(ctx context.Context, instance string, raw json.RawMessage) error {
	var p WebhookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("unmarshal outbox payload: %w", err)
	}
	return o.HandleFor(ctx, instance, p)
}

// HandleFor procesa el webhook con conocimiento de la instancia destino.
func (o *Outgoing) HandleFor(ctx context.Context, instance string, p WebhookPayload) error {
	// Mark-as-read outgoing (v0.39.0): cuando el downstream nos avisa que
	// el agente leyó la conv, drenamos el tracker de WAIDs y llamamos
	// MarkRead via wameow para que el cliente vea doble check azul.
	if p.Event == "conversation_updated" && o.waids != nil && o.marker != nil {
		o.handleConversationUpdated(ctx, instance, p)
		return nil
	}
	if p.MessageType != "outgoing" {
		return nil
	}
	// CRITICAL: las notas privadas (private:true) son visibles solo para
	// agentes del downstream. NUNCA deben enviarse al cliente por WhatsApp.
	if p.Private {
		o.logger.Debug("private note ignored (no se envía a WhatsApp)",
			"instance", instance, "msg_id", p.ID)
		return nil
	}
	if strings.HasPrefix(p.SourceID, "WAID:") {
		o.logger.Debug("outgoing already has WAID (echo from whatsapp), skipping")
		return nil
	}

	// Idempotencia por downstream msg_id: si recibimos el mismo webhook 2 veces
	// (retry, doble-click del agente que crea 2 msgs con IDs distintos NO se
	// duplica aquí porque tienen IDs distintos — esto SÍ atrapa retry exacto).
	if o.dedup != nil && p.ID > 0 {
		seen, err := o.dedup.SeenIncomingMsg(ctx, instance, p.ID)
		if err == nil && seen {
			o.logger.Warn("dropped duplicate outgoing webhook (downstream msg_id retry)",
				"instance", instance, "msg_id", p.ID)
			return nil
		}
	}

	var remoteJid string
	if p.Conversation != nil && p.Conversation.Meta != nil && p.Conversation.Meta.Sender != nil {
		remoteJid = p.Conversation.Meta.Sender.Identifier
	}
	if remoteJid == "" {
		o.logger.Warn("outgoing webhook missing remoteJid", "instance", instance)
		return nil
	}
	// Safety net: los contactos "ops" del router (identifier prefijo qrsgen-qr-)
	// no tienen número real de WhatsApp — son contactos sintéticos que sirven de
	// panel sintético del bridge. Cualquier outgoing dirigido a uno de ellos NO debe
	// despacharse vía whatsmeow.
	if strings.HasPrefix(remoteJid, "qrsgen-qr-") {
		o.logger.Debug("outgoing to ops contact, skip WhatsApp dispatch",
			"instance", instance, "remoteJid", remoteJid, "msg_id", p.ID)
		return nil
	}

	// Spamguard: si está activo para esta instancia, comparamos el contenido
	// del nuevo outgoing con los últimos 2 enviados a esa misma JID (in-memory).
	// Si coincide → bloqueamos + emitimos evento spam_blocked con counter.
	// Sin ventana ni min-chars: política simple "no enviar lo mismo 2 veces seguidas".
	if o.sg != nil && o.tracker != nil && p.Content != "" {
		if o.sg.IsSpamguardEnabled(ctx, instance) {
			blocked, count := o.tracker.CheckAndRecord(instance, remoteJid, p.Content)
			if blocked {
				metrics.SpamguardBlocks.WithLabelValues(instance, o.ds.OwnerTagFor(ctx, instance)).Inc()
				o.incUsageSpamguard(instance)
				o.logger.Warn("spamguard blocked outgoing dup",
					"instance", instance, "remoteJid", remoteJid,
					"msg_id", p.ID, "block_count", count)
				preview := p.Content
				if len([]rune(preview)) > 60 {
					r := []rune(preview)
					preview = string(r[:60]) + "…"
				}
				// Extras enriquecidos (v0.28.3): permiten que el integrador (n8n)
				// linke al mensaje en el panel QR y postee una nota interna en la
				// conversación del cliente avisando que no se entregó.
				extras := map[string]any{
					"count":      count,
					"preview":    preview,
					"remote_jid": remoteJid,
					"msg_id":     p.ID,
				}
				if p.Conversation != nil {
					extras["conv_id"] = p.Conversation.ID
				}
				o.sg.EmitLifecycle(instance, "spam_blocked", extras)
				// Sentinel error → el handler HTTP devuelve 422 para que
				// Chatwoot marque el mensaje como failed (icono rojo).
				return ErrSpamguardBlocked
			}
		}
	}

	// Si trae adjuntos: por cada uno, descarga blob + envía media.
	// El content (si existe) se manda como caption del PRIMER adjunto (semántica
	// de WhatsApp: una caption por mensaje). Si hay varios adjuntos, los siguientes
	// van sin caption. Resolvemos el client downstream según el owner_tag de la
	// instancia (multi-tenant) o caemos al global. Lazy — solo si vamos a usarlo.
	if len(p.Attachments) > 0 {
		ds := o.ds.For(ctx, instance)
		caption := p.Content
		var firstWAID string
		// v0.51.0: resolver reply context para que el PRIMER adjunto
		// vaya como reply nativo si el webhook trae in_reply_to.
		// Los adjuntos subsecuentes (cuando hay >1) van sin quote para
		// no duplicar el preview en cada uno.
		quotedWAID, quotedSenderJID, quotedText := o.resolveReplyContext(ctx, instance, p)

		for i, att := range p.Attachments {
			if att.DataURL == "" {
				o.logger.Warn("attachment without data_url, skipping", "att_id", att.ID)
				continue
			}
			data, ct, err := ds.DownloadBlob(ctx, att.DataURL)
			if err != nil {
				o.logger.Error("download downstream blob failed", "err", err, "att_id", att.ID, "data_url", att.DataURL)
				continue
			}
			mimetype := ct
			if mimetype == "" {
				mimetype = mimeFromExt(att.Extension)
			}
			filename := att.FileName
			if filename == "" {
				filename = fmt.Sprintf("attachment_%d.%s", att.ID, att.Extension)
			}
			capForThis := ""
			if i == 0 {
				capForThis = caption
			}
			var waID string
			if i == 0 && quotedWAID != "" {
				waID, err = o.sender.SendMediaReply(ctx, instance, remoteJid, att.FileType, mimetype, filename, capForThis, data, quotedWAID, quotedSenderJID, quotedText)
			} else {
				waID, err = o.sender.SendMedia(ctx, instance, remoteJid, att.FileType, mimetype, filename, capForThis, data)
			}
			tag := o.ds.OwnerTagFor(ctx, instance)
			if err != nil {
				metrics.MessageDispatchErrors.WithLabelValues("out", instance, "send_media", tag).Inc()
				o.recordBanwatch(instance, remoteJid, false)
				o.logger.Error("send media failed", "err", err, "att_id", att.ID, "kind", att.FileType)
				continue
			}
			metrics.MessagesTotal.WithLabelValues("out", instance, tag).Inc()
			o.incUsageOut(instance)
			o.recordBanwatch(instance, remoteJid, true)
			o.logger.Info("sent outgoing media to whatsapp",
				"instance", instance, "remoteJid", remoteJid,
				"kind", att.FileType, "mime", mimetype, "size", len(data), "waID", waID)
			o.markBotInGroup(instance, remoteJid)
			if firstWAID == "" {
				firstWAID = waID
			}
		}
		if firstWAID != "" && p.ID > 0 && p.Conversation != nil {
			if err := ds.UpdateMessageSourceID(ctx, p.Conversation.ID, p.ID, "WAID:"+firstWAID); err != nil {
				o.logger.Warn("patch source_id failed (media)", "err", err, "msg_id", p.ID)
			}
		}
		// v0.52.1: trackear outgoing media también.
		o.trackOutgoing(instance, p, remoteJid, firstWAID, p.Content)
		return nil
	}

	// Sin adjuntos: si tampoco hay content, no hay nada que mandar.
	if p.Content == "" {
		o.logger.Warn("outgoing webhook missing content y sin attachments", "instance", instance)
		return nil
	}

	// v0.44.0: si el webhook trae content_attributes.in_reply_to,
	// resolver el WAID original via msgHistory y mandar como reply
	// nativo de WhatsApp en lugar de SendText pelado.
	quotedWAID, quotedSenderJID, quotedText := o.resolveReplyContext(ctx, instance, p)

	var waID string
	var err error
	if quotedWAID != "" {
		waID, err = o.sender.SendTextReply(ctx, instance, remoteJid, p.Content, quotedWAID, quotedSenderJID, quotedText)
	} else {
		waID, err = o.sender.SendText(ctx, instance, remoteJid, p.Content)
	}
	tag := o.ds.OwnerTagFor(ctx, instance)
	if err != nil {
		metrics.MessageDispatchErrors.WithLabelValues("out", instance, "send_text", tag).Inc()
		o.recordBanwatch(instance, remoteJid, false)
		return err
	}
	metrics.MessagesTotal.WithLabelValues("out", instance, tag).Inc()
	o.incUsageOut(instance)
	o.recordBanwatch(instance, remoteJid, true)
	o.logger.Info("sent outgoing to whatsapp", "instance", instance, "remoteJid", remoteJid, "waID", waID)
	o.markBotInGroup(instance, remoteJid)

	if p.ID > 0 && p.Conversation != nil {
		ds := o.ds.For(ctx, instance)
		if err := ds.UpdateMessageSourceID(ctx, p.Conversation.ID, p.ID, "WAID:"+waID); err != nil {
			o.logger.Warn("patch downstream message source_id failed (mensaje enviado, pero echo no se dedupará)",
				"err", err, "msg_id", p.ID, "conv_id", p.Conversation.ID)
		}
	}
	// v0.52.1: trackear outgoing en msg_history para que reply-to a
	// msgs del agente funcione. Sin esto, msg_history solo tenía
	// incoming → quote-reply a un msg outgoing del propio agente
	// no encuentra anchor y degrada a SendText sin quote.
	o.trackOutgoing(instance, p, remoteJid, waID, p.Content)
	return nil
}

// trackOutgoing registra el outgoing recién enviado en msg_history
// (si está habilitado). Permite que reply-to outgoing funcione
// también cuando el agente quote-replea a un msg del propio agente.
// v0.52.1.
//
// Key del tracker: chatJID (igual que sender en 1:1, sintético en
// grupos — FindByChatwootMsgID hace lookup por msgID linealmente,
// la key solo afecta organización).
func (o *Outgoing) trackOutgoing(instance string, p WebhookPayload, remoteJid, waID, body string) {
	if o.msgHistory == nil {
		return
	}
	if p.ID == 0 || p.Conversation == nil || p.Conversation.ID == 0 {
		return
	}
	if waID == "" {
		return
	}
	o.msgHistory.Record(instance, remoteJid, trackedMsg{
		convID:   p.Conversation.ID,
		msgID:    p.ID,
		body:     body,
		postedAt: time.Now(),
		waid:     waID,
		// hasPrefix=false (outgoing del agente no lleva group prefix);
		// queda fuera del retroactive name update — correcto.
	})
}

// mimeFromExt infiere un mimetype razonable cuando downstream no devuelve Content-Type.
func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "mp4":
		return "video/mp4"
	case "mp3":
		return "audio/mpeg"
	case "ogg", "opus":
		return "audio/ogg; codecs=opus"
	case "wav":
		return "audio/wav"
	case "pdf":
		return "application/pdf"
	case "doc", "docx":
		return "application/msword"
	case "xls", "xlsx":
		return "application/vnd.ms-excel"
	}
	return "application/octet-stream"
}
