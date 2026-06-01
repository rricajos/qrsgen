package bridge

import (
	"strings"

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
//   - $phone → E.164 con `+` (si resoluble)
//
// Default template `@$name`. Si el resolver no encuentra nombre, cae
// al phone si está disponible; sin nombre ni phone, mantiene el
// token raw (devolviendo "").
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
