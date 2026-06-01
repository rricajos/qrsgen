package bridge

import (
	"context"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/types/events"
)


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

	// v0.40.1: delegamos a resolveSenderInfo (helper centralizado que
	// hereda el fix v0.39.9 para LID con PN saved).
	// v0.45.0: el header de reacciones usa el mismo template configurable
	// que el group prefix (QRSGEN_HEADER_TEMPLATE). El verb sale en línea
	// aparte, separado por i.headerSep — mismo layout que group msgs:
	//
	//   `+34611887663 · ~Agustina Sant Martí Real Estate`
	//   reaccionó con 👍
	si := resolveSenderInfo(msg, r)
	if si.name == "" {
		si.name = "alguien"
	}
	header, ok := renderSenderHeader(si, i.headerTemplate)
	verb := "reaccionó con " + emoji
	if emoji == "" {
		verb = "quitó su reacción"
	}
	var content string
	if ok {
		// v0.45.1: reactionSep (default `\n`) — distinto del headerSep
		// usado en group msgs (default `\n\n`) porque la reacción es
		// visualmente más atómica.
		content = header + i.reactionSep + verb
	} else {
		// Fallback: sin phone ni name → solo el verb plano.
		content = verb
	}

	// v0.53.2: si está activado reactionAsReply, intentamos resolver
	// el WAID del msg target a su Chatwoot msg_id para postear la
	// reacción como quote-reply visual (`content_attributes.in_reply_to`).
	// El agente ve a qué msg se reaccionó. Si el lookup falla
	// (msg target no trackeado), degrada al formato standalone.
	inReplyTo := 0
	if i.reactionAsReply && i.msgHistory != nil && targetMsgID != "" {
		if tm, ok := i.msgHistory.FindByWAID(ctx, instance, targetMsgID); ok {
			inReplyTo = tm.msgID
		}
	}

	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    "incoming",
		SourceID:       "WAID:reaction:" + msg.Info.ID,
		InReplyTo:      inReplyTo,
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
		"sender", si.name, "instance", instance)
}
