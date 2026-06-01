package bridge

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestResolveMentions_PNSaved(t *testing.T) {
	// @<phone> mention con contacto saved → @Nombre (sin tilde)
	r := &fakeResolver{
		names:     map[string]string{"34600000099@s.whatsapp.net": "Ivan Madrid"},
		savedJIDs: map[string]bool{"34600000099@s.whatsapp.net": true},
	}
	text := "@34600000099 buenos días"
	got := resolveMentions(text, []string{"34600000099@s.whatsapp.net"}, r, MentionTemplateDefault)
	want := "@Ivan Madrid buenos días"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveMentions_PNUnsavedTilde(t *testing.T) {
	r := &fakeResolver{
		names: map[string]string{"34600000099@s.whatsapp.net": "Random"},
	}
	got := resolveMentions("hola @34600000099", []string{"34600000099@s.whatsapp.net"}, r, MentionTemplateDefault)
	want := "hola @~Random"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveMentions_LIDWithSavedPN(t *testing.T) {
	// LID con PN saved → usa nombre canónico del PN
	r := &fakeResolver{
		pnByLID: map[string]types.JID{
			"148855681191942@lid": {User: "34600000099", Server: types.DefaultUserServer},
		},
		names: map[string]string{
			"148855681191942@lid":      "PushName",
			"34600000099@s.whatsapp.net": "Ivan Madrid",
		},
		savedJIDs: map[string]bool{"34600000099@s.whatsapp.net": true},
	}
	text := "@148855681191942 buenos días"
	got := resolveMentions(text, []string{"148855681191942@lid"}, r, MentionTemplateDefault)
	want := "@Ivan Madrid buenos días"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveMentions_NoResolverFallsBackToPhone(t *testing.T) {
	got := resolveMentions("hola @34600000099", []string{"34600000099@s.whatsapp.net"}, nil, MentionTemplateDefault)
	// Sin resolver, no hay name → fallback a phone E.164 con `+`
	want := "hola @+34600000099"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveMentions_EmptyTemplateDisables(t *testing.T) {
	r := &fakeResolver{
		names:     map[string]string{"34600000099@s.whatsapp.net": "Ivan"},
		savedJIDs: map[string]bool{"34600000099@s.whatsapp.net": true},
	}
	text := "@34600000099 hola"
	got := resolveMentions(text, []string{"34600000099@s.whatsapp.net"}, r, "")
	if got != text {
		t.Errorf("empty template should not modify: got %q", got)
	}
}

func TestResolveMentions_MultipleMentionsAllResolved(t *testing.T) {
	jidA := "34600000001@s.whatsapp.net"
	jidB := "34600000002@s.whatsapp.net"
	r := &fakeResolver{
		names: map[string]string{
			jidA: "Ana",
			jidB: "Beatriz",
		},
		savedJIDs: map[string]bool{jidA: true, jidB: true},
	}
	text := "@34600000001 y @34600000002 atentos"
	got := resolveMentions(text, []string{jidA, jidB}, r, MentionTemplateDefault)
	want := "@Ana y @Beatriz atentos"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveMentions_TokenNotInTextIsNoOp(t *testing.T) {
	r := &fakeResolver{
		names: map[string]string{"34600000099@s.whatsapp.net": "Ivan"},
	}
	// El array tiene un JID que NO está en el texto inline
	text := "hola sin mencion"
	got := resolveMentions(text, []string{"34600000099@s.whatsapp.net"}, r, MentionTemplateDefault)
	if got != text {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestResolveMentions_LIDFallsBackToRedactedPhone(t *testing.T) {
	// v0.53.1: LID sin PN resoluble + sin nombre → fallback a
	// RedactedPhone que WA expone para privacy mode.
	lidJID := "999111222333444@lid"
	r := &fakeResolver{
		// nada en names ni en pnByLID — LID completamente unresolved
		redactedPhones: map[string]string{lidJID: "+1∙∙∙∙∙∙∙∙80"},
	}
	got := resolveMentions("hola @999111222333444", []string{lidJID}, r, MentionTemplateDefault)
	want := "hola @+1∙∙∙∙∙∙∙∙80"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveMentions_LIDNoFallbackStaysRaw(t *testing.T) {
	// LID sin name, sin PN, sin RedactedPhone → mantiene texto raw.
	lidJID := "999111222333444@lid"
	r := &fakeResolver{}
	got := resolveMentions("hola @999111222333444", []string{lidJID}, r, MentionTemplateDefault)
	want := "hola @999111222333444"
	if got != want {
		t.Errorf("got %q, want unchanged %q", got, want)
	}
}

func TestResolveMentions_CustomTemplate(t *testing.T) {
	r := &fakeResolver{
		names:     map[string]string{"34600000099@s.whatsapp.net": "Ivan"},
		savedJIDs: map[string]bool{"34600000099@s.whatsapp.net": true},
	}
	// Template custom con phone Y name
	tpl := "**@$name** ($phone)"
	got := resolveMentions("@34600000099 confirmá", []string{"34600000099@s.whatsapp.net"}, r, tpl)
	want := "**@Ivan** (+34600000099) confirmá"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
