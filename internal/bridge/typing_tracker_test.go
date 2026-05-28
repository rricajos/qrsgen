package bridge

import (
	"testing"
	"time"
)

func TestTypingTracker_FirstEmitsTrue(t *testing.T) {
	tr := newTypingTracker(5 * time.Second)
	if !tr.ShouldEmit(123, true) {
		t.Errorf("primer typing event: debe emitir true")
	}
}

func TestTypingTracker_SameStateWithinIntervalReturnsFalse(t *testing.T) {
	tr := newTypingTracker(5 * time.Second)
	tr.ShouldEmit(123, true)
	if tr.ShouldEmit(123, true) {
		t.Errorf("mismo state dentro de minInterval: NO debe emitir")
	}
}

func TestTypingTracker_StateChangeEmits(t *testing.T) {
	tr := newTypingTracker(5 * time.Second)
	tr.ShouldEmit(123, true)
	if !tr.ShouldEmit(123, false) {
		t.Errorf("cambio composing→paused: debe emitir")
	}
	if !tr.ShouldEmit(123, true) {
		t.Errorf("cambio paused→composing: debe emitir")
	}
}

func TestTypingTracker_SameStateAfterIntervalEmits(t *testing.T) {
	tr := newTypingTracker(1 * time.Millisecond)
	tr.ShouldEmit(123, true)
	time.Sleep(5 * time.Millisecond)
	if !tr.ShouldEmit(123, true) {
		t.Errorf("mismo state tras minInterval: debe emitir (refresh)")
	}
}

func TestTypingTracker_PerConvIsolation(t *testing.T) {
	tr := newTypingTracker(5 * time.Second)
	tr.ShouldEmit(123, true)
	if !tr.ShouldEmit(456, true) {
		t.Errorf("otra conv: debe emitir (estado por-conv)")
	}
}
