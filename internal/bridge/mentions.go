package bridge

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/types"
)

// resolveMentions sustituye los `@<jid_user>` inline del texto por
// `@<nombre resuelto>`. Las menciones llegan en dos partes desde
// WhatsApp:
//
//  1. Inline en el body: `@148855681191942 buenos días`
//  2. Como array separado en `ContextInfo.MentionedJID`:
//     ["148855681191942@lid"]
//
// El cliente WA del receptor une las dos y muestra "@Ivan Madrid".
// qrsgen, sin esta resolución, propaga el `@<numero>` raw al
// downstream → el agente ve un JID incomprensible.
//
// v0.53.0. El nombre se resuelve con `resolveJIDNameSaved` heredando
// el fix v0.39.9 (LID con PN saved usa canónico) y aplica el patrón
// de tilde según `saved`.
//
// Si la feature está desactivada (template=""), devuelve el texto
// sin cambios.
func resolveMentions(text string, mentionedJIDs []string, r wameow.WAResolver, template string) string {
	if text == "" || len(mentionedJIDs) == 0 || template == "" {
		return text
	}
	for _, jidStr := range mentionedJIDs {
		jid, err := types.ParseJID(jidStr)
		if err != nil {
			continue
		}
		// Token a buscar: @<user> sin sufijo de server (es como WA
		// transporta la mención inline).
		token := "@" + jid.User
		if !strings.Contains(text, token) {
			continue
		}
		replacement := renderMention(jid, r, template)
		if replacement == "" {
			continue
		}
		text = strings.ReplaceAll(text, token, replacement)
	}
	return text
}

// renderMention resuelve un JID a "@Nombre" (con o sin tilde según
// saved) usando el template configurado. Tokens disponibles:
//   - $name  → nombre canónico (con `~` automático si no saved)
//   - $phone → E.164 con `+` (si resoluble); fallback a RedactedPhone
//             (`+1∙∙∙∙∙∙∙∙80`) si WA respeta privacidad del LID.
//
// Cadena de fallback (v0.53.1):
//  1. Saved name (FullName/FirstName del store)
//  2. PushName / BusinessName (con `~` por no saved)
//  3. Phone resuelto vía PNForLID (LID → PN)
//  4. RedactedPhone (LID con privacy enabled — WA da partial)
//  5. Si nada: devuelve "" → mantiene el token raw en el texto.
func renderMention(jid types.JID, r wameow.WAResolver, template string) string {
	name, saved := resolveJIDNameSaved(jid, r)
	phone := ""
	switch jid.Server {
	case types.DefaultUserServer:
		phone = "+" + jid.User
	case types.HiddenUserServer:
		if r != nil {
			if pn, ok := r.PNForLID(jid.ToNonAD()); ok {
				phone = "+" + pn.User
			} else {
				// v0.53.1 fallback: si el LID no resuelve a PN
				// (privacy mode o mapping aún no conocida),
				// usamos RedactedPhone que WA expone para grupos.
				if rp := r.RedactedPhone(jid); rp != "" {
					phone = rp
				}
			}
		}
	}
	nameMark := name
	if name != "" && !saved {
		nameMark = "~" + name
	}
	// Si no tenemos ni nombre ni phone, dejamos el texto raw (skip).
	if nameMark == "" && phone == "" {
		return ""
	}
	// Fallback: si no hay name pero hay phone, el rendering puede
	// quedar feo si el template usa solo $name. Sustituimos $name
	// por phone como fallback razonable.
	if nameMark == "" {
		nameMark = phone
	}
	out := template
	out = strings.ReplaceAll(out, "$name", nameMark)
	out = strings.ReplaceAll(out, "$phone", phone)
	return out
}

// MentionTemplateDefault es el template default cuando la feature está
// activa. v0.53.0.
const MentionTemplateDefault = "@$name"

// lidRefreshTTL es el tiempo mínimo entre llamadas a RefreshGroupLIDs
// por el mismo grupo. Evita inundar GetGroupInfo si llegan muchos
// mensajes con menciones LID seguidas. v0.53.1.
const lidRefreshTTL = 1 * time.Hour

// lidRefreshTracker registra cuándo se refrescó last time el LID
// store de cada grupo, para no spammear GetGroupInfo. v0.53.1.
type lidRefreshTracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func newLIDRefreshTracker() *lidRefreshTracker {
	return &lidRefreshTracker{lastSeen: map[string]time.Time{}}
}

// shouldRefresh devuelve true si han pasado más de TTL desde el
// último refresh del grupo. Marca el grupo como recién refrescado
// si devuelve true (evita races concurrentes).
func (t *lidRefreshTracker) shouldRefresh(group string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	last, ok := t.lastSeen[group]
	if ok && time.Since(last) < lidRefreshTTL {
		return false
	}
	t.lastSeen[group] = time.Now()
	return true
}

// maybeRefreshLIDs llama RefreshGroupLIDs si:
//   - El chat es un grupo
//   - Hay al menos una mention LID sin PN resoluble
//   - No se ha refrescado este grupo en la última hora
//
// Side effect: tras el refresh, whatsmeow's LID store puede tener
// las mappings que faltaban → próximo resolveMentions resuelve.
// v0.53.1.
func maybeRefreshLIDs(chat types.JID, mentionedJIDs []string, r wameow.WAResolver, tracker *lidRefreshTracker) {
	if chat.Server != types.GroupServer || r == nil || tracker == nil {
		return
	}
	hasUnresolved := false
	for _, jidStr := range mentionedJIDs {
		jid, err := types.ParseJID(jidStr)
		if err != nil || jid.Server != types.HiddenUserServer {
			continue
		}
		if _, ok := r.PNForLID(jid.ToNonAD()); !ok {
			hasUnresolved = true
			break
		}
	}
	if !hasUnresolved {
		return
	}
	if !tracker.shouldRefresh(chat.String()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.RefreshGroupLIDs(ctx, chat)
}
