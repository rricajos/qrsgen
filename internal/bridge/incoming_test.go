package bridge

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// fakeResolver implementa wameow.WAResolver para tests determinísticos.
type fakeResolver struct {
	names     map[string]string    // jid (no-AD).String() → contact name
	pnByLID   map[string]types.JID // lid.String() → pn JID
	lidByPN   map[string]types.JID // pn.String() → lid JID
	groupSubj map[string]string    // group jid (no-AD).String() → subject
}

func (f *fakeResolver) ContactName(jid types.JID) string {
	if f == nil || f.names == nil {
		return ""
	}
	return f.names[jid.ToNonAD().String()]
}

func (f *fakeResolver) GroupSubject(jid types.JID) (string, bool) {
	if f == nil || f.groupSubj == nil || jid.Server != types.GroupServer {
		return "", false
	}
	v, ok := f.groupSubj[jid.ToNonAD().String()]
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func (f *fakeResolver) PNForLID(lid types.JID) (types.JID, bool) {
	if f == nil || f.pnByLID == nil {
		return types.JID{}, false
	}
	v, ok := f.pnByLID[lid.ToNonAD().String()]
	return v, ok
}

func (f *fakeResolver) LIDForPN(pn types.JID) (types.JID, bool) {
	if f == nil || f.lidByPN == nil {
		return types.JID{}, false
	}
	v, ok := f.lidByPN[pn.ToNonAD().String()]
	return v, ok
}

// DownloadAny en tests devuelve un error indicativo. Los tests de incoming
// que dependan de descarga real necesitan inyectar un downloader específico.
func (f *fakeResolver) DownloadAny(_ context.Context, _ *waE2E.Message) ([]byte, error) {
	return nil, errors.New("fakeResolver: download no implementado en tests")
}

// JIDs reutilizados — usar la forma del mundo real:
// Ricard: PN 34604021705@s.whatsapp.net ↔ LID 41961931190522@lid
// Algunos eventos vienen con device suffix (:92) y debemos normalizar.
var (
	jidRicardPN    = types.NewJID("34604021705", types.DefaultUserServer)
	jidRicardPNAD  = types.JID{User: "34604021705", Server: types.DefaultUserServer, Device: 92}
	jidRicardLID   = types.NewJID("41961931190522", types.HiddenUserServer)
	jidRicardLIDAD = types.JID{User: "41961931190522", Server: types.HiddenUserServer, Device: 4}
)

func mkMsg(chat, alt types.JID, fromMe bool, pushName string, mode types.AddressingMode) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:           chat,
				SenderAlt:      alt,
				IsFromMe:       fromMe,
				AddressingMode: mode,
			},
			PushName: pushName,
		},
	}
}

