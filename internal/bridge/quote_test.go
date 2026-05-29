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

func TestFormatQuotedBlock_GroupReplyUnsavedAuthor(t *testing.T) {
	// v0.44.4: header en su propia línea, code block con ↪ + phone +
	// middle dot + ~name (no saved).
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "hola, qué tal", "todo bien")
	r := &fakeResolver{
		names: map[string]string{author.String(): "Pepito"},
		// no en savedJIDs → no saved → ~
	}
	got := formatQuotedBlock(msg, r)
	want := "> `↪ +34600000099 · ~Pepito`\n> hola, qué tal"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_GroupReplySavedAuthor(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "msg cita", "reply")
	r := &fakeResolver{
		names:     map[string]string{author.String(): "Pepito Saved"},
		savedJIDs: map[string]bool{author.String(): true},
	}
	got := formatQuotedBlock(msg, r)
	want := "> `↪ +34600000099 · Pepito Saved`\n> msg cita"
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

func TestFormatQuotedBlock_NoResolverFallsBackToPhoneOnly(t *testing.T) {
	// Sin resolver: no podemos saber name ni saved. Solo phone E.164
	// (no saved → header sin tilde porque no hay nombre que tildear,
	// solo phone, y phone no lleva ~).
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "msg cita", "reply")
	got := formatQuotedBlock(msg, nil)
	want := "> `↪ +34600000099`\n> msg cita"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_OneOnOneNoHeader(t *testing.T) {
	// 1:1 chat: Participant nil. Sin header — el contexto del author
	// es trivial (el otro extremo del chat).
	chat := types.NewJID("34600000001", types.DefaultUserServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	msg := mkQuotedTextMsg(chat, sender, "", "qué tal", "bien")
	got := formatQuotedBlock(msg, nil)
	want := "> qué tal"
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
	if !strings.Contains(got, strings.Repeat("a", 200)) {
		t.Errorf("expected 200 'a' chars in quote, got: %q", got)
	}
}

func TestFormatQuotedBlock_MultilineQuoted(t *testing.T) {
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	author := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, author.String(), "linea1\nlinea2\nlinea3", "ok")
	got := formatQuotedBlock(msg, nil)
	// header (`↪ +num`) en su línea, luego cada línea del citado en > .
	want := "> `↪ +34600000099`\n> linea1\n> linea2\n> linea3"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatQuotedBlock_LIDAuthorWithSavedPN(t *testing.T) {
	// Hereda el fix v0.39.9 vía resolveJIDNameSaved: si el author es
	// LID con PushName pero PN saved, usa el canónico SIN ~.
	chat := types.NewJID("120363111@g.us", types.GroupServer)
	sender := types.NewJID("34600000001", types.DefaultUserServer)
	authorLID := types.NewJID("99887766554433", types.HiddenUserServer)
	authorPN := types.NewJID("34600000099", types.DefaultUserServer)

	msg := mkQuotedTextMsg(chat, sender, authorLID.String(), "msg", "reply")
	r := &fakeResolver{
		pnByLID: map[string]types.JID{authorLID.String(): authorPN},
		names: map[string]string{
			authorLID.String(): "PushName",
			authorPN.String():  "Nombre Canónico",
		},
		savedJIDs: map[string]bool{authorPN.String(): true},
	}
	got := formatQuotedBlock(msg, r)
	want := "> `↪ +34600000099 · Nombre Canónico`\n> msg"
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
