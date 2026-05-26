// Package usage tracks per-instance daily usage counters (messages in/out,
// spamguard blocks, lifecycle events) and persists them to Postgres.
//
// In-memory increments are cheap (map + mutex). A background goroutine flushes
// counters every minute via UPSERT keyed by (instance, day in UTC). On restart
// the in-memory map is reset, but persisted aggregates survive in
// bridge_usage_daily.
package usage

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Counter is the per-instance, per-day bucket of counts.
type Counter struct {
	MessagesIn      int64 `json:"messages_in"`
	MessagesOut     int64 `json:"messages_out"`
	SpamguardBlocks int64 `json:"spamguard_blocks"`
	LifecycleEvents int64 `json:"lifecycle_events"`
}

// DailyRow is what the API returns: identification + counter + dates.
type DailyRow struct {
	Instance string `json:"instance"`
	Day      string `json:"day"` // YYYY-MM-DD (UTC)
	Counter
}

// Tracker accumulates counters in memory and periodically flushes them to DB.
//
// Safe for concurrent use.
type Tracker struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	mu sync.Mutex
	// buckets is the pending-to-flush delta per (instance, day).
	// After a successful flush, entries are cleared.
	buckets map[bucketKey]*Counter
}

type bucketKey struct {
	instance string
	day      string // YYYY-MM-DD UTC, derived from time.Now().UTC()
}

func dayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// EnsureSchema creates the bridge_usage_daily table.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS bridge_usage_daily (
			instance         TEXT NOT NULL,
			day              DATE NOT NULL,
			messages_in      BIGINT NOT NULL DEFAULT 0,
			messages_out     BIGINT NOT NULL DEFAULT 0,
			spamguard_blocks BIGINT NOT NULL DEFAULT 0,
			lifecycle_events BIGINT NOT NULL DEFAULT 0,
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (instance, day)
		);
		CREATE INDEX IF NOT EXISTS bridge_usage_daily_day_idx ON bridge_usage_daily (day);
	`)
	if err != nil {
		return fmt.Errorf("usage schema: %w", err)
	}
	return nil
}

// New returns a Tracker. Call Start to launch the flush goroutine.
func New(pool *pgxpool.Pool, logger *slog.Logger) *Tracker {
	return &Tracker{
		pool:    pool,
		logger:  logger,
		buckets: make(map[bucketKey]*Counter),
	}
}

func (t *Tracker) bucketLocked(instance string) *Counter {
	k := bucketKey{instance: instance, day: dayKey(time.Now())}
	c, ok := t.buckets[k]
	if !ok {
		c = &Counter{}
		t.buckets[k] = c
	}
	return c
}

// IncIn increments messages_in for the instance.
func (t *Tracker) IncIn(instance string) {
	if instance == "" {
		return
	}
	t.mu.Lock()
	t.bucketLocked(instance).MessagesIn++
	t.mu.Unlock()
}

// IncOut increments messages_out for the instance.
func (t *Tracker) IncOut(instance string) {
	if instance == "" {
		return
	}
	t.mu.Lock()
	t.bucketLocked(instance).MessagesOut++
	t.mu.Unlock()
}

// IncSpamguardBlock increments spamguard_blocks for the instance.
func (t *Tracker) IncSpamguardBlock(instance string) {
	if instance == "" {
		return
	}
	t.mu.Lock()
	t.bucketLocked(instance).SpamguardBlocks++
	t.mu.Unlock()
}

// IncLifecycle increments lifecycle_events for the instance.
func (t *Tracker) IncLifecycle(instance string) {
	if instance == "" {
		return
	}
	t.mu.Lock()
	t.bucketLocked(instance).LifecycleEvents++
	t.mu.Unlock()
}

// Start launches a background goroutine that flushes pending counters to DB
// every interval. Returns immediately. The goroutine exits when ctx is done,
// flushing one last time.
func (t *Tracker) Start(ctx context.Context, interval time.Duration) {
	go t.loop(ctx, interval)
}

func (t *Tracker) loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := t.Flush(context.Background()); err != nil {
				t.logger.Error("usage flush on shutdown", "err", err)
			}
			return
		case <-ticker.C:
			if err := t.Flush(ctx); err != nil {
				t.logger.Error("usage flush", "err", err)
			}
		}
	}
}

// Flush UPSERTs all pending counters and resets the in-memory state.
// Best-effort: if it fails, current deltas are preserved for the next attempt.
func (t *Tracker) Flush(ctx context.Context) error {
	t.mu.Lock()
	pending := t.buckets
	t.buckets = make(map[bucketKey]*Counter)
	t.mu.Unlock()

	if len(pending) == 0 {
		return nil
	}

	const stmt = `
		INSERT INTO bridge_usage_daily (instance, day, messages_in, messages_out, spamguard_blocks, lifecycle_events, updated_at)
		VALUES ($1, $2::date, $3, $4, $5, $6, NOW())
		ON CONFLICT (instance, day) DO UPDATE SET
			messages_in      = bridge_usage_daily.messages_in      + EXCLUDED.messages_in,
			messages_out     = bridge_usage_daily.messages_out     + EXCLUDED.messages_out,
			spamguard_blocks = bridge_usage_daily.spamguard_blocks + EXCLUDED.spamguard_blocks,
			lifecycle_events = bridge_usage_daily.lifecycle_events + EXCLUDED.lifecycle_events,
			updated_at       = NOW()
	`

	var failed error
	for k, c := range pending {
		if _, err := t.pool.Exec(ctx, stmt,
			k.instance, k.day,
			c.MessagesIn, c.MessagesOut, c.SpamguardBlocks, c.LifecycleEvents,
		); err != nil {
			// Re-buffer this bucket so we retry next tick.
			t.mu.Lock()
			rb, ok := t.buckets[k]
			if !ok {
				rb = &Counter{}
				t.buckets[k] = rb
			}
			rb.MessagesIn += c.MessagesIn
			rb.MessagesOut += c.MessagesOut
			rb.SpamguardBlocks += c.SpamguardBlocks
			rb.LifecycleEvents += c.LifecycleEvents
			t.mu.Unlock()
			if failed == nil {
				failed = fmt.Errorf("upsert %s/%s: %w", k.instance, k.day, err)
			}
		}
	}
	return failed
}

// Query returns rows for a single instance between from and to (inclusive).
// from/to are YYYY-MM-DD UTC strings.
func (t *Tracker) Query(ctx context.Context, instance, from, to string) ([]DailyRow, error) {
	return t.query(ctx, &instance, from, to)
}

// QueryAll returns rows for all instances between from and to (inclusive).
func (t *Tracker) QueryAll(ctx context.Context, from, to string) ([]DailyRow, error) {
	return t.query(ctx, nil, from, to)
}

// MonthlySummaryRow aggregates usage across instances grouping by owner_tag
// and calendar month (UTC). The integrator typically maps owner_tag to its
// tenant identifier and uses this view for monthly billing.
type MonthlySummaryRow struct {
	OwnerTag         string `json:"owner_tag"` // "" for instances without a tag set
	Month            string `json:"month"`     // YYYY-MM
	MessagesIn       int64  `json:"messages_in"`
	MessagesOut      int64  `json:"messages_out"`
	SpamguardBlocks  int64  `json:"spamguard_blocks"`
	LifecycleEvents  int64  `json:"lifecycle_events"`
	ActiveInstances  int    `json:"active_instances"`
}

// MonthlySummary returns one row per (owner_tag, month) covering days
// between fromMonth and toMonth (YYYY-MM, inclusive). Days outside the range
// are not aggregated; partial months on the boundary are included.
func (t *Tracker) MonthlySummary(ctx context.Context, fromMonth, toMonth string) ([]MonthlySummaryRow, error) {
	rows, err := t.pool.Query(ctx, `
		SELECT
			COALESCE(NULLIF(i.owner_tag, ''), '') AS owner_tag,
			to_char(u.day, 'YYYY-MM')             AS month,
			SUM(u.messages_in)::bigint            AS messages_in,
			SUM(u.messages_out)::bigint           AS messages_out,
			SUM(u.spamguard_blocks)::bigint       AS spamguard_blocks,
			SUM(u.lifecycle_events)::bigint       AS lifecycle_events,
			COUNT(DISTINCT u.instance)::int       AS active_instances
		FROM bridge_usage_daily u
		LEFT JOIN bridge_instance i ON i.name = u.instance
		WHERE u.day >= ($1 || '-01')::date
		  AND u.day < (($2 || '-01')::date + INTERVAL '1 month')
		GROUP BY 1, 2
		ORDER BY 2 DESC, 1 ASC
	`, fromMonth, toMonth)
	if err != nil {
		return nil, fmt.Errorf("monthly summary: %w", err)
	}
	defer rows.Close()

	out := make([]MonthlySummaryRow, 0)
	for rows.Next() {
		var r MonthlySummaryRow
		if err := rows.Scan(&r.OwnerTag, &r.Month, &r.MessagesIn, &r.MessagesOut, &r.SpamguardBlocks, &r.LifecycleEvents, &r.ActiveInstances); err != nil {
			return nil, fmt.Errorf("scan monthly: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *Tracker) query(ctx context.Context, instance *string, from, to string) ([]DailyRow, error) {
	var rows pgx.Rows
	var err error
	const base = `
		SELECT instance, to_char(day, 'YYYY-MM-DD'),
		       messages_in, messages_out, spamguard_blocks, lifecycle_events
		FROM bridge_usage_daily
		WHERE day BETWEEN $1::date AND $2::date
	`
	if instance != nil {
		rows, err = t.pool.Query(ctx, base+" AND instance=$3 ORDER BY day ASC", from, to, *instance)
	} else {
		rows, err = t.pool.Query(ctx, base+" ORDER BY instance ASC, day ASC", from, to)
	}
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer rows.Close()

	out := make([]DailyRow, 0)
	for rows.Next() {
		var r DailyRow
		if err := rows.Scan(&r.Instance, &r.Day, &r.MessagesIn, &r.MessagesOut, &r.SpamguardBlocks, &r.LifecycleEvents); err != nil {
			return nil, fmt.Errorf("scan usage: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
