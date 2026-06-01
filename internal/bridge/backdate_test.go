package bridge

import (
	"testing"
	"time"
)

func TestBackdater_DefaultsWhenZeroOrNegative(t *testing.T) {
	// pool=nil aquí, no llamamos a Run/tick, solo constructor.
	b := NewBackdater(nil, nil, 0, 0, 0)
	if b.interval != 30*time.Second {
		t.Errorf("expected default interval 30s, got %v", b.interval)
	}
	if b.batchSize != 500 {
		t.Errorf("expected default batchSize 500, got %d", b.batchSize)
	}
	if b.toleranceSec != 5 {
		t.Errorf("expected default toleranceSec 5, got %d", b.toleranceSec)
	}
	if b.logger == nil {
		t.Error("expected non-nil logger via slog.Default fallback")
	}

	b = NewBackdater(nil, nil, -1*time.Second, -10, -1)
	if b.interval != 30*time.Second || b.batchSize != 500 || b.toleranceSec != 5 {
		t.Errorf("negative inputs should fall back to defaults, got interval=%v batch=%d tol=%d",
			b.interval, b.batchSize, b.toleranceSec)
	}
}

func TestBackdater_AcceptsCustomValues(t *testing.T) {
	b := NewBackdater(nil, nil, 10*time.Second, 100, 2)
	if b.interval != 10*time.Second {
		t.Errorf("interval: got %v", b.interval)
	}
	if b.batchSize != 100 {
		t.Errorf("batchSize: got %d", b.batchSize)
	}
	if b.toleranceSec != 2 {
		t.Errorf("toleranceSec: got %d", b.toleranceSec)
	}
}
