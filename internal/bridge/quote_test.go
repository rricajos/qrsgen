package bridge

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// mkQuotedTextMsg construye un *events.Message que es un reply textual.
// participant es el JID del autor del mensaje citado (en grupos), "" si 1:1.
// quotedText es el texto del mensaje original. replyText es la respuesta.
func mkQuotedTextMsg(chat, sender types.JID, participant, quotedText, replyText string) *events.Message {
	ci := &waE2E.ContextInfo{
		StanzaID: proto("ORIGINAL_MSG_ID"),
		QuotedMessage: &waE2E.Message{
			Conversation: proto(quotedText),
		},
	}
	if participant != "" {
		ci.Participant = proto(participant)
	}
	return &events.Message{
		Message: &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto(replyText),
				ContextInfo: ci,
			},
		},
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: sender,
			},
		},
	}
}

// proto helper para *string (waE2E genera con punteros).
func proto(s string) *string { return &s }
func protoBool(b bool) *bool { return &b }

func TestFormatQuotedBlock_PlainTextReply(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "hola, qué tal", "todo bien gracias")
	r := &fakeResolver{
		names: map[string]string{author.String(): "Pepito"},
	}
	got := formatQuotedBlock(msg, r)
	// Pepito está en `names` pero NO en `savedJIDs` → ~Pepito (v0.44.3).
	want := "> _↩️ respondiendo a ~Pepito:_ hola, qué tal"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_NoQuoteReturnsEmpty(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	msg := &events.Message{
		Message: &waE2E.Message{Conversation: proto("hola sin quote")},
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender},
		},
	}
	if got := formatQuotedBlock(msg, nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormatQuotedBlock_NoResolverFallsBackToPhone(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "msg cita", "msg reply")
	got := formatQuotedBlock(msg, nil)
	// Sin resolver: nombre cae a phone E.164.
	// Sin resolver → no saved → ~+34… (v0.44.3).
	want := "> _↩️ respondiendo a ~+34600000099:_ msg cita"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_SavedAuthorWithoutTilde(t *testing.T) {
	// v0.44.3: si el autor está guardado en la agenda, sale sin `~`.
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "msg", "reply")
	r := &fakeResolver{
		names:     map[string]string{author.String(): "Pepito Saved"},
		savedJIDs: map[string]bool{author.String(): true},
	}
	got := formatQuotedBlock(msg, r)
	want := "> _↩️ respondiendo a Pepito Saved:_ msg"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_LIDAuthorWithSavedPN(t *testing.T) {
	// v0.44.3 regression: si el author es LID con PushName pero el PN
	// resuelto está saved con otro nombre, mostramos el canónico SIN `~`
	// (hereda fix v0.39.9 vía resolveJIDNameSaved).
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	authorLID := types.NewJID("99887766554433", types.HiddenUserServer)
	authorPN := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, authorLID.String(), "msg", "reply")
	r := &fakeResolver{
		pnByLID: map[string]types.JID{authorLID.String(): authorPN},
		names: map[string]string{
			authorLID.String(): "PushName",      // LID name (no saved)
			authorPN.String():  "Nombre Canónico", // PN name (saved)
		},
		savedJIDs: map[string]bool{authorPN.String(): true},
	}
	got := formatQuotedBlock(msg, r)
	want := "> _↩️ respondiendo a Nombre Canónico:_ msg"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_NoParticipantNoName(t *testing.T) {
	chat := types.NewJID("34600000001", types.DefaultUserServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	// 1:1 chat: el Participant es nil porque el author es la otra parte.
	msg := mkQuotedTextMsg(chat, sender, "", "qué tal", "bien")
	got := formatQuotedBlock(msg, nil)
	want := "> _↩️ respondiendo:_ qué tal"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_TruncatesLongQuote(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	long := strings.Repeat("a", 300)
	msg := mkQuotedTextMsg(chat, sender, author.String(), long, "ok")
	got := formatQuotedBlock(msg, nil)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis on truncated quote, got: %q", got)
	}
	// 200 runas + "…" + header
	if !strings.Contains(got, strings.Repeat("a", 200)) {
		t.Errorf("expected 200 'a' chars in quote, got: %q", got)
	}
}

func TestFormatQuotedBlock_MultilineQuotedTextPrefixesEachLine(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "linea1\nlinea2\nlinea3", "ok")
	got := formatQuotedBlock(msg, nil)
	// Sin resolver → no saved → ~+34… (v0.44.3).
	want := "> _↩️ respondiendo a ~+34600000099:_ linea1\n> linea2\n> linea3"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_ImageQuoteUsesPlaceholder(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := &events.Message{
		Message: &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto("mira esta foto"),
				ContextInfo: &waE2E.ContextInfo{
					Participant: proto(author.String()),
					QuotedMessage: &waE2E.Message{
						ImageMessage: &waE2E.ImageMessage{
							Caption: proto("foto del verano"),
						},
					},
				},
			},
		},
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender},
		},
	}
	got := formatQuotedBlock(msg, nil)
	if !strings.Contains(got, "🖼️ foto del verano") {
		t.Errorf("expected image quote placeholder, got: %q", got)
	}
}

func TestFormatQuotedBlock_AudioPTTPlaceholder(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := &events.Message{
		Message: &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto("dale, te escucho"),
				ContextInfo: &waE2E.ContextInfo{
					Participant: proto(author.String()),
					QuotedMessage: &waE2E.Message{
						AudioMessage: &waE2E.AudioMessage{
							PTT: protoBool(true),
						},
					},
				},
			},
		},
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender},
		},
	}
	got := formatQuotedBlock(msg, nil)
	if !strings.Contains(got, "🎤 [nota de voz]") {
		t.Errorf("expected PTT placeholder, got: %q", got)
	}
}

// Avoid unused import errors if waCommon is not referenced.
var _ = waCommon.MessageKey{}
