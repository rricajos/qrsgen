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
	pfp       map[string]fakePFP   // jid (no-AD).String() → profile picture
	savedJIDs map[string]bool      // jid (no-AD).String() → IsContactSaved
}

// fakePFP simula el retorno de GetProfilePicture / GetProfilePictureID
// en tests. id es el "current ID" que devolvería GetProfilePictureID,
// data/mime el bytes que devolvería GetProfilePicture.
type fakePFP struct {
	id   string
	data []byte
	mime string
	err  error
}

func (f *fakeResolver) GetProfilePicture(_ context.Context, jid types.JID) ([]byte, string, error) {
	if f == nil || f.pfp == nil {
		return nil, "", nil
	}
	e, ok := f.pfp[jid.ToNonAD().String()]
	if !ok {
		return nil, "", nil
	}
	return e.data, e.mime, e.err
}

func (f *fakeResolver) GetProfilePictureID(_ context.Context, jid types.JID) (string, error) {
	if f == nil || f.pfp == nil {
		return "", nil
	}
	e, ok := f.pfp[jid.ToNonAD().String()]
	if !ok {
		return "", nil
	}
	if e.err != nil {
		return "", e.err
	}
	// Sin foto = "" como ID. Si hay bytes, generamos un ID determinista
	// para tests usando la longitud + primer byte (no-cripto, solo
	// para que IDs sean estables y comparables).
	if len(e.data) == 0 {
		return "", nil
	}
	return e.id, nil
}

func (f *fakeResolver) ContactName(jid types.JID) string {
	if f == nil || f.names == nil {
		return ""
	}
	return f.names[jid.ToNonAD().String()]
}

