package bridge

import "testing"

// Tests del template configurable del header de sender (v0.45.0).

func TestRenderSenderHeader_DefaultTemplate(t *testing.T) {
	si := senderInfo{phone: "34604021705", phoneFmt: "+34604021705", name: "Ricard", saved: true}
	got, ok := renderSenderHeader(si, "")
	if !ok || got != "`+34604021705 · Ricard`" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderSenderHeader_DefaultUnsavedTilde(t *testing.T) {
	si := senderInfo{phone: "34604021705", phoneFmt: "+34604021705", name: "Richard", saved: false}
	got, _ := renderSenderHeader(si, "")
	want := "`+34604021705 · ~Richard`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSenderHeader_CustomBoldNameTemplate(t *testing.T) {
	// Template del operador: phone en code + bold name aparte.
	tpl := "`$phone` · **$name**"
	si := senderInfo{phone: "34611887663", phoneFmt: "+34611887663", name: "Agustina", saved: false}
	got, _ := renderSenderHeader(si, tpl)
	want := "`+34611887663` · **~Agustina**"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSenderHeader_CustomPlainTemplate(t *testing.T) {
	tpl := "$phone | $name"
	si := senderInfo{phone: "34604021705", phoneFmt: "+34604021705", name: "Ricard", saved: true}
	got, _ := renderSenderHeader(si, tpl)
	want := "+34604021705 | Ricard"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSenderHeader_OnlyNameFallback(t *testing.T) {
	// Solo nombre, sin phone → fallback al formato simple
	// (el template no aplica para no romper layout).
	si := senderInfo{name: "Ricard", saved: false}
	got, _ := renderSenderHeader(si, "`$phone · $name`")
	want := "`~Ricard:`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSenderHeader_OnlyPhoneFallback(t *testing.T) {
	si := senderInfo{phone: "34604021705", phoneFmt: "+34604021705"}
	got, _ := renderSenderHeader(si, "`$phone · $name`")
	want := "`+34604021705:`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSenderHeader_NoIdentificationReturnsFalse(t *testing.T) {
	si := senderInfo{}
	_, ok := renderSenderHeader(si, "")
	if ok {
		t.Error("expected false when nothing to render")
	}
}

func TestRenderSenderHeader_TildeAppliedToNameToken(t *testing.T) {
	// El template puede envolver $name como quiera — el ~ va dentro
	// del token sustituido, sin escape.
	tpl := "**$name** ($phone)"
	si := senderInfo{phone: "34611887663", phoneFmt: "+34611887663", name: "Agustina", saved: false}
	got, _ := renderSenderHeader(si, tpl)
	want := "**~Agustina** (+34611887663)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
