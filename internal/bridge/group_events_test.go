package bridge

import (
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Tests del builder de activity msgs a partir de events.GroupInfo.

func TestBuildGroupInfoLines_NameChange(t *testing.T) {
	actor := types.NewJID("34600000099", types.DefaultUserServer)
	evt := &events.GroupInfo{
		JID:    types.NewJID("120363111@g.us", types.GroupServer),
		Sender: &actor,
		Name:   &types.GroupName{Name: "Nuevo Nombre"},
	}
	r := &fakeResolver{
		names:     map[string]string{actor.String(): "Pepito"},
		savedJIDs: map[string]bool{actor.String(): true},
	}
	lines := buildGroupInfoLines(evt, r)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "Pepito") || !strings.Contains(lines[0], "Nuevo Nombre") {
		t.Errorf("got %q", lines[0])
	}
	if strings.Contains(lines[0], "~") {
		t.Errorf("saved author shouldn't have tilde: %q", lines[0])
	}
}

func TestBuildGroupInfoLines_TopicSetAndUnset(t *testing.T) {
	actor := types.NewJID("34600000099", types.DefaultUserServer)
	r := &fakeResolver{names: map[string]string{actor.String(): "Pepito"}}

	evtSet := &events.GroupInfo{
		JID:    types.NewJID("120363111@g.us", types.GroupServer),
		Sender: &actor,
		Topic:  &types.GroupTopic{Topic: "Tema nuevo"},
	}
	lines := buildGroupInfoLines(evtSet, r)
	if len(lines) != 1 || !strings.Contains(lines[0], "Tema nuevo") {
		t.Errorf("topic set: got %v", lines)
	}

	evtUnset := &events.GroupInfo{
		JID:    types.NewJID("120363111@g.us", types.GroupServer),
		Sender: &actor,
		Topic:  &types.GroupTopic{Topic: ""},
	}
	lines = buildGroupInfoLines(evtUnset, r)
	if len(lines) != 1 || !strings.Contains(lines[0], "quitó la descripción") {
		t.Errorf("topic unset: got %v", lines)
	}
}

func TestBuildGroupInfoLines_JoinLeavePromoteDemote(t *testing.T) {
	actor := types.NewJID("34600000099", types.DefaultUserServer)
	joined := types.NewJID("34600000001", types.DefaultUserServer)
	left := types.NewJID("34600000002", types.DefaultUserServer)
	promoted := types.NewJID("34600000003", types.DefaultUserServer)
	demoted := types.NewJID("34600000004", types.DefaultUserServer)

	evt := &events.GroupInfo{
		JID:     types.NewJID("120363111@g.us", types.GroupServer),
		Sender:  &actor,
		Join:    []types.JID{joined},
		Leave:   []types.JID{left},
		Promote: []types.JID{promoted},
		Demote:  []types.JID{demoted},
	}
	r := &fakeResolver{
		names: map[string]string{
			actor.String():    "Pepito",
			joined.String():   "Ana",
			left.String():     "Bea",
			promoted.String(): "Carlos",
			demoted.String():  "Diana",
		},
	}
	lines := buildGroupInfoLines(evt, r)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
	want := []string{"añadió a", "salieron/fueron expulsados", "promovió a admin", "quitó admin"}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("line %d should contain %q, got %q", i, w, lines[i])
		}
	}
}

func TestBuildGroupInfoLines_LockedAnnounceEphemeral(t *testing.T) {
	actor := types.NewJID("34600000099", types.DefaultUserServer)
	r := &fakeResolver{names: map[string]string{actor.String(): "Pepito"}}

	evt := &events.GroupInfo{
		JID:       types.NewJID("120363111@g.us", types.GroupServer),
		Sender:    &actor,
		Locked:    &types.GroupLocked{IsLocked: true},
		Announce:  &types.GroupAnnounce{IsAnnounce: true},
		Ephemeral: &types.GroupEphemeral{IsEphemeral: true, DisappearingTimer: 86400},
	}
	lines := buildGroupInfoLines(evt, r)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "restringió") || !strings.Contains(lines[1], "modo anuncio") || !strings.Contains(lines[2], "mensajes temporales") {
		t.Errorf("got: %v", lines)
	}
}

func TestBuildGroupInfoLines_UnknownActor(t *testing.T) {
	// Sin Sender, debería usar "Alguien".
	evt := &events.GroupInfo{
		JID:  types.NewJID("120363111@g.us", types.GroupServer),
		Name: &types.GroupName{Name: "X"},
	}
	lines := buildGroupInfoLines(evt, nil)
	if len(lines) != 1 || !strings.Contains(lines[0], "Alguien") {
		t.Errorf("got %v", lines)
	}
}

func TestIdentityFromJID_SavedNoTilde(t *testing.T) {
	jid := types.NewJID("34600000099", types.DefaultUserServer)
	r := &fakeResolver{
		names:     map[string]string{jid.String(): "Pepito"},
		savedJIDs: map[string]bool{jid.String(): true},
	}
	if got := identityFromJID(jid, r); got != "Pepito" {
		t.Errorf("got %q", got)
	}
}

func TestIdentityFromJID_UnsavedWithTilde(t *testing.T) {
	jid := types.NewJID("34600000099", types.DefaultUserServer)
	r := &fakeResolver{names: map[string]string{jid.String(): "Pepito"}}
	if got := identityFromJID(jid, r); got != "~Pepito" {
		t.Errorf("got %q", got)
	}
}

func TestIdentityFromJID_FallbackPhone(t *testing.T) {
	jid := types.NewJID("34600000099", types.DefaultUserServer)
	if got := identityFromJID(jid, nil); got != "+34600000099" {
		t.Errorf("got %q", got)
	}
}

// Avoid unused import errors.
var _ = time.Now
