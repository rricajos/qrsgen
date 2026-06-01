package bridge

import (
	"context"
	"testing"
	"time"
)

func TestChatAnchorTracker_RecordAndFind(t *testing.T) {
	tr := newChatAnchorTracker()
	now := time.Now()
	tr.Record("inst1", "120363@g.us", "WAID:A", now)

	waid, ts, ok := tr.Find(context.Background(), "inst1", "120363@g.us")
	if !ok {
		t.Fatal("expected found")
	}
	if waid != "WAID:A" {
		t.Errorf("waid = %q", waid)
	}
	if !ts.Equal(now) {
		t.Errorf("ts = %v want %v", ts, now)
	}
}

func TestChatAnchorTracker_FindMissReturnsFalse(t *testing.T) {
	tr := newChatAnchorTracker()
	_, _, ok := tr.Find(context.Background(), "inst1", "nope@g.us")
	if ok {
		t.Error("expected miss")
	}
}

func TestChatAnchorTracker_OnlyUpdatesIfNewer(t *testing.T) {
	tr := newChatAnchorTracker()
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()

	tr.Record("inst1", "120363@g.us", "WAID:newer", newer)
	tr.Record("inst1", "120363@g.us", "WAID:older", older)

	waid, _, _ := tr.Find(context.Background(), "inst1", "120363@g.us")
	if waid != "WAID:newer" {
		t.Errorf("expected newer to win, got %q", waid)
	}
}

func TestChatAnchorTracker_EmptyWAIDOrZeroTSIgnored(t *testing.T) {
	tr := newChatAnchorTracker()
	tr.Record("inst1", "120363@g.us", "", time.Now()) // empty waid
	tr.Record("inst1", "120363@g.us", "WAID:A", time.Time{}) // zero ts

	if _, _, ok := tr.Find(context.Background(), "inst1", "120363@g.us"); ok {
		t.Error("invalid records should be ignored")
	}
}

func TestChatAnchorTracker_PerInstanceIsolation(t *testing.T) {
	tr := newChatAnchorTracker()
	now := time.Now()
	tr.Record("instA", "120363@g.us", "WAID:A", now)
	tr.Record("instB", "120363@g.us", "WAID:B", now)

	wA, _, _ := tr.Find(context.Background(), "instA", "120363@g.us")
	wB, _, _ := tr.Find(context.Background(), "instB", "120363@g.us")
	if wA != "WAID:A" || wB != "WAID:B" {
		t.Errorf("isolation broken: A=%q B=%q", wA, wB)
	}
}
