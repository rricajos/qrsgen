package manager

import "testing"

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
