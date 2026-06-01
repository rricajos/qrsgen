package bridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// HandleGroupInfo procesa un *events.GroupInfo. Genera mensajes
// "activity" en Chatwoot para cada subevento relevante: cambio de
// nombre/topic, miembros añadidos/expulsados, promote/demote, lock/
// announce/ephemeral toggles. v0.47.0.
//
// Si la feature está desactivada o el conv del grupo aún no existe
// (no hemos recibido msgs de ese grupo todavía), no-op silencioso.
func (i *Incoming) HandleGroupInfo(ctx context.Context, instance string, evt *events.GroupInfo, r wameow.WAResolver) {
	if !i.groupEventsEnabled {
		return
	}
	if evt == nil {
		return
	}
	lines := buildGroupInfoLines(evt, r)
	if len(lines) == 0 {
		return
	}
	if err := i.postActivityToGroupConv(ctx, instance, evt.JID, lines, r); err != nil {
		metrics.RealtimeEventsTotal.WithLabelValues("group_event", "ds_error", instance).Inc()
		i.logger.Warn("group info: post activity failed",
			"err", err, "instance", instance, "group", evt.JID)
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("group_event", "ok", instance).Inc()
}

// HandleJoinedGroup procesa un *events.JoinedGroup — cuando el bot
// es añadido a un grupo nuevo. Postea una notificación inicial al
// conv si ya existe; si no, el primer msg del grupo creará el conv
// y el contexto del join se pierde (acceptable). v0.47.0.
func (i *Incoming) HandleJoinedGroup(ctx context.Context, instance string, evt *events.JoinedGroup, r wameow.WAResolver) {
	if !i.groupEventsEnabled {
		return
	}
	if evt == nil {
		return
	}
	reason := evt.Reason
	if reason == "" {
		reason = "added"
	}
	verb := "Te añadieron a este grupo"
	if evt.Type == "new" {
		verb = "Te añadieron al grupo recién creado"
	}
	if reason == "invite" {
		verb = "Te uniste vía invite link"
	}
	subject := ""
	if r != nil {
		if s, ok := r.GroupSubject(evt.JID); ok {
			subject = s
		}
	}
	line := "**" + verb + "**"
	if subject != "" {
		line += "\n_Grupo: " + subject + "_"
	}
	if err := i.postActivityToGroupConv(ctx, instance, evt.JID, []string{line}, r); err != nil {
		// Conv puede no existir todavía — no es error real.
		i.logger.Debug("joined group: conv not ready, skipping",
			"err", err, "instance", instance, "group", evt.JID)
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("group_event", "ok", instance).Inc()
	i.logger.Info("joined group activity posted",
		"instance", instance, "group", evt.JID, "subject", subject)
}

// HandleIdentityChange procesa un *events.IdentityChange — cuando un
// contacto cambia su primary device, su código de seguridad cambia.
// Postea una nota informativa al conv 1:1 del contacto si existe.
// v0.47.0.
func (i *Incoming) HandleIdentityChange(ctx context.Context, instance string, evt *events.IdentityChange, r wameow.WAResolver) {
	if !i.groupEventsEnabled {
		return
	}
	if evt == nil {
		return
	}
	// Implicit changes (recovery de untrusted identity errors) son
	// ruidosos — los saltamos para no inundar.
	if evt.Implicit {
		return
	}
	name, _ := resolveJIDNameSaved(evt.JID, r)
	if name == "" {
		name = evt.JID.User
	}
	line := fmt.Sprintf("🔐 **El código de seguridad de %s cambió.** Toca para más información.", name)
	if err := i.postActivityToPNConv(ctx, instance, evt.JID, []string{line}, r); err != nil {
		i.logger.Debug("identity change: post activity failed",
			"err", err, "instance", instance, "jid", evt.JID)
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("group_event", "ok", instance).Inc()
	i.logger.Info("identity change activity posted",
		"instance", instance, "jid", evt.JID, "name", name)
}

// buildGroupInfoLines genera las líneas de activity msg a partir de
// los campos del *events.GroupInfo. Devuelve un slice con cada
// subevento como una línea de markdown. Si no hay cambios reportables
// (todo es nil), devuelve slice vacío.
func buildGroupInfoLines(evt *events.GroupInfo, r wameow.WAResolver) []string {
	var lines []string
	actor := identityFromJIDPtr(evt.Sender, r)
	if actor == "" {
		actor = "Alguien"
	}

	if evt.Name != nil {
		lines = append(lines, fmt.Sprintf("📝 **%s** cambió el nombre del grupo a _%s_", actor, evt.Name.Name))
	}
	if evt.Topic != nil {
		t := evt.Topic.Topic
		if t == "" {
			lines = append(lines, fmt.Sprintf("📝 **%s** quitó la descripción del grupo", actor))
		} else {
			lines = append(lines, fmt.Sprintf("📝 **%s** cambió la descripción del grupo: _%s_", actor, t))
		}
	}
	if evt.Locked != nil {
		if evt.Locked.IsLocked {
			lines = append(lines, fmt.Sprintf("🔒 **%s** restringió la edición del grupo a admins", actor))
		} else {
			lines = append(lines, fmt.Sprintf("🔓 **%s** permitió a todos editar el grupo", actor))
		}
	}
	if evt.Announce != nil {
		if evt.Announce.IsAnnounce {
			lines = append(lines, fmt.Sprintf("📢 **%s** activó modo anuncio (solo admins pueden enviar mensajes)", actor))
		} else {
			lines = append(lines, fmt.Sprintf("📢 **%s** desactivó modo anuncio", actor))
		}
	}
	if evt.Ephemeral != nil {
		if evt.Ephemeral.IsEphemeral {
			lines = append(lines, fmt.Sprintf("⏱️ **%s** activó mensajes temporales (%ds)", actor, evt.Ephemeral.DisappearingTimer))
		} else {
			lines = append(lines, fmt.Sprintf("⏱️ **%s** desactivó los mensajes temporales", actor))
		}
	}
	if len(evt.Join) > 0 {
		names := identityListFromJIDs(evt.Join, r)
		if len(names) > 0 {
			lines = append(lines, fmt.Sprintf("➕ **%s** añadió a %s", actor, strings.Join(names, ", ")))
		}
	}
	if len(evt.Leave) > 0 {
		names := identityListFromJIDs(evt.Leave, r)
		if len(names) > 0 {
			lines = append(lines, fmt.Sprintf("➖ %s salieron/fueron expulsados del grupo", strings.Join(names, ", ")))
		}
	}
	if len(evt.Promote) > 0 {
		names := identityListFromJIDs(evt.Promote, r)
		if len(names) > 0 {
			lines = append(lines, fmt.Sprintf("⭐ **%s** promovió a admin: %s", actor, strings.Join(names, ", ")))
		}
	}
	if len(evt.Demote) > 0 {
		names := identityListFromJIDs(evt.Demote, r)
		if len(names) > 0 {
			lines = append(lines, fmt.Sprintf("⭐ **%s** quitó admin a: %s", actor, strings.Join(names, ", ")))
		}
	}
	return lines
}

// identityFromJIDPtr resuelve el nombre+tilde para un *types.JID,
// preferentemente del contact store; cae a phone E.164 si no hay
// nombre. nil → "".
func identityFromJIDPtr(p *types.JID, r wameow.WAResolver) string {
	if p == nil {
		return ""
	}
	return identityFromJID(*p, r)
}

// identityFromJID resuelve el nombre+tilde para un JID (idem).
func identityFromJID(jid types.JID, r wameow.WAResolver) string {
	name, saved := resolveJIDNameSaved(jid, r)
	if name == "" && jid.Server == types.DefaultUserServer {
		return formatE164(jid.User)
	}
	if name == "" {
		return jid.User
	}
	if !saved {
		return "~" + name
	}
	return name
}

// identityListFromJIDs aplica identityFromJID a un slice y filtra ""s.
func identityListFromJIDs(jids []types.JID, r wameow.WAResolver) []string {
	out := make([]string, 0, len(jids))
	for _, j := range jids {
		n := identityFromJID(j, r)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// postActivityToGroupConv encuentra la conv del grupo en Chatwoot y
// postea las líneas como un mensaje "incoming" (visible en la conv
// pero distinto de los msgs del cliente porque viene unido). Si la
// conv no existe (grupo sin actividad previa), no-op.
func (i *Incoming) postActivityToGroupConv(ctx context.Context, instance string, groupJID types.JID, lines []string, r wameow.WAResolver) error {
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return fmt.Errorf("no downstream client")
	}
	identifier := groupJID.String()
	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil {
		return fmt.Errorf("find contact: %w", err)
	}
	if contact == nil {
		return fmt.Errorf("group conv not in downstream yet")
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		return fmt.Errorf("find conversation")
	}
	content := strings.Join(lines, "\n")
	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    "incoming",
	})
	return err
}

// postActivityToPNConv equivalente para conv 1:1 (no grupo).
func (i *Incoming) postActivityToPNConv(ctx context.Context, instance string, jid types.JID, lines []string, r wameow.WAResolver) error {
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return fmt.Errorf("no downstream client")
	}
	// 1:1: identifier es el JID (PN o LID resuelto). Para identidad,
	// preferimos PN; si entrada es LID, resolver.
	target := jid
	if jid.Server == types.HiddenUserServer && r != nil {
		if pn, ok := r.PNForLID(jid.ToNonAD()); ok {
			target = pn
		}
	}
	identifier := target.String()
	phone := ""
	if target.Server == types.DefaultUserServer {
		phone = target.User
	}
	contact, err := findContactByIdentifier(ctx, ds, identifier, phone)
	if err != nil {
		return fmt.Errorf("find contact: %w", err)
	}
	if contact == nil {
		return fmt.Errorf("1:1 conv not in downstream yet")
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		return fmt.Errorf("find conversation")
	}
	content := strings.Join(lines, "\n")
	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    "incoming",
	})
	return err
}

// SetGroupEventsEnabled activa la propagación de events.GroupInfo,
// JoinedGroup e IdentityChange como activity msgs en Chatwoot. v0.47.0.
func (i *Incoming) SetGroupEventsEnabled(v bool) { i.groupEventsEnabled = v }
