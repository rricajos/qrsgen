package banwatch

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"
)

type capturingEmitter struct {
	mu     sync.Mutex
	events []struct {
		instance, event string
		extras          map[string]any
	}
}

func (c *capturingEmitter) EmitLifecycle(instance, event string, extras map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, struct {
		instance, event string
		extras          map[string]any
	}{instance, event, extras})
}

func (c *capturingEmitter) drain() []struct {
	instance, event string
	extras          map[string]any
} {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]struct {
		instance, event string
		extras          map[string]any
	}{}, c.events...)
	c.events = nil
	return out
}

func newWatcher(t *testing.T, cfg Config) (*Watcher, *capturingEmitter) {
	t.Helper()
	em := &capturingEmitter{}
	w := New(cfg, em, slog.New(slog.DiscardHandler))
	return w, em
}

func TestSnapshot_EmptyBucket(t *testing.T) {
	w, _ := newWatcher(t, DefaultConfig())
	s := w.Snapshot("X")
	if s.Level != "ok" || s.Score != 0 || len(s.Alerts) != 0 {
		t.Errorf("empty bucket should be ok/0/[], got %+v", s)
	}
}

func TestRecord_VelocityAlert(t *testing.T) {
	cfg := DefaultConfig()
	cfg.VelocityWindow = time.Minute
	cfg.VelocityThreshold = 5
	w, _ := newWatcher(t, cfg)
	for i := 0; i < 10; i++ {
		w.Record("X", "j"+strconv.Itoa(i), true)
	}
	s := w.Snapshot("X")
	if s.VelocityMsgsPerWindow != 10 {
		t.Errorf("velocity = %d, want 10", s.VelocityMsgsPerWindow)
	}
	if !hasAlert(s, "high_velocity") {
		t.Errorf("expected high_velocity alert, got %v", s.Alerts)
	}
}

func TestRecord_DiversityAlert(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DiversityWindow = time.Minute
	cfg.DiversityThreshold = 3
	w, _ := newWatcher(t, cfg)
	for i := 0; i < 10; i++ {
		w.Record("X", "jid-"+strconv.Itoa(i), true)
	}
	s := w.Snapshot("X")
	if s.DiversityUniqueJIDs != 10 {
		t.Errorf("diversity = %d, want 10", s.DiversityUniqueJIDs)
	}
	if !hasAlert(s, "burst_outreach") {
		t.Errorf("expected burst_outreach, got %v", s.Alerts)
	}
}

func TestRecord_LowDeliveryAlert(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DeliveryWindow = time.Minute
	cfg.DeliveryThreshold = 0.7
	cfg.DeliveryMinSamples = 5
	w, _ := newWatcher(t, cfg)
	for i := 0; i < 10; i++ {
		w.Record("X", "jid", i%2 == 0) // 50% success
	}
	s := w.Snapshot("X")
	if s.DeliveryRatio != 0.5 {
		t.Errorf("delivery = %.3f, want 0.5", s.DeliveryRatio)
	}
	if !hasAlert(s, "low_delivery") {
		t.Errorf("expected low_delivery, got %v", s.Alerts)
	}
}

func TestRecord_NoAlertBelowSampleFloor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DeliveryWindow = time.Minute
	cfg.DeliveryMinSamples = 20
	w, _ := newWatcher(t, cfg)
	// 1 failure out of 1 — bad ratio but tiny sample → must not flag.
	w.Record("X", "jid", false)
	s := w.Snapshot("X")
	if hasAlert(s, "low_delivery") {
		t.Errorf("should not flag with samples below floor, got %v", s.Alerts)
	}
}

func TestCompact_DropsOldEvents(t *testing.T) {
	cfg := Config{
		VelocityWindow:    50 * time.Millisecond,
		VelocityThreshold: 100,
		DiversityWindow:   50 * time.Millisecond,
		DeliveryWindow:    50 * time.Millisecond,
		DeliveryThreshold: 0.7,
	}
	w, _ := newWatcher(t, cfg)
	w.Record("X", "j", true)
	w.Record("X", "j", true)
	time.Sleep(70 * time.Millisecond)
	w.Record("X", "j", true) // triggers compaction
	w.mu.Lock()
	got := len(w.buckets["X"].events)
	w.mu.Unlock()
	if got != 1 {
		t.Errorf("expected only the last event after compaction, got %d", got)
	}
}

func TestScoreLevels(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		name  string
		snap  Snapshot
		want  string
		minOK float64 // min expected score
	}{
		{"empty", Snapshot{}, "ok", 0},
		{"velocity x2 threshold", Snapshot{VelocityMsgsPerWindow: 60}, "moderate", 0.3},
		{"velocity + diversity + low delivery", Snapshot{
			VelocityMsgsPerWindow: 60,
			DiversityUniqueJIDs:   40,
			DeliverySamples:       20,
			DeliveryRatio:         0.1,
		}, "high", 0.7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.snap.DeliveryMinSamples = cfg.DeliveryMinSamples
			got, level := scoreOf(tc.snap, cfg)
			if level != tc.want {
				t.Errorf("level = %q (score %.3f), want %q", level, got, tc.want)
			}
			if got < tc.minOK {
				t.Errorf("score = %.3f, want >= %.3f", got, tc.minOK)
			}
		})
	}
}

func TestEvaluate_EmitsAlertOnceUntilCleared(t *testing.T) {
	cfg := DefaultConfig()
	cfg.VelocityWindow = time.Minute
	cfg.VelocityThreshold = 3
	w, em := newWatcher(t, cfg)
	for i := 0; i < 5; i++ {
		w.Record("X", "j"+strconv.Itoa(i), true)
	}
	w.evaluate()
	w.evaluate() // second tick — same alert, must NOT re-emit
	if got := len(em.drain()); got != 1 {
		t.Errorf("expected exactly 1 emit while alert active, got %d", got)
	}

	// Clear by emptying the bucket via compaction (force window to almost-zero).
	w.mu.Lock()
	w.buckets["X"].events = nil
	w.mu.Unlock()
	w.evaluate()
	w.evaluate()

	// Now re-trigger.
	for i := 0; i < 5; i++ {
		w.Record("X", "j"+strconv.Itoa(i), true)
	}
	w.evaluate()
	if got := len(em.drain()); got != 1 {
		t.Errorf("expected 1 emit on re-trigger, got %d", got)
	}
}

func TestStart_ExitsWithContext(t *testing.T) {
	w, _ := newWatcher(t, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx, 10*time.Millisecond)
	cancel()
	// Sleep beyond one interval to let the goroutine notice the ctx.
	time.Sleep(30 * time.Millisecond)
}

func TestRecord_EmptyInstance_NoCrash(t *testing.T) {
	w, _ := newWatcher(t, DefaultConfig())
	w.Record("", "jid", true)
	if len(w.buckets) != 0 {
		t.Errorf("empty instance should not allocate bucket")
	}
}

func hasAlert(s Snapshot, name string) bool {
	for _, a := range s.Alerts {
		if a == name {
			return true
		}
	}
	return false
}
