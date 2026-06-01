package bridge

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingSender captura los args pasados a SendText/SendTextReply
// para assertions sobre el flujo reply-to outgoing v0.44.0.
type recordingSender struct {
	mu sync.Mutex

	textCalls       int32
	replyCalls      atomic.Int32
	lastQuotedWAID  string
	lastQuotedJID   string
	lastQuotedText  string
	lastReplyText   string
	lastReplyTarget string
}

func (s *recordingSender) SendText(_ context.Context, _, remoteJid, content string) (string, error) {
	atomic.AddInt32(&s.textCalls, 1)
	s.mu.Lock()
	s.lastReplyText = content
	s.lastReplyTarget = remoteJid
	s.mu.Unlock()
	return "WAID:fresh-text", nil
}

func (s *recordingSender) SendMedia(_ context.Context, _, _, _, _, _, _ string, _ []byte) (string, error) {
	return "WAID:fresh-media", nil
}

func (s *recordingSender) SendTextReply(_ context.Context, _, remoteJid, content, quotedWAID, quotedJID, quotedText string) (string, error) {
	s.replyCalls.Add(1)
	s.mu.Lock()
	s.lastQuotedWAID = quotedWAID
	s.lastQuotedJID = quotedJID
	s.lastQuotedText = quotedText
	s.lastReplyText = content
	s.lastReplyTarget = remoteJid
	s.mu.Unlock()
	return "WAID:fresh-reply", nil
}

// SendMediaReply para tests reusa la misma lógica de captura — los
// reply-to media tests no son granulares en v0.51.0.
func (s *recordingSender) SendMediaReply(_ context.Context, _, remoteJid, _, _, _, _ string, _ []byte, quotedWAID, quotedJID, quotedText string) (string, error) {
	s.replyCalls.Add(1)
	s.mu.Lock()
	s.lastQuotedWAID = quotedWAID
	s.lastQuotedJID = quotedJID
	s.lastQuotedText = quotedText
	s.lastReplyTarget = remoteJid
	s.mu.Unlock()
	return "WAID:fresh-media-reply", nil
}

// newReplyTestOutgoing arma un Outgoing con un Incoming que tiene el
// tracker de msg_history habilitado, conecta replyToOutgoing y devuelve
// también el sender que captura las calls.
func newReplyTestOutgoing(t *testing.T) (*Outgoing, *Incoming, *recordingSender) {
	t.Helper()
	rs := &recordingSender{}
	logger := slog.New(slog.DiscardHandler)
	inc := NewIncomingDynamic(nil, nil, logger, func(string) int { return 11 })
	inc.EnableRetroactiveNameUpdate(50)
	out := NewOutgoing(rs, &fakeRouter{}, nil, noopSpamguard{}, NewSpamguardTracker(), logger)
	out.EnableReplyToOutgoing(inc)
	return out, inc, rs
}

