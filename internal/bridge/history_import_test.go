package bridge

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// Tests del extractHistoryText helper — verifica que cada tipo de
// media genera un placeholder textual o caption según corresponda.

func TestExtractHistoryText_PlainConversation(t *testing.T) {
	m := &waE2E.Message{Conversation: proto("hola")}
	got := extractHistoryText(m)
	if got != "hola" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHistoryText_ExtendedText(t *testing.T) {
	m := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto("hola ext")},
	}
	got := extractHistoryText(m)
	if got != "hola ext" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHistoryText_ImageCaption(t *testing.T) {
	m := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{Caption: proto("foto")},
	}
	got := extractHistoryText(m)
	if got != "🖼️ foto" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHistoryText_ImageNoCaptionPlaceholder(t *testing.T) {
	m := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}
	got := extractHistoryText(m)
	if got != "🖼️ [imagen — no importada]" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHistoryText_AudioPTTPlaceholder(t *testing.T) {
	m := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{PTT: protoBool(true)},
	}
	got := extractHistoryText(m)
	if got != "🎤 [nota de voz — no importada]" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHistoryText_DocumentTitle(t *testing.T) {
	m := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{Title: proto("factura.pdf")},
	}
	got := extractHistoryText(m)
	if got != "📄 factura.pdf" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHistoryText_NilReturnsEmpty(t *testing.T) {
	if got := extractHistoryText(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestEnableHistoryImport_DaysClamped(t *testing.T) {
	inc := &Incoming{}
	inc.EnableHistoryImport(100, 3)
	if inc.historyCfg.days != 30 {
		t.Errorf("days = %d, want clamped to 30", inc.historyCfg.days)
	}
	inc.EnableHistoryImport(-5, 3)
	if inc.historyCfg.days != 1 {
		t.Errorf("days = %d, want clamped to 1", inc.historyCfg.days)
	}
}

func TestEnableHistoryImport_RateDefaultWhenInvalid(t *testing.T) {
	inc := &Incoming{}
	inc.EnableHistoryImport(7, 0)
	if inc.historyCfg.ratePerSec != 5 {
		t.Errorf("rate = %d, want default 5", inc.historyCfg.ratePerSec)
	}
}
