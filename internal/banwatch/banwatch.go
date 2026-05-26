// Package banwatch implements lightweight ban-prevention heuristics on top of
// the outgoing message stream. WhatsApp detects spammy clients via patterns
// like very high velocity, sudden burst of new contacts, or low delivery ratio
// (lots of message-send errors). This package gives the integrator visibility
// into those signals before WhatsApp acts on them.
//
// All state is in-memory and per-process. Restarts reset the windows; that's
// acceptable because thresholds are tuned for short timescales (1-10 minutes).
package banwatch

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Config holds the tunable thresholds.
type Config struct {
	// VelocityWindow is the rolling window for messages/min.
	VelocityWindow time.Duration
	// VelocityThreshold: max messages in VelocityWindow before flagging.
	VelocityThreshold int

	// DiversityWindow is the rolling window for unique JIDs contacted.
	DiversityWindow time.Duration
	// DiversityThreshold: max unique JIDs in DiversityWindow before flagging.
	DiversityThreshold int

	// DeliveryWindow is the rolling window for delivery ratio.
	DeliveryWindow time.Duration
	// DeliveryThreshold: minimum success ratio (0-1) below which we flag.
	// Only evaluated when at least DeliveryMinSamples have been seen.
	DeliveryThreshold float64
	// DeliveryMinSamples avoids flagging with tiny samples (e.g. 1 failure out of 1).
	DeliveryMinSamples int
}

// DefaultConfig returns sane defaults tuned for typical business-account
// behavior. Override per-deployment if needed.
func DefaultConfig() Config {
	return Config{
		VelocityWindow:     1 * time.Minute,
		VelocityThreshold:  30,
		DiversityWindow:    5 * time.Minute,
		DiversityThreshold: 20,
		DeliveryWindow:     10 * time.Minute,
		DeliveryThreshold:  0.7,
		DeliveryMinSamples: 10,
	}
}

// LifecycleEmitter is the minimal hook the watcher uses to surface alerts.
// In production this is satisfied by *manager.Manager.EmitCustomLifecycle.
type LifecycleEmitter interface {
	EmitLifecycle(name, event string, extras map[string]any)
}

type sendEvent struct {
	ts      time.Time
	jid     string
	success bool
}

type bucket struct {
	// events is the ring of recent send attempts ordered by ts ascending.
	// We compact lazily on Record/Snapshot to keep memory bounded.
	events []sendEvent
	// activeAlerts tracks which alerts were already fired so we don't spam.
	activeAlerts map[string]bool
}

// Watcher is the per-process ban-risk tracker. Safe for concurrent use.
type Watcher struct {
	cfg     Config
	mu      sync.Mutex
	buckets map[string]*bucket
	emitter LifecycleEmitter
	logger  *slog.Logger
}

// New builds a Watcher. emitter may be nil (alerts get logged but not emitted).
func New(cfg Config, emitter LifecycleEmitter, logger *slog.Logger) *Watcher {
	return &Watcher{
		cfg:     cfg,
		buckets: map[string]*bucket{},
		emitter: emitter,
		logger:  logger,
	}
}

func (w *Watcher) bucketLocked(instance string) *bucket {
	b, ok := w.buckets[instance]
	if !ok {
		b = &bucket{activeAlerts: map[string]bool{}}
		w.buckets[instance] = b
	}
	return b
}

// Record adds a send attempt to the rolling window.
func (w *Watcher) Record(instance, jid string, success bool) {
	if instance == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.bucketLocked(instance)
	b.events = append(b.events, sendEvent{ts: time.Now(), jid: jid, success: success})
	w.compactLocked(b)
}

// compactLocked drops events older than the largest configured window. Caller
// holds w.mu.
func (w *Watcher) compactLocked(b *bucket) {
	cutoff := time.Now().Add(-w.maxWindow())
	if len(b.events) == 0 || !b.events[0].ts.Before(cutoff) {
		return
	}
	// Find first event >= cutoff. Since events are append-ordered, a linear
	// scan is fine for the volumes we expect (≤ a few thousand per bucket).
	idx := 0
	for ; idx < len(b.events); idx++ {
		if !b.events[idx].ts.Before(cutoff) {
			break
		}
	}
	b.events = append(b.events[:0], b.events[idx:]...)
}

func (w *Watcher) maxWindow() time.Duration {
	m := w.cfg.VelocityWindow
	if w.cfg.DiversityWindow > m {
		m = w.cfg.DiversityWindow
	}
	if w.cfg.DeliveryWindow > m {
		m = w.cfg.DeliveryWindow
	}
	return m
}

// Snapshot is the public read shape consumed by the API and the evaluator.
type Snapshot struct {
	Instance string `json:"instance"`

	VelocityMsgsPerWindow int           `json:"velocity_msgs_per_window"`
	VelocityWindow        time.Duration `json:"velocity_window_ns"`
	VelocityThreshold     int           `json:"velocity_threshold"`

	DiversityUniqueJIDs int           `json:"diversity_unique_jids"`
	DiversityWindow     time.Duration `json:"diversity_window_ns"`
	DiversityThreshold  int           `json:"diversity_threshold"`

	DeliveryRatio      float64       `json:"delivery_ratio"`
	DeliverySamples    int           `json:"delivery_samples"`
	DeliveryWindow     time.Duration `json:"delivery_window_ns"`
	DeliveryThreshold  float64       `json:"delivery_threshold"`
	DeliveryMinSamples int           `json:"delivery_min_samples"`

	Alerts []string `json:"alerts"` // subset of {"high_velocity", "burst_outreach", "low_delivery"}
	Score  float64  `json:"score"`  // 0.0–1.0
	Level  string   `json:"level"`  // ok | low | moderate | high
}

