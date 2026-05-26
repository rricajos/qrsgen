package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
)

// Sender es la interfaz mínima del cliente WhatsApp que usamos para mandar
// mensajes. Multi-instance: el primer parámetro es el nombre de la instancia.
type Sender interface {
	SendText(ctx context.Context, instance, remoteJid, content string) (string, error)
	SendMedia(ctx context.Context, instance, remoteJid, kind, mimetype, filename, caption string, data []byte) (string, error)
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
	Event        string               `json:"event"`
	ID           int                  `json:"id"`           // message id en eventos message_created
	MessageType  string               `json:"message_type"` // "incoming" | "outgoing" | "activity" | "template"
	Private      bool                 `json:"private"`      // notas internas del agente — NO se envían a WhatsApp
	Content      string               `json:"content"`
	SourceID     string               `json:"source_id"`
	Attachments  []WebhookAttachment `json:"attachments"`
	Conversation *struct {
		ID      int `json:"id"`
		InboxID int `json:"inbox_id"`
		Meta    *struct {
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

type Outgoing struct {
	sender  Sender
	ds      *downstream.Client
	dedup   *Deduper
	sg      SpamguardProvider
	tracker *SpamguardTracker
	logger  *slog.Logger
	usage   UsageRecorder
}

func NewOutgoing(sender Sender, ds *downstream.Client, dedup *Deduper, sg SpamguardProvider, tracker *SpamguardTracker, logger *slog.Logger) *Outgoing {
	return &Outgoing{sender: sender, ds: ds, dedup: dedup, sg: sg, tracker: tracker, logger: logger}
}

// SetUsage attaches a usage recorder for counter increments. Safe to call once
// during bootstrap. Pass nil to disable.
func (o *Outgoing) SetUsage(u UsageRecorder) { o.usage = u }

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

// HandleFor procesa el webhook con conocimiento de la instancia destino.
func (o *Outgoing) HandleFor(ctx context.Context, instance string, p WebhookPayload) error {
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
				metrics.SpamguardBlocks.WithLabelValues(instance).Inc()
				o.incUsageSpamguard(instance)
				o.logger.Warn("spamguard blocked outgoing dup",
					"instance", instance, "remoteJid", remoteJid,
					"msg_id", p.ID, "block_count", count)
				preview := p.Content
				if len([]rune(preview)) > 60 {
					r := []rune(preview)
					preview = string(r[:60]) + "…"
				}
				o.sg.EmitLifecycle(instance, "spam_blocked", map[string]any{
					"count":   count,
					"preview": preview,
				})
				return nil
			}
		}
	}

	// Si trae adjuntos: por cada uno, descarga blob + envía media.
	// El content (si existe) se manda como caption del PRIMER adjunto (semántica
	// de WhatsApp: una caption por mensaje). Si hay varios adjuntos, los siguientes
	// van sin caption.
	if len(p.Attachments) > 0 {
		caption := p.Content
		var firstWAID string
		for i, att := range p.Attachments {
			if att.DataURL == "" {
				o.logger.Warn("attachment without data_url, skipping", "att_id", att.ID)
				continue
			}
			data, ct, err := o.ds.DownloadBlob(ctx, att.DataURL)
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
			waID, err := o.sender.SendMedia(ctx, instance, remoteJid, att.FileType, mimetype, filename, capForThis, data)
			if err != nil {
				metrics.MessageDispatchErrors.WithLabelValues("out", instance, "send_media").Inc()
				o.logger.Error("send media failed", "err", err, "att_id", att.ID, "kind", att.FileType)
				continue
			}
			metrics.MessagesTotal.WithLabelValues("out", instance).Inc()
			o.incUsageOut(instance)
			o.logger.Info("sent outgoing media to whatsapp",
				"instance", instance, "remoteJid", remoteJid,
				"kind", att.FileType, "mime", mimetype, "size", len(data), "waID", waID)
			if firstWAID == "" {
				firstWAID = waID
			}
		}
		if firstWAID != "" && p.ID > 0 && p.Conversation != nil {
			if err := o.ds.UpdateMessageSourceID(ctx, p.Conversation.ID, p.ID, "WAID:"+firstWAID); err != nil {
				o.logger.Warn("patch source_id failed (media)", "err", err, "msg_id", p.ID)
			}
		}
		return nil
	}

	// Sin adjuntos: si tampoco hay content, no hay nada que mandar.
	if p.Content == "" {
		o.logger.Warn("outgoing webhook missing content y sin attachments", "instance", instance)
		return nil
	}

	waID, err := o.sender.SendText(ctx, instance, remoteJid, p.Content)
	if err != nil {
		metrics.MessageDispatchErrors.WithLabelValues("out", instance, "send_text").Inc()
		return err
	}
	metrics.MessagesTotal.WithLabelValues("out", instance).Inc()
	o.incUsageOut(instance)
	o.logger.Info("sent outgoing to whatsapp", "instance", instance, "remoteJid", remoteJid, "waID", waID)

	if p.ID > 0 && p.Conversation != nil {
		if err := o.ds.UpdateMessageSourceID(ctx, p.Conversation.ID, p.ID, "WAID:"+waID); err != nil {
			o.logger.Warn("patch downstream message source_id failed (mensaje enviado, pero echo no se dedupará)",
				"err", err, "msg_id", p.ID, "conv_id", p.Conversation.ID)
		}
	}
	return nil
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
