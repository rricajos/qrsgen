package bridge

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
)

// fakeSender registra las llamadas a SendText/SendMedia para que los tests
// puedan verificar qué se envió (o que NO se envió, según el caso).
type fakeSender struct {
	sentTexts atomic.Int32
	sentMedia atomic.Int32
	failNext  bool
}

func (f *fakeSender) SendText(_ context.Context, _, _, _ string) (string, error) {
	if f.failNext {
		f.failNext = false
		return "", &mockError{msg: "send_text failed"}
	}
	f.sentTexts.Add(1)
	return "WAID:mock-text", nil
}

func (f *fakeSender) SendMedia(_ context.Context, _, _, _, _, _, _ string, _ []byte) (string, error) {
	if f.failNext {
		f.failNext = false
		return "", &mockError{msg: "send_media failed"}
	}
	f.sentMedia.Add(1)
	return "WAID:mock-media", nil
}

// SendTextReply en test default delega en SendText (sin diferenciar
// reply en assertions). Los tests específicos de reply-to lo overridean.
func (f *fakeSender) SendTextReply(ctx context.Context, instance, remoteJid, content, _, _, _ string) (string, error) {
	return f.SendText(ctx, instance, remoteJid, content)
}

// SendMediaReply test default delega en SendMedia.
func (f *fakeSender) SendMediaReply(ctx context.Context, instance, remoteJid, kind, mimetype, filename, caption string, data []byte, _, _, _ string) (string, error) {
	return f.SendMedia(ctx, instance, remoteJid, kind, mimetype, filename, caption, data)
}

type mockError struct{ msg string }

func (e *mockError) Error() string { return e.msg }

// noopSpamguard satisface SpamguardProvider sin habilitar el filtro.
type noopSpamguard struct{}

func (noopSpamguard) IsSpamguardEnabled(_ context.Context, _ string) bool { return false }
func (noopSpamguard) EmitLifecycle(_, _ string, _ map[string]any)         {}

func newTestOutgoing() (*Outgoing, *fakeSender) {
	s := &fakeSender{}
	logger := slog.New(slog.DiscardHandler)
	// dedup y downstream client se pasan nil — el HandleFor los gestiona
	// como opcionales (los safety nets que probamos retornan antes).
	o := NewOutgoing(s, nil, nil, noopSpamguard{}, NewSpamguardTracker(), logger)
	return o, s
}

// makeOutgoing crea un WebhookPayload outgoing válido base.
func makeOutgoing(jid, content string) WebhookPayload {
	conv := struct {
		ID                int   `json:"id"`
		InboxID           int   `json:"inbox_id"`
		AgentLastSeenAt   int64 `json:"agent_last_seen_at"`
		ContactLastSeenAt int64 `json:"contact_last_seen_at"`
		Meta              *struct {
			Sender *struct {
				PhoneNumber string `json:"phone_number"`
				Identifier  string `json:"identifier"`
			} `json:"sender"`
		} `json:"meta"`
	}{
		ID:      1,
		InboxID: 0,
		Meta: &struct {
			Sender *struct {
				PhoneNumber string `json:"phone_number"`
				Identifier  string `json:"identifier"`
			} `json:"sender"`
		}{
			Sender: &struct {
				PhoneNumber string `json:"phone_number"`
				Identifier  string `json:"identifier"`
			}{Identifier: jid},
		},
	}
	return WebhookPayload{
		Event:        "message_created",
		ID:           42,
		MessageType:  "outgoing",
		Private:      false,
		Content:      content,
		Conversation: &conv,
	}
}

func TestHandleFor_SkipsNonOutgoing(t *testing.T) {
	o, s := newTestOutgoing()
	p := makeOutgoing("34600000000@s.whatsapp.net", "hola")
	p.MessageType = "incoming"
	if err := o.HandleFor(context.Background(), "X", p); err != nil {
		t.Errorf("err: %v", err)
	}
	if s.sentTexts.Load() != 0 {
		t.Errorf("expected NOT to send for message_type=incoming, got %d sends", s.sentTexts.Load())
	}
}

func TestHandleFor_SkipsPrivateNotes(t *testing.T) {
	o, s := newTestOutgoing()
	p := makeOutgoing("34600000000@s.whatsapp.net", "nota interna")
	p.Private = true
	if err := o.HandleFor(context.Background(), "X", p); err != nil {
		t.Errorf("err: %v", err)
	}
	if s.sentTexts.Load() != 0 {
		t.Errorf("private notes must not be sent, got %d sends", s.sentTexts.Load())
	}
}

func TestHandleFor_SkipsEchoWAID(t *testing.T) {
	o, s := newTestOutgoing()
	p := makeOutgoing("34600000000@s.whatsapp.net", "hola")
	p.SourceID = "WAID:abc123"
	if err := o.HandleFor(context.Background(), "X", p); err != nil {
		t.Errorf("err: %v", err)
	}
	if s.sentTexts.Load() != 0 {
		t.Errorf("echo (WAID prefix) must not be re-sent, got %d sends", s.sentTexts.Load())
	}
}

func TestHandleFor_SkipsQrsgenOpsContact(t *testing.T) {
	o, s := newTestOutgoing()
	p := makeOutgoing("qrsgen-qr-X@something", "ops msg")
	if err := o.HandleFor(context.Background(), "X", p); err != nil {
		t.Errorf("err: %v", err)
	}
	if s.sentTexts.Load() != 0 {
		t.Errorf("qrsgen-qr-* contacts must not receive WhatsApp dispatch, got %d sends", s.sentTexts.Load())
	}
}

func TestHandleFor_SkipsMissingRemoteJid(t *testing.T) {
	o, s := newTestOutgoing()
	p := makeOutgoing("", "hola")
	if err := o.HandleFor(context.Background(), "X", p); err != nil {
		t.Errorf("err: %v", err)
	}
	if s.sentTexts.Load() != 0 {
		t.Errorf("missing remoteJid must skip, got %d sends", s.sentTexts.Load())
	}
}

func TestHandleFor_SkipsEmptyContentNoAttachments(t *testing.T) {
	o, s := newTestOutgoing()
	p := makeOutgoing("34600000000@s.whatsapp.net", "")
	if err := o.HandleFor(context.Background(), "X", p); err != nil {
		t.Errorf("err: %v", err)
	}
	if s.sentTexts.Load() != 0 || s.sentMedia.Load() != 0 {
		t.Errorf("empty content + no attachments must skip")
	}
}

// El happy-path completo (SendText real + PATCH source_id en downstream)
// requiere un fake downstream.Client; está cubierto indirectamente por
// los tests de integración. Aquí solo verificamos los safety nets, que
// es donde más bugs latentes han aparecido históricamente.

func TestSpamguardTracker_PerInstanceSpam(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34600000000@s.whatsapp.net"
	if b, _ := tr.CheckAndRecord("X", jid, "hola"); b {
		t.Error("first message must not block")
	}
	if b, _ := tr.CheckAndRecord("X", jid, "hola"); !b {
		t.Error("second identical must block")
	}
	// Misma payload distinto instance → no bloquea
	if b, _ := tr.CheckAndRecord("Y", jid, "hola"); b {
		t.Error("different instance must not see the spam from X")
	}
}