func TestResolveSender(t *testing.T) {
	resolverWithMap := &fakeResolver{
		pnByLID: map[string]types.JID{
			jidRicardLID.String(): jidRicardPN,
		},
		lidByPN: map[string]types.JID{
			jidRicardPN.String(): jidRicardLID,
		},
	}
	emptyResolver := &fakeResolver{}

	cases := []struct {
		name        string
		chat, alt   types.JID
		mode        types.AddressingMode
		r           *fakeResolver
		wantPrimary string
		wantPN      string
		wantLID     string
		wantPhone   string
	}{
		{
			name:        "PN chat, LID alt — should resolve PN canonical, both forms known",
			chat:        jidRicardPN,
			alt:         jidRicardLID,
			mode:        types.AddressingModePN,
			r:           emptyResolver,
			wantPrimary: jidRicardPN.String(),
			wantPN:      jidRicardPN.String(),
			wantLID:     jidRicardLID.String(),
			wantPhone:   "34604021705",
		},
		{
			name:        "LID chat, PN alt — PN canonical via alt",
			chat:        jidRicardLID,
			alt:         jidRicardPN,
			mode:        types.AddressingModeLID,
			r:           emptyResolver,
			wantPrimary: jidRicardPN.String(),
			wantPN:      jidRicardPN.String(),
			wantLID:     jidRicardLID.String(),
			wantPhone:   "34604021705",
		},
		{
			name:        "LID chat, no alt, LIDStore knows PN — resolves PN via store",
			chat:        jidRicardLID,
			alt:         types.JID{},
			mode:        types.AddressingModeLID,
			r:           resolverWithMap,
			wantPrimary: jidRicardPN.String(),
			wantPN:      jidRicardPN.String(),
			wantLID:     jidRicardLID.String(),
			wantPhone:   "34604021705",
		},
		{
			name:        "LID chat, no alt, no store — LID-only canonical (no phone fabricated)",
			chat:        jidRicardLID,
			alt:         types.JID{},
			mode:        types.AddressingModeLID,
			r:           emptyResolver,
			wantPrimary: jidRicardLID.String(),
			wantPN:      "",
			wantLID:     jidRicardLID.String(),
			wantPhone:   "",
		},
		{
			name:        "PN chat, no alt, LIDStore knows LID — fills lid via store",
			chat:        jidRicardPN,
			alt:         types.JID{},
			mode:        types.AddressingModePN,
			r:           resolverWithMap,
			wantPrimary: jidRicardPN.String(),
			wantPN:      jidRicardPN.String(),
			wantLID:     jidRicardLID.String(),
			wantPhone:   "34604021705",
		},
		{
			name:        "PN chat with device suffix — phone stripped via ToNonAD",
			chat:        jidRicardPNAD,
			alt:         jidRicardLIDAD,
			mode:        types.AddressingModePN,
			r:           emptyResolver,
			wantPrimary: jidRicardPN.String(),
			wantPN:      jidRicardPN.String(),
			wantLID:     jidRicardLID.String(),
			wantPhone:   "34604021705",
		},
		{
			name:        "nil resolver does not panic, still resolves alt directly",
			chat:        jidRicardLID,
			alt:         jidRicardPN,
			mode:        types.AddressingModeLID,
			r:           nil,
			wantPrimary: jidRicardPN.String(),
			wantPN:      jidRicardPN.String(),
			wantLID:     jidRicardLID.String(),
			wantPhone:   "34604021705",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := mkMsg(tc.chat, tc.alt, false, "", tc.mode)
			r := tc.r // satisfies wameow.WAResolver when not nil
			var got resolvedSender
			if r == nil {
				got = resolveSender(msg, nil)
			} else {
				got = resolveSender(msg, r)
			}
			if got.primaryJID != tc.wantPrimary {
				t.Errorf("primaryJID = %q, want %q", got.primaryJID, tc.wantPrimary)
			}
			if got.pnJID != tc.wantPN {
				t.Errorf("pnJID = %q, want %q", got.pnJID, tc.wantPN)
			}
			if got.lidJID != tc.wantLID {
				t.Errorf("lidJID = %q, want %q", got.lidJID, tc.wantLID)
			}
			if got.phone != tc.wantPhone {
				t.Errorf("phone = %q, want %q", got.phone, tc.wantPhone)
			}
		})
	}
}

func TestPickName(t *testing.T) {
	rsPN := resolvedSender{pnJID: jidRicardPN.String(), primaryJID: jidRicardPN.String(), phone: "34604021705"}
	rsLIDOnly := resolvedSender{lidJID: jidRicardLID.String(), primaryJID: jidRicardLID.String()}
	rsEmpty := resolvedSender{primaryJID: "unknown@something"}

	cases := []struct {
		name         string
		resolvedName string
		rs           resolvedSender
		want         string
	}{
		{"resolved name takes precedence", "Richard", rsPN, "Richard"},
		{"no name, has phone — prefix with WhatsApp", "", rsPN, "WhatsApp 34604021705"},
		{"no name, LID only — prefix with WhatsApp LID + short", "", rsLIDOnly, "WhatsApp LID …190522"},
		{"no name nothing — fallback primary", "", rsEmpty, "unknown@something"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickName(tc.resolvedName, tc.rs)
			if got != tc.want {
				t.Errorf("pickName(%q, ...) = %q, want %q", tc.resolvedName, got, tc.want)
			}
		})
	}
}

func TestLidShort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"41961931190522@lid", "…190522"},
		{"123@lid", "123"},
		{"abc", "abc"},
	}
	for _, tc := range cases {
		got := lidShort(tc.in)
		if got != tc.want {
			t.Errorf("lidShort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtractTextContent(t *testing.T) {
	// nil message
	if got := extractTextContent(&events.Message{}); got != "" {
		t.Errorf("nil message = %q, want empty", got)
	}
}

// mkGroupMsg construye un events.Message como si llegara desde un grupo.
// chat es el JID del grupo (@g.us), sender es el participante.
func mkGroupMsg(chat, sender types.JID, pushName string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:           chat,
				Sender:         sender,
				IsFromMe:       false,
				AddressingMode: types.AddressingModePN,
			},
			PushName: pushName,
		},
	}
}