// Snapshot computes the current view for instance. Empty Snapshot if no data.
func (w *Watcher) Snapshot(instance string) Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.snapshotLocked(instance)
}

func (w *Watcher) snapshotLocked(instance string) Snapshot {
	b, ok := w.buckets[instance]
	snap := Snapshot{
		Instance:           instance,
		VelocityWindow:     w.cfg.VelocityWindow,
		VelocityThreshold:  w.cfg.VelocityThreshold,
		DiversityWindow:    w.cfg.DiversityWindow,
		DiversityThreshold: w.cfg.DiversityThreshold,
		DeliveryWindow:     w.cfg.DeliveryWindow,
		DeliveryThreshold:  w.cfg.DeliveryThreshold,
		DeliveryMinSamples: w.cfg.DeliveryMinSamples,
		Alerts:             []string{},
		Level:              "ok",
	}
	if !ok || len(b.events) == 0 {
		return snap
	}

	now := time.Now()
	velocityCutoff := now.Add(-w.cfg.VelocityWindow)
	diversityCutoff := now.Add(-w.cfg.DiversityWindow)
	deliveryCutoff := now.Add(-w.cfg.DeliveryWindow)

	uniqueJIDs := map[string]struct{}{}
	var velocityCount, deliverySent, deliveryOK int

	for _, ev := range b.events {
		if !ev.ts.Before(velocityCutoff) {
			velocityCount++
		}
		if !ev.ts.Before(diversityCutoff) && ev.jid != "" {
			uniqueJIDs[ev.jid] = struct{}{}
		}
		if !ev.ts.Before(deliveryCutoff) {
			deliverySent++
			if ev.success {
				deliveryOK++
			}
		}
	}

	snap.VelocityMsgsPerWindow = velocityCount
	snap.DiversityUniqueJIDs = len(uniqueJIDs)
	snap.DeliverySamples = deliverySent
	if deliverySent > 0 {
		snap.DeliveryRatio = float64(deliveryOK) / float64(deliverySent)
	}

	// Alerts.
	if w.cfg.VelocityThreshold > 0 && velocityCount > w.cfg.VelocityThreshold {
		snap.Alerts = append(snap.Alerts, "high_velocity")
	}
	if w.cfg.DiversityThreshold > 0 && len(uniqueJIDs) > w.cfg.DiversityThreshold {
		snap.Alerts = append(snap.Alerts, "burst_outreach")
	}
	if deliverySent >= w.cfg.DeliveryMinSamples && snap.DeliveryRatio < w.cfg.DeliveryThreshold {
		snap.Alerts = append(snap.Alerts, "low_delivery")
	}

	// Compose risk score and level.
	snap.Score, snap.Level = scoreOf(snap, w.cfg)
	return snap
}

func scoreOf(s Snapshot, cfg Config) (float64, string) {
	var score float64

	if cfg.VelocityThreshold > 0 {
		v := float64(s.VelocityMsgsPerWindow) / float64(cfg.VelocityThreshold)
		score += clamp(v, 0, 2) * 0.4
	}
	if cfg.DiversityThreshold > 0 {
		d := float64(s.DiversityUniqueJIDs) / float64(cfg.DiversityThreshold)
		score += clamp(d, 0, 2) * 0.3
	}
	if s.DeliverySamples >= cfg.DeliveryMinSamples && cfg.DeliveryThreshold > 0 {
		// 1.0 score when ratio is 0, 0.0 score when ratio >= threshold.
		gap := cfg.DeliveryThreshold - s.DeliveryRatio
		if gap > 0 {
			score += (gap / cfg.DeliveryThreshold) * 0.3
		}
	}
	// Normalize to 0-1 (score above is up to 2*0.4 + 2*0.3 + 1*0.3 = 1.7).
	score /= 1.7
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	switch {
	case score >= 0.7:
		return score, "high"
	case score >= 0.4:
		return score, "moderate"
	case score > 0.1:
		return score, "low"
	default:
		return score, "ok"
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Start launches a background goroutine that evaluates each bucket every
// interval and emits a lifecycle event for newly-active alerts. Returns
// immediately; goroutine exits with ctx.
func (w *Watcher) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go w.loop(ctx, interval)
}

func (w *Watcher) loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.evaluate()
		}
	}
}

// evaluate scans every bucket, emits an alert event the first time it shows
// up, and clears the active flag when the alert recovers (so the next time it
// triggers we emit again).
func (w *Watcher) evaluate() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for instance, b := range w.buckets {
		w.compactLocked(b)
		snap := w.snapshotLocked(instance)
		current := map[string]bool{}
		for _, a := range snap.Alerts {
			current[a] = true
		}
		for alert := range current {
			if !b.activeAlerts[alert] {
				b.activeAlerts[alert] = true
				w.emit(instance, alert, snap)
			}
		}
		for alert := range b.activeAlerts {
			if !current[alert] {
				delete(b.activeAlerts, alert)
				w.logger.Info("banwatch alert cleared", "instance", instance, "alert", alert)
			}
		}
	}
}

func (w *Watcher) emit(instance, alert string, snap Snapshot) {
	w.logger.Warn("banwatch alert raised",
		"instance", instance,
		"alert", alert,
		"score", snap.Score,
		"level", snap.Level,
		"velocity", snap.VelocityMsgsPerWindow,
		"diversity", snap.DiversityUniqueJIDs,
		"delivery_ratio", snap.DeliveryRatio,
	)
	if w.emitter == nil {
		return
	}
	w.emitter.EmitLifecycle(instance, "ban_risk", map[string]any{
		"alert":          alert,
		"score":          snap.Score,
		"level":          snap.Level,
		"velocity":       snap.VelocityMsgsPerWindow,
		"diversity":      snap.DiversityUniqueJIDs,
		"delivery_ratio": snap.DeliveryRatio,
	})
}