func TestReplyToOutgoing_SendsAsReplyWhenTracked(t *testing.T) {
	out, inc, rs := newReplyTestOutgoing(t)

	// Sembramos un incoming "original" en el tracker.
	inc.msgHistory.Record("inst1", "34604021705@s.whatsapp.net", trackedMsg{
		convID:    100,
		msgID:     5000,
		body:      "hola, qué tal?",
		postedAt:  time.Now(),
		waid:      "WAID:original-incoming-123",
		hasPrefix: false,
	})

	// Construimos un payload outgoing con in_reply_to=5000.
	p := makeOutgoing("34611111111@s.whatsapp.net", "bien, gracias")
	p.ID = 0 // skip UpdateMessageSourceID — fakeRouter returns nil client
	p.ContentAttributes = &struct {
		InReplyTo int `json:"in_reply_to"`
	}{InReplyTo: 5000}

	if err := out.HandleFor(context.Background(), "inst1", p); err != nil {
		t.Fatalf("HandleFor: %v", err)
	}

	if rs.replyCalls.Load() != 1 {
		t.Errorf("expected 1 SendTextReply call, got %d", rs.replyCalls.Load())
	}
	if atomic.LoadInt32(&rs.textCalls) != 0 {
		t.Errorf("expected 0 SendText (plain) calls, got %d", atomic.LoadInt32(&rs.textCalls))
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.lastQuotedWAID != "WAID:original-incoming-123" {
		t.Errorf("quotedWAID = %q", rs.lastQuotedWAID)
	}
	if rs.lastQuotedJID != "34604021705@s.whatsapp.net" {
		t.Errorf("quotedJID = %q", rs.lastQuotedJID)
	}
	if rs.lastQuotedText != "hola, qué tal?" {
		t.Errorf("quotedText = %q", rs.lastQuotedText)
	}
	if rs.lastReplyText != "bien, gracias" {
		t.Errorf("reply content = %q", rs.lastReplyText)
	}
}

func TestReplyToOutgoing_FallsBackToPlainTextWhenNotFound(t *testing.T) {
	out, _, rs := newReplyTestOutgoing(t)

	// payload con in_reply_to pero msgID no tracked.
	p := makeOutgoing("34611111111@s.whatsapp.net", "hola")
	p.ID = 0 // skip UpdateMessageSourceID
	p.ContentAttributes = &struct {
		InReplyTo int `json:"in_reply_to"`
	}{InReplyTo: 9999}

	if err := out.HandleFor(context.Background(), "inst1", p); err != nil {
		t.Fatalf("HandleFor: %v", err)
	}

	if rs.replyCalls.Load() != 0 {
		t.Errorf("expected 0 SendTextReply (not tracked), got %d", rs.replyCalls.Load())
	}
	if atomic.LoadInt32(&rs.textCalls) != 1 {
		t.Errorf("expected 1 SendText fallback, got %d", atomic.LoadInt32(&rs.textCalls))
	}
}

func TestReplyToOutgoing_NoInReplyToUsesSendText(t *testing.T) {
	out, _, rs := newReplyTestOutgoing(t)

	// Sin ContentAttributes → no es reply.
	p := makeOutgoing("34611111111@s.whatsapp.net", "msg suelto")
	p.ID = 0 // skip UpdateMessageSourceID
	if err := out.HandleFor(context.Background(), "inst1", p); err != nil {
		t.Fatalf("HandleFor: %v", err)
	}

	if atomic.LoadInt32(&rs.textCalls) != 1 {
		t.Errorf("expected 1 SendText, got %d", atomic.LoadInt32(&rs.textCalls))
	}
	if rs.replyCalls.Load() != 0 {
		t.Errorf("expected 0 SendTextReply, got %d", rs.replyCalls.Load())
	}
}

func TestReplyToOutgoing_DisabledFallsBackToPlainText(t *testing.T) {
	rs := &recordingSender{}
	logger := slog.New(slog.DiscardHandler)
	// Outgoing sin EnableReplyToOutgoing → o.msgHistory == nil.
	out := NewOutgoing(rs, &fakeRouter{}, nil, noopSpamguard{}, NewSpamguardTracker(), logger)

	p := makeOutgoing("34611111111@s.whatsapp.net", "hola")
	p.ID = 0 // skip UpdateMessageSourceID
	p.ContentAttributes = &struct {
		InReplyTo int `json:"in_reply_to"`
	}{InReplyTo: 5000}

	if err := out.HandleFor(context.Background(), "inst1", p); err != nil {
		t.Fatalf("HandleFor: %v", err)
	}

	if atomic.LoadInt32(&rs.textCalls) != 1 {
		t.Errorf("expected 1 SendText (disabled), got %d", atomic.LoadInt32(&rs.textCalls))
	}
	if rs.replyCalls.Load() != 0 {
		t.Errorf("expected 0 SendTextReply, got %d", rs.replyCalls.Load())
	}
}

func TestReplyToOutgoing_EmptyWAIDFallsBack(t *testing.T) {
	out, inc, rs := newReplyTestOutgoing(t)

	// Entry "vieja" sin WAID (simula pre-v0.44.0 row warmed up).
	inc.msgHistory.Record("inst1", "34604021705@s.whatsapp.net", trackedMsg{
		convID:    100,
		msgID:     5000,
		body:      "msg viejo",
		waid:      "", // <— sin WAID
		hasPrefix: true,
	})

	p := makeOutgoing("34611111111@s.whatsapp.net", "responde")
	p.ID = 0 // skip UpdateMessageSourceID
	p.ContentAttributes = &struct {
		InReplyTo int `json:"in_reply_to"`
	}{InReplyTo: 5000}

	if err := out.HandleFor(context.Background(), "inst1", p); err != nil {
		t.Fatalf("HandleFor: %v", err)
	}

	if atomic.LoadInt32(&rs.textCalls) != 1 {
		t.Errorf("expected 1 SendText fallback, got %d", atomic.LoadInt32(&rs.textCalls))
	}
	if rs.replyCalls.Load() != 0 {
		t.Errorf("expected 0 SendTextReply, got %d", rs.replyCalls.Load())
	}
}
