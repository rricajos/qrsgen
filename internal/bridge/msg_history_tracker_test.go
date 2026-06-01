package bridge

import (
	"context"
	"testing"
	"time"
)

func TestMsgHistoryTracker_RecordAndList(t *testing.T) {
	tr := newMsgHistoryTracker(10)
	now := time.Now()

	tr.Record("inst1", "user@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 1, phone: "+34600000001",
		nameUsed: "Old Name", wasSaved: false,
		body: "hola", postedAt: now,
	})
	tr.Record("inst1", "user@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 2, phone: "+34600000001",
		nameUsed: "Old Name", wasSaved: false,
		body: "qué tal", postedAt: now.Add(time.Minute),
	})

	got := tr.ListBySender("inst1", "user@s.whatsapp.net")
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
	if got[0].body != "hola" || got[1].body != "qué tal" {
		t.Errorf("entries out of order: %+v", got)
	}
}

func TestMsgHistoryTracker_CapEnforced(t *testing.T) {
	tr := newMsgHistoryTracker(3)
	now := time.Now()

	for i := 1; i <= 5; i++ {
		tr.Record("inst1", "u@s.whatsapp.net", trackedMsg{
			msgID: i, body: "msg", postedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	got := tr.ListBySender("inst1", "u@s.whatsapp.net")
	if len(got) != 3 {
		t.Errorf("cap=3, expected 3 entries, got %d", len(got))
	}
	// Los más viejos (msg 1, 2) deben haberse descartado
	for _, e := range got {
		if e.msgID < 3 {
			t.Errorf("cap should evict oldest, but msgID=%d still present", e.msgID)
		}
	}
}

func TestMsgHistoryTracker_PerSenderIsolation(t *testing.T) {
	tr := newMsgHistoryTracker(10)
	tr.Record("inst1", "userA@s.whatsapp.net", trackedMsg{msgID: 1})
	tr.Record("inst1", "userB@s.whatsapp.net", trackedMsg{msgID: 2})

	a := tr.ListBySender("inst1", "userA@s.whatsapp.net")
	b := tr.ListBySender("inst1", "userB@s.whatsapp.net")
	if len(a) != 1 || a[0].msgID != 1 {
		t.Errorf("A: got %v", a)
	}
	if len(b) != 1 || b[0].msgID != 2 {
		t.Errorf("B: got %v", b)
	}
}

func TestMsgHistoryTracker_UpdateAfterPatch(t *testing.T) {
	tr := newMsgHistoryTracker(10)
	tr.Record("inst1", "u@s.whatsapp.net", trackedMsg{
		msgID: 42, nameUsed: "Old", wasSaved: false,
	})
	tr.UpdateAfterPatch("inst1", "u@s.whatsapp.net", 42, "New", true)
	got := tr.ListBySender("inst1", "u@s.whatsapp.net")
	if got[0].nameUsed != "New" || !got[0].wasSaved {
		t.Errorf("post-patch: got %+v", got[0])
	}
}

func TestMsgHistoryTracker_EmptyListReturnsNil(t *testing.T) {
	tr := newMsgHistoryTracker(10)
	got := tr.ListBySender("inst1", "nobody@s.whatsapp.net")
	if got != nil {
		t.Errorf("empty: got %v, want nil", got)
	}
}

func TestMsgHistoryTracker_FindByWAID(t *testing.T) {
	tr := newMsgHistoryTracker(10)
	tr.Record("inst1", "u@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 42, waid: "WAID:ABC123",
		body: "hola", postedAt: time.Now(),
	})

	got, ok := tr.FindByWAID(context.Background(), "inst1", "WAID:ABC123")
	if !ok || got.msgID != 42 || got.body != "hola" {
		t.Errorf("got %+v ok=%v", got, ok)
	}

	if _, ok := tr.FindByWAID(context.Background(), "inst1", "WAID:NOPE"); ok {
		t.Error("expected miss for unknown WAID")
	}

	if _, ok := tr.FindByWAID(context.Background(), "inst1", ""); ok {
		t.Error("expected miss for empty WAID")
	}

	// Aislamiento per-instance
	if _, ok := tr.FindByWAID(context.Background(), "inst-other", "WAID:ABC123"); ok {
		t.Error("expected miss across instances")
	}
}
