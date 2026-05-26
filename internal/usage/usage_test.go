package usage

import (
	"sync"
	"testing"
	"time"
)

func TestDayKeyFormat(t *testing.T) {
	got := dayKey(time.Now())
	if len(got) != 10 || got[4] != '-' || got[7] != '-' {
		t.Errorf("dayKey format = %q, want YYYY-MM-DD", got)
	}
}

func TestTrackerInc_EmptyInstance_NoCrash(t *testing.T) {
	tr := &Tracker{buckets: map[bucketKey]*Counter{}}
	tr.IncIn("")
	tr.IncOut("")
	tr.IncSpamguardBlock("")
	tr.IncLifecycle("")
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.buckets) != 0 {
		t.Errorf("empty instance should not allocate bucket, got %d", len(tr.buckets))
	}
}

func TestTrackerInc_Accumulates(t *testing.T) {
	tr := &Tracker{buckets: map[bucketKey]*Counter{}}
	for i := 0; i < 5; i++ {
		tr.IncIn("X")
	}
	for i := 0; i < 3; i++ {
		tr.IncOut("X")
	}
	tr.IncSpamguardBlock("X")
	tr.IncLifecycle("X")
	tr.IncLifecycle("X")
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(tr.buckets))
	}
	for _, c := range tr.buckets {
		if c.MessagesIn != 5 || c.MessagesOut != 3 || c.SpamguardBlocks != 1 || c.LifecycleEvents != 2 {
			t.Errorf("counter = %+v, want {in:5 out:3 sg:1 lc:2}", c)
		}
	}
}

func TestTrackerInc_Concurrent(t *testing.T) {
	tr := &Tracker{buckets: map[bucketKey]*Counter{}}
	const workers = 50
	const each = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				tr.IncIn("X")
			}
		}()
	}
	wg.Wait()

	tr.mu.Lock()
	defer tr.mu.Unlock()
	var total int64
	for _, c := range tr.buckets {
		total += c.MessagesIn
	}
	if total != int64(workers*each) {
		t.Errorf("total = %d, want %d (race lost increments)", total, workers*each)
	}
}
