package bridge

import (
	"testing"
)

func TestNormalizeJid(t *testing.T) {
	cases := []struct{ in, want string }{
		{"34604021705:92@s.whatsapp.net", "34604021705"},
		{"34604021705@s.whatsapp.net", "34604021705"},
		{"41961931190522:5@lid", "41961931190522"},
		{"41961931190522@lid", "41961931190522"},
		{"no-suffix", "no-suffix"},
		{"", ""},
	}
	for _, c := range cases {
		got := normalizeJid(c.in)
		if got != c.want {
			t.Errorf("normalizeJid(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeContent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hola mundo", "hola mundo"},
		{"*Hola* mundo", "hola mundo"},
		{"_Italic_ text", "italic text"},
		{"`code` here", "code here"},
		{"~strike~ text", "strike text"},
		{"  spaces   matter  ", "spaces matter"},
		{"Mixed *_~`Bold`~_*", "mixed bold"},
		{"", ""},
	}
	for _, c := range cases {
		got := normalizeContent(c.in)
		if got != c.want {
			t.Errorf("normalizeContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashContentDeterministic(t *testing.T) {
	if hashContent("hola") != hashContent("hola") {
		t.Error("hashContent must be deterministic for same input")
	}
	if hashContent("hola") == hashContent("HOLA") {
		t.Error("hashContent must be case-sensitive (normalize before hashing if needed)")
	}
	if len(hashContent("any")) != 16 {
		t.Errorf("hashContent should return 16 hex chars, got %d", len(hashContent("any")))
	}
}

func TestSpamguardTracker_FirstSeenNotBlocked(t *testing.T) {
	tr := NewSpamguardTracker()
	blocked, count := tr.CheckAndRecord("SAT-X", "34611111111@s.whatsapp.net", "Hola, ¿cómo estás?")
	if blocked {
		t.Error("first message must not be blocked")
	}
	if count != 0 {
		t.Errorf("counter should be 0 on first, got %d", count)
	}
}

func TestSpamguardTracker_DuplicateBlocked(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34611111111@s.whatsapp.net"
	content := "Saludos cordiales, le atendemos enseguida"

	if b, _ := tr.CheckAndRecord("SAT-X", jid, content); b {
		t.Fatal("first should not be blocked")
	}
	b, count := tr.CheckAndRecord("SAT-X", jid, content)
	if !b {
		t.Error("duplicate must be blocked")
	}
	if count != 1 {
		t.Errorf("counter should be 1 after 1 block, got %d", count)
	}
}

func TestSpamguardTracker_TwoBackHistoryBlocked(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34611111111@s.whatsapp.net"

	tr.CheckAndRecord("SAT-X", jid, "Mensaje A largo suficiente")
	tr.CheckAndRecord("SAT-X", jid, "Mensaje B largo distinto suficiente")
	// El mensaje A debe seguir en la historia (slot 2/2)
	b, _ := tr.CheckAndRecord("SAT-X", jid, "Mensaje A largo suficiente")
	if !b {
		t.Error("message 2 back must still be blocked (last-2 history)")
	}
}

func TestSpamguardTracker_ThreeBackEvicted(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34611111111@s.whatsapp.net"

	tr.CheckAndRecord("SAT-X", jid, "Mensaje A largo")
	tr.CheckAndRecord("SAT-X", jid, "Mensaje B largo")
	tr.CheckAndRecord("SAT-X", jid, "Mensaje C largo")
	// A ya debe haber sido evicted del slot [latest, prev]
	b, _ := tr.CheckAndRecord("SAT-X", jid, "Mensaje A largo")
	if b {
		t.Error("message 3 back should have been evicted from history (max 2)")
	}
}

func TestSpamguardTracker_PerInstance(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34611111111@s.whatsapp.net"
	content := "Mismo contenido distinta instancia"

	tr.CheckAndRecord("SAT-X", jid, content)
	// La MISMA palabra pero en OTRA instancia no debe bloquearse
	b, _ := tr.CheckAndRecord("SAT-Y", jid, content)
	if b {
		t.Error("history must be per-instance, not global")
	}
}

func TestSpamguardTracker_PerJid(t *testing.T) {
	tr := NewSpamguardTracker()
	content := "Mismo mensaje a distinto contacto"

	tr.CheckAndRecord("SAT-X", "34611111111@s.whatsapp.net", content)
	// Mismo contenido, JID distinto, no bloquea
	b, _ := tr.CheckAndRecord("SAT-X", "34622222222@s.whatsapp.net", content)
	if b {
		t.Error("history must be per-jid, not per-instance only")
	}
}

func TestSpamguardTracker_JidNormalization(t *testing.T) {
	tr := NewSpamguardTracker()
	content := "Mensaje con device suffix"

	tr.CheckAndRecord("SAT-X", "34611111111:0@s.whatsapp.net", content)
	// Mismo JID-user pero con device suffix distinto debe contar como mismo destino
	b, _ := tr.CheckAndRecord("SAT-X", "34611111111:99@s.whatsapp.net", content)
	if !b {
		t.Error("device suffix differences must normalize to same JID")
	}
}

func TestSpamguardTracker_EmptyContentNotBlocked(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34611111111@s.whatsapp.net"

	tr.CheckAndRecord("SAT-X", jid, "")
	b, _ := tr.CheckAndRecord("SAT-X", jid, "")
	if b {
		t.Error("empty content should not produce a blocking hash")
	}
}

func TestSpamguardTracker_CounterAccumulates(t *testing.T) {
	tr := NewSpamguardTracker()
	jid := "34611111111@s.whatsapp.net"

	tr.CheckAndRecord("SAT-X", jid, "Hola otra vez de nuevo")
	tr.CheckAndRecord("SAT-X", jid, "Hola otra vez de nuevo") // block 1
	tr.CheckAndRecord("SAT-X", jid, "Hola otra vez de nuevo") // block 2

	if got := tr.BlockCount("SAT-X"); got != 2 {
		t.Errorf("BlockCount = %d, want 2", got)
	}
	if got := tr.BlockCount("SAT-Y"); got != 0 {
		t.Errorf("BlockCount for other instance should be 0, got %d", got)
	}
}