func (f *fakeResolver) IsContactSaved(jid types.JID) bool {
	if f == nil {
		return false
	}
	return f.savedJIDs[jid.ToNonAD().String()]
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

func TestFormatLocationContent(t *testing.T) {
	lat := 41.385064
	lng := 2.173404
	t.Run("plain lat/lng → header + maps link", func(t *testing.T) {
		loc := &waE2E.LocationMessage{
			DegreesLatitude:  &lat,
			DegreesLongitude: &lng,
		}
		got := formatLocationContent(loc)
		want := "📍 Ubicación compartida\nhttps://maps.google.com/?q=41.385064,2.173404"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("with name and address → header + bold name + address + link", func(t *testing.T) {
		name := "La Sagrada Familia"
		addr := "Carrer de Mallorca 401, Barcelona"
		loc := &waE2E.LocationMessage{
			DegreesLatitude:  &lat,
			DegreesLongitude: &lng,
			Name:             &name,
			Address:          &addr,
		}
		got := formatLocationContent(loc)
		want := "📍 Ubicación compartida\n**La Sagrada Familia**\nCarrer de Mallorca 401, Barcelona\nhttps://maps.google.com/?q=41.385064,2.173404"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("live location → header dice en vivo", func(t *testing.T) {
		isLive := true
		loc := &waE2E.LocationMessage{
			DegreesLatitude:  &lat,
			DegreesLongitude: &lng,
			IsLive:           &isLive,
		}
		got := formatLocationContent(loc)
		want := "📍 Ubicación en vivo\nhttps://maps.google.com/?q=41.385064,2.173404"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("with comment → italic al final", func(t *testing.T) {
		cmt := "nos vemos aquí"
		loc := &waE2E.LocationMessage{
			DegreesLatitude:  &lat,
			DegreesLongitude: &lng,
			Comment:          &cmt,
		}
		got := formatLocationContent(loc)
		want := "📍 Ubicación compartida\nhttps://maps.google.com/?q=41.385064,2.173404\n_nos vemos aquí_"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("zero lat/lng → empty (invalid location)", func(t *testing.T) {
		zero := 0.0
		loc := &waE2E.LocationMessage{
			DegreesLatitude:  &zero,
			DegreesLongitude: &zero,
		}
		if got := formatLocationContent(loc); got != "" {
			t.Errorf("zero coords: got %q, want empty", got)
		}
	})
}

func TestSanitizeMime(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"audio/ogg", "audio/ogg"},
		{"audio/ogg; codecs=opus", "audio/ogg"},
		{"image/webp; animated=true", "image/webp"},
		{"video/mp4;codecs=avc1", "video/mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := sanitizeMime(tc.in); got != tc.want {
				t.Errorf("sanitizeMime(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatPollContent(t *testing.T) {
	mkOpt := func(name string) *waE2E.PollCreationMessage_Option {
		s := name
		return &waE2E.PollCreationMessage_Option{OptionName: &s}
	}
	mkPoll := func(name string, max uint32, opts ...string) *waE2E.PollCreationMessage {
		options := make([]*waE2E.PollCreationMessage_Option, 0, len(opts))
		for _, o := range opts {
			options = append(options, mkOpt(o))
		}
		n := name
		m := max
		return &waE2E.PollCreationMessage{
			Name:                   &n,
			Options:                options,
			SelectableOptionsCount: &m,
		}
	}

	t.Run("single-select poll", func(t *testing.T) {
		poll := mkPoll("¿Día para el meeting?", 1, "Lunes", "Martes", "Miércoles")
		got := formatPollContent(poll)
		want := "🗳️ **Encuesta:** ¿Día para el meeting?\n1. Lunes\n2. Martes\n3. Miércoles\n_(elige 1 opción)_"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("multi-select poll", func(t *testing.T) {
		poll := mkPoll("¿Qué pizzas? (top 2)", 2, "Margherita", "Diavola", "Marinara")
		got := formatPollContent(poll)
		want := "🗳️ **Encuesta:** ¿Qué pizzas? (top 2)\n1. Margherita\n2. Diavola\n3. Marinara\n_(elige hasta 2 opciones)_"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("unlimited (max=0) omits hint", func(t *testing.T) {
		poll := mkPoll("Open vote", 0, "A", "B")
		got := formatPollContent(poll)
		want := "🗳️ **Encuesta:** Open vote\n1. A\n2. B"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("poll without name → empty (no contexto)", func(t *testing.T) {
		empty := ""
		poll := &waE2E.PollCreationMessage{
			Name:    &empty,
			Options: []*waE2E.PollCreationMessage_Option{mkOpt("A")},
		}
		if got := formatPollContent(poll); got != "" {
			t.Errorf("nameless poll: got %q, want empty", got)
		}
	})

	t.Run("poll without options → empty", func(t *testing.T) {
		name := "Question"
		poll := &waE2E.PollCreationMessage{Name: &name}
		if got := formatPollContent(poll); got != "" {
			t.Errorf("optionless poll: got %q, want empty", got)
		}
	})
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

	t.Run("PN sender (Spain) with push name → bold name + italic phone", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "Jean Paul")
		got := applyGroupSenderPrefix("hola", msg, nil)
		want := "`+34640047775 · ~Jean Paul`  \nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("PN sender empty push name, resolver knows contact name", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "")
		r := &fakeResolver{names: map[string]string{senderPN.String(): "Jean Paul (CRM)"}}
		got := applyGroupSenderPrefix("hola", msg, r)
		want := "`+34640047775 · ~Jean Paul (CRM)`  \nhola"
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
		want := "`+34640047775 · ~Anon`  \nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("LID sender unresolvable, only push name → bold name + colon", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderLID, "Pseudo")
		got := applyGroupSenderPrefix("hola", msg, nil)
		want := "`~Pseudo:`  \nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("PN sender no name → italic phone + colon", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "")
		got := applyGroupSenderPrefix("hola", msg, nil)
		want := "`+34640047775:`  \nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty body keeps prefix without trailing newline", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderPN, "Jean Paul")
		got := applyGroupSenderPrefix("", msg, nil)
		want := "`+34640047775 · ~Jean Paul`"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("saved contact → name without tilde (v0.39.5)", func(t *testing.T) {
		// Desde v0.39.5: contactos guardados en agenda van sin ~ delante
		// del nombre. Teléfono se sigue mostrando siempre (v0.39.4).
		msg := mkGroupMsg(groupJID, senderPN, "Jean Paul")
		r := &fakeResolver{
			names:     map[string]string{senderPN.String(): "Jean Paul"},
			savedJIDs: map[string]bool{senderPN.String(): true},
		}
		got := applyGroupSenderPrefix("hola", msg, r)
		want := "`+34640047775 · Jean Paul`  \nhola"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("saved LID resolved to saved PN → name without tilde", func(t *testing.T) {
		msg := mkGroupMsg(groupJID, senderLID, "Anon")
		r := &fakeResolver{
			pnByLID:   map[string]types.JID{senderLID.String(): senderPN},
			names:     map[string]string{senderPN.String(): "Jean Paul"},
			savedJIDs: map[string]bool{senderPN.String(): true},
		}
		got := applyGroupSenderPrefix("hola", msg, r)
		want := "`+34640047775 · Jean Paul`  \nhola"
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
		{"Spain", "34604021705", "+34604021705"},
		{"France", "33612345678", "+33612345678"},
		{"US", "14155551234", "+14155551234"},
		{"single digit", "1", "+1"},
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
