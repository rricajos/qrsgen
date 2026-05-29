package bridge

import (
	"testing"
	"time"
)

func TestWAIDTracker_RecordAndDrain(t *testing.T) {
	tr := newWAIDTracker(10)
	now := time.Now()

	tr.RecordIncoming("inst1", "user@s.whatsapp.net", "WAID1", "user@s.whatsapp.net", now.Add(-2*time.Minute))
	tr.RecordIncoming("inst1", "user@s.whatsapp.net", "WAID2", "user@s.whatsapp.net", now.Add(-1*time.Minute))
	tr.RecordIncoming("inst1", "user@s.whatsapp.net", "WAID3", "user@s.whatsapp.net", now.Add(30*time.Second))

	// Drain con cutoff entre WAID2 y WAID3
	drained, sender := tr.DrainBefore("inst1", "user@s.whatsapp.net", now)
	if len(drained) != 2 {
		t.Errorf("drained = %v, want 2 entries (WAID1+WAID2)", drained)
	}
	if sender != "user@s.whatsapp.net" {
		t.Errorf("sender = %q, want %q", sender, "user@s.whatsapp.net")
	}

	// El WAID3 sigue ahí (más nuevo que cutoff)
	drained2, _ := tr.DrainBefore("inst1", "user@s.whatsapp.net", now.Add(2*time.Minute))
	if len(drained2) != 1 || drained2[0] != "WAID3" {
		t.Errorf("post-drain: got %v, want [WAID3]", drained2)
	}
}

func TestWAIDTracker_CapEnforced(t *testing.T) {
	tr := newWAIDTracker(3)
	now := time.Now()

	// Insertar 5 entries, solo deben quedar los 3 últimos
	for i := 1; i <= 5; i++ {
		tr.RecordIncoming("inst1", "chat@g.us", "WAID"+string(rune('0'+i)), "sender@s.whatsapp.net", now.Add(time.Duration(i)*time.Minute))
	}
	drained, _ := tr.DrainBefore("inst1", "chat@g.us", now.Add(10*time.Minute))
	if len(drained) != 3 {
		t.Errorf("cap=3 should limit to 3 entries, got %d: %v", len(drained), drained)
	}
	// Los más viejos (WAID1, WAID2) deben haberse descartado
	for _, w := range drained {
		if w == "WAID1" || w == "WAID2" {
			t.Errorf("cap should evict oldest, but %q still present", w)
		}
	}
}

func TestWAIDTracker_PerConvIsolation(t *testing.T) {
	tr := newWAIDTracker(10)
	now := time.Now()

	tr.RecordIncoming("inst1", "chatA@s.whatsapp.net", "WAID-A1", "x", now)
	tr.RecordIncoming("inst1", "chatB@s.whatsapp.net", "WAID-B1", "y", now)

	drainedA, _ := tr.DrainBefore("inst1", "chatA@s.whatsapp.net", now.Add(time.Minute))
	drainedB, _ := tr.DrainBefore("inst1", "chatB@s.whatsapp.net", now.Add(time.Minute))

	if len(drainedA) != 1 || drainedA[0] != "WAID-A1" {
		t.Errorf("chatA drain = %v, want [WAID-A1]", drainedA)
	}
	if len(drainedB) != 1 || drainedB[0] != "WAID-B1" {
		t.Errorf("chatB drain = %v, want [WAID-B1]", drainedB)
	}
}

func TestWAIDTracker_EmptyConvReturnsNil(t *testing.T) {
	tr := newWAIDTracker(10)
	drained, sender := tr.DrainBefore("inst1", "no-data@s.whatsapp.net", time.Now())
	if drained != nil {
		t.Errorf("empty: got %v, want nil", drained)
	}
	if sender != "" {
		t.Errorf("empty: sender = %q, want empty", sender)
	}
}