func TestApplyGroupSenderPrefix(t *testing.T) {
	groupJID := types.NewJID("120363111222333444", types.GroupServer)
	senderPN := types.NewJID("34640047775", types.DefaultUserServer)
	senderLID := types.NewJID("99887766554433221100", types.HiddenUserServer)

	t.Run("PN sender (Spain) with push name → name + italic phone", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "Jean Paul")
		got := applyGroupSenderPrefix("hola", msg, nil)
		want := "Jean Paul _(+34 640 04 77 75)_\nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("PN sender empty push name, resolver knows contact name", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "")
		r := &fakeResolver{names: map[string]string{senderPN.String(): "Jean Paul (CRM)"}}
		got := applyGroupSenderPrefix("hola", msg, r)
		want := "Jean Paul (CRM) _(+34 640 04 77 75)_\nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("LID sender resolved to PN via resolver", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderLID, "Anon")
		r := &fakeResolver{
			pnByLID: map[string]types.JID{senderLID.String(): senderPN},
		}
		got := applyGroupSenderPrefix("hola", msg, r)
		want := "Anon _(+34 640 04 77 75)_\nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("LID sender unresolvable, only push name → name + colon", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderLID, "Pseudo")
		got := applyGroupSenderPrefix("hola", msg, nil)
		want := "Pseudo:\nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("PN sender no name → bare phone + colon", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "")
		got := applyGroupSenderPrefix("hola", msg, nil)
		want := "+34 640 04 77 75:\nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty body keeps prefix without trailing newline", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "Jean Paul")
		got := applyGroupSenderPrefix("", msg, nil)
		want := "Jean Paul _(+34 640 04 77 75)_"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no identification possible — body unchanged", func(t *testing.T) {
		// Sender LID sin resolver y sin push name: no podemos identificar.
		// Mejor no prefijar que prefijar basura.
		msg := mkGroupMsg(groupJID, senderLID, "")
		got := applyGroupSenderPrefix("hola", msg, nil)
		if got != "hola" {
			t.Errorf("got %q, want %q", got, "hola")
		}
	})
}

func TestFormatE164(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"Spain mobile (3-2-2-2)", "34604021705", "+34 604 02 17 05"},
		{"Spain 9-digit landline", "34931234567", "+34 931 23 45 67"},
		{"France (generic 3-3-3)", "33612345678", "+33 612 345 678"},
		{"Germany", "4915112345678", "+49 151 123 456 78"},
		{"UK", "447911123456", "+44 791 112 345 6"},
		{"US (1-digit CC)", "14155551234", "+1 415 555 123 4"},
		{"Portugal (3-digit CC)", "351912345678", "+351 912 345 678"},
		{"Italy", "393331234567", "+39 333 123 456 7"},
		{"Mexico", "525512345678", "+52 551 234 567 8"},
		{"Unknown CC — compact", "999123456", "+999123456"},
		{"Only CC (rare)", "34", "+34"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatE164(tc.in)
			if got != tc.want {
				t.Errorf("formatE164(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFakeResolver_GroupSubject(t *testing.T) {
	groupJID := types.NewJID("120363111222333444", types.GroupServer)
	userJID := types.NewJID("34640047775", types.DefaultUserServer)
	r := &fakeResolver{groupSubj: map[string]string{groupJID.String(): "IA LA CASA CLOT (GROUP)"}}

	if name, ok := r.GroupSubject(groupJID); !ok || name != "IA LA CASA CLOT (GROUP)" {
		t.Errorf("group hit: got (%q, %v), want (%q, true)", name, ok, "IA LA CASA CLOT (GROUP)")
	}
	// Non-group JID nunca debe acertar.
	if name, ok := r.GroupSubject(userJID); ok || name != "" {
		t.Errorf("non-group: got (%q, %v), want (\"\", false)", name, ok)
	}
	// Group sin entrada → miss.
	other := types.NewJID("999@g.us", types.GroupServer)
	if name, ok := r.GroupSubject(other); ok || name != "" {
		t.Errorf("group miss: got (%q, %v), want (\"\", false)", name, ok)
	}
}
