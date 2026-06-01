package manager

import (
	"context"
	"testing"
	"time"
)

// inBootstrapWindow es un helper interno usado durante el arranque
// para suprimir webhooks ruidosos. Cubrimos los 3 casos: sin bootstrap
// activo, dentro de la ventana, fuera de la ventana.
func TestInBootstrapWindow(t *testing.T) {
	cases := []struct {
		name string
		set  time.Time
		want bool
	}{
		{"zero value (no bootstrap set)", time.Time{}, false},
		{"future (still inside window)", time.Now().Add(5 * time.Second), true},
		{"past (window expired)", time.Now().Add(-5 * time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{bootstrapWindowUntil: tc.set}
			got := m.inBootstrapWindow()
			if got != tc.want {
				t.Errorf("inBootstrapWindow() = %v, want %v (set=%v)", got, tc.want, tc.set)
			}
		})
	}
}

// stubResolver satisface OwnerTagResolver capturando llamadas para
// validar el ownerTag wrapper sin necesidad de un Postgres real.
type stubResolver struct {
	called string
	ret    string
}

func (s *stubResolver) OwnerTagFor(_ context.Context, name string) string {
	s.called = name
	return s.ret
}

func TestOwnerTag_NilResolver(t *testing.T) {
	m := &Manager{}
	if got := m.ownerTag("anything"); got != "" {
		t.Errorf("nil resolver should return empty, got %q", got)
	}
}

func TestOwnerTag_WithResolver(t *testing.T) {
	stub := &stubResolver{ret: "client-xyz"}
	m := &Manager{ownerTagResolver: stub}
	got := m.ownerTag("ATC")
	if got != "client-xyz" {
		t.Errorf("got %q, want %q", got, "client-xyz")
	}
	if stub.called != "ATC" {
		t.Errorf("resolver called with %q, want %q", stub.called, "ATC")
	}
}

func TestPhoneFromJID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"34604021705@s.whatsapp.net", "34604021705"},
		{"34604021705:92@s.whatsapp.net", "34604021705"}, // strip device suffix
		{"41961931190522@lid", ""},                       // LID nunca expone teléfono
		{"123@lid", ""},
		{"not-a-jid", ""},
		{"@s.whatsapp.net", ""}, // user vacío con server PN → técnicamente vacío
		{"abc:1@s.whatsapp.net", "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := phoneFromJID(tc.in)
			if got != tc.want {
				t.Errorf("phoneFromJID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
