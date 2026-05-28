package bridge

import (
	"sync"
	"testing"
	"time"
)

func TestAvatarTracker_FirstCheckReturnsTrue(t *testing.T) {
	tr := newAvatarTracker(24 * time.Hour)
	if !tr.ShouldCheck("inst1", "u@s.whatsapp.net") {
		t.Errorf("primer chequeo: debe devolver true")
	}
}

func TestAvatarTracker_RepeatedCheckWithinTTLReturnsFalse(t *testing.T) {
	tr := newAvatarTracker(24 * time.Hour)
	tr.ShouldCheck("inst1", "u@s.whatsapp.net")
	if tr.ShouldCheck("inst1", "u@s.whatsapp.net") {
		t.Errorf("segundo chequeo dentro de TTL: debe devolver false")
	}
}

func TestAvatarTracker_CheckAfterTTLReturnsTrue(t *testing.T) {
	tr := newAvatarTracker(1 * time.Millisecond)
	tr.ShouldCheck("inst1", "u@s.whatsapp.net")
	time.Sleep(5 * time.Millisecond)
	if !tr.ShouldCheck("inst1", "u@s.whatsapp.net") {
		t.Errorf("chequeo tras TTL: debe devolver true")
	}
}

func TestAvatarTracker_UpdateIDPersists(t *testing.T) {
	tr := newAvatarTracker(24 * time.Hour)
	tr.ShouldCheck("inst1", "u@s.whatsapp.net")
	tr.UpdateID("inst1", "u@s.whatsapp.net", "v123")
	if got := tr.LastID("inst1", "u@s.whatsapp.net"); got != "v123" {
		t.Errorf("LastID = %q, want v123", got)
	}
}

func TestAvatarTracker_PerJIDIsolation(t *testing.T) {
	tr := newAvatarTracker(24 * time.Hour)
	tr.ShouldCheck("inst1", "u1@s.whatsapp.net")
	if !tr.ShouldCheck("inst1", "u2@s.whatsapp.net") {
		t.Errorf("otro JID: debe devolver true (estado per-JID)")
	}
}

func TestAvatarTracker_PerInstanceIsolation(t *testing.T) {
	tr := newAvatarTracker(24 * time.Hour)
	tr.ShouldCheck("inst1", "u@s.whatsapp.net")
	if !tr.ShouldCheck("inst2", "u@s.whatsapp.net") {
		t.Errorf("misma JID, otra instancia: debe devolver true")
	}
}

func TestAvatarTracker_ConcurrentShouldCheck_OnlyOneTrue(t *testing.T) {
	// Simula burst de N goroutines chequeando el mismo JID a la vez.
	// Solo UNA debe ver true; el resto false (previene spawn duplicado).
	tr := newAvatarTracker(24 * time.Hour)
	const N = 20
	var wg sync.WaitGroup
	var trueCount int
	var mu sync.Mutex
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tr.ShouldCheck("inst1", "u@s.whatsapp.net") {
				mu.Lock()
				trueCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if trueCount != 1 {
		t.Errorf("burst de %d goroutines: %d vieron true, esperaba 1", N, trueCount)
	}
}
