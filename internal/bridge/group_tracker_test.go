package bridge

import (
	"testing"
	"time"
)

func TestGroupSenderTracker_FirstMessageEmitsHeader(t *testing.T) {
	tr := newGroupSenderTracker(10 * time.Minute)
	if !tr.RecordAndCheck("inst1", "group@g.us", "user1") {
		t.Errorf("primer mensaje del grupo: debe emitir header (no había registro previo)")
	}
}

func TestGroupSenderTracker_SameSenderConsecutiveSuppressesHeader(t *testing.T) {
	tr := newGroupSenderTracker(10 * time.Minute)
	tr.RecordAndCheck("inst1", "group@g.us", "user1")
	if tr.RecordAndCheck("inst1", "group@g.us", "user1") {
		t.Errorf("2do mensaje mismo sender: NO debe emitir header")
	}
	if tr.RecordAndCheck("inst1", "group@g.us", "user1") {
		t.Errorf("3er mensaje mismo sender: NO debe emitir header")
	}
}

func TestGroupSenderTracker_DifferentSenderEmitsHeader(t *testing.T) {
	tr := newGroupSenderTracker(10 * time.Minute)
	tr.RecordAndCheck("inst1", "group@g.us", "user1")
	if !tr.RecordAndCheck("inst1", "group@g.us", "user2") {
		t.Errorf("sender distinto debe emitir header")
	}
	if !tr.RecordAndCheck("inst1", "group@g.us", "user1") {
		t.Errorf("vuelve a user1 tras user2: debe emitir header")
	}
}

func TestGroupSenderTracker_TTLExpiry(t *testing.T) {
	tr := newGroupSenderTracker(1 * time.Millisecond)
	tr.RecordAndCheck("inst1", "group@g.us", "user1")
	time.Sleep(5 * time.Millisecond)
	if !tr.RecordAndCheck("inst1", "group@g.us", "user1") {
		t.Errorf("mismo sender pero TTL expirado: debe emitir header")
	}
}

func TestGroupSenderTracker_PerGroupIsolation(t *testing.T) {
	tr := newGroupSenderTracker(10 * time.Minute)
	tr.RecordAndCheck("inst1", "group_a@g.us", "user1")
	if !tr.RecordAndCheck("inst1", "group_b@g.us", "user1") {
		t.Errorf("mismo user, otro grupo: debe emitir header (estado por-grupo)")
	}
}

func TestGroupSenderTracker_PerInstanceIsolation(t *testing.T) {
	tr := newGroupSenderTracker(10 * time.Minute)
	tr.RecordAndCheck("inst1", "group@g.us", "user1")
	if !tr.RecordAndCheck("inst2", "group@g.us", "user1") {
		t.Errorf("mismo user y grupo, otra instancia: debe emitir header (estado por-instancia)")
	}
}
