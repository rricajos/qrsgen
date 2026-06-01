package bridge

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func msgHistoryDSN() string { return os.Getenv("INTEGRATION_PG_DSN") }

func newMsgHistoryPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := msgHistoryDSN()
	if dsn == "" {
		t.Skip("INTEGRATION_PG_DSN not set; skipping msg_history integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanMsgHistory borra todas las filas — los tests no asumen estado previo.
func cleanMsgHistory(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, _ = pool.Exec(context.Background(), `DELETE FROM bridge_msg_history`)
}

func TestIntegration_MsgHistory_SchemaIdempotent(t *testing.T) {
	pool := newMsgHistoryPool(t)
	ctx := context.Background()
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}

func TestIntegration_MsgHistory_RecordPersists(t *testing.T) {
	pool := newMsgHistoryPool(t)
	ctx := context.Background()
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanMsgHistory(t, pool)

	tr := newMsgHistoryTracker(10)
	tr.SetPool(pool, nil)
	tr.Record("inst1", "34604021705@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 1, phone: "+34604021705",
		nameUsed: "Richard", wasSaved: false,
		body: "hola", postedAt: time.Now(),
	})

	// Record persiste async — esperar a que la goroutine termine.
	// 500ms es generoso pero deterministic en CI.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bridge_msg_history WHERE msg_id = 1`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var inst, jid, body, name string
	var wasSaved bool
	err := pool.QueryRow(ctx, `
		SELECT instance, sender_jid, body, name_used, was_saved
		FROM bridge_msg_history WHERE msg_id = 1
	`).Scan(&inst, &jid, &body, &name, &wasSaved)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if inst != "inst1" || jid != "34604021705@s.whatsapp.net" {
		t.Errorf("instance/jid: %q / %q", inst, jid)
	}
	if body != "hola" || name != "Richard" || wasSaved {
		t.Errorf("body=%q name=%q saved=%v", body, name, wasSaved)
	}
}

func TestIntegration_MsgHistory_WarmupReloads(t *testing.T) {
	pool := newMsgHistoryPool(t)
	ctx := context.Background()
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanMsgHistory(t, pool)

	// Tracker A: graba 2 entries, espera persist.
	a := newMsgHistoryTracker(10)
	a.SetPool(pool, nil)
	a.Record("inst1", "u@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 1, nameUsed: "A", body: "msg1", postedAt: time.Now(),
	})
	a.Record("inst1", "u@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 2, nameUsed: "A", body: "msg2", postedAt: time.Now(),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bridge_msg_history`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Tracker B: fresco, warmup desde DB.
	b := newMsgHistoryTracker(10)
	b.SetPool(pool, nil)
	if err := b.Warmup(ctx, 1*time.Hour); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	entries := b.ListBySender("inst1", "u@s.whatsapp.net")
	if len(entries) != 2 {
		t.Fatalf("warmup loaded %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].body != "msg1" || entries[1].body != "msg2" {
		t.Errorf("order: %+v", entries)
	}
}

func TestIntegration_MsgHistory_UpdateAfterPatchPersists(t *testing.T) {
	pool := newMsgHistoryPool(t)
	ctx := context.Background()
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanMsgHistory(t, pool)

	tr := newMsgHistoryTracker(10)
	tr.SetPool(pool, nil)
	tr.Record("inst1", "u@s.whatsapp.net", trackedMsg{
		convID: 100, msgID: 42, nameUsed: "Old", wasSaved: false,
		body: "hola", postedAt: time.Now(),
	})

	// Esperar persist inicial.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM bridge_msg_history WHERE msg_id = 42`).Scan(&n)
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	tr.UpdateAfterPatch("inst1", "u@s.whatsapp.net", 42, "New", true)

	// Esperar persist del update.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var name string
		var saved bool
		_ = pool.QueryRow(ctx, `
			SELECT name_used, was_saved FROM bridge_msg_history WHERE msg_id = 42
		`).Scan(&name, &saved)
		if name == "New" && saved {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("UpdateAfterPatch did not persist within timeout")
}

func TestIntegration_MsgHistory_CleanupOldBorraEntries(t *testing.T) {
	pool := newMsgHistoryPool(t)
	ctx := context.Background()
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanMsgHistory(t, pool)

	// Insertar 2 entries: una vieja (posted_at = -2h), una reciente.
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now()
	if _, err := pool.Exec(ctx, `
		INSERT INTO bridge_msg_history (instance, sender_jid, conv_id, msg_id, posted_at)
		VALUES ('inst1', 'u@s.whatsapp.net', 100, 1, $1),
		       ('inst1', 'u@s.whatsapp.net', 100, 2, $2)
	`, old, recent); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tr := newMsgHistoryTracker(10)
	tr.SetPool(pool, nil)
	n, err := tr.CleanupOld(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Errorf("cleanup deleted %d, want 1", n)
	}
	var remaining int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM bridge_msg_history`).Scan(&remaining)
	if remaining != 1 {
		t.Errorf("remaining=%d, want 1", remaining)
	}
}

// v0.58.0: cobertura del DropInstance (v0.53.3) — verifica que las
// rows DB se borran para la instancia indicada, dejando intactas las
// del resto.
func TestIntegration_MsgHistory_DropInstancePersistedClean(t *testing.T) {
	pool := newMsgHistoryPool(t)
	ctx := context.Background()
	if err := EnsureMsgHistorySchema(ctx, pool); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanMsgHistory(t, pool)

	tr := newMsgHistoryTracker(100)
	tr.SetPool(pool, nil)

	// 3 entries para instA + 2 para instB.
	now := time.Now()
	for i := 1; i <= 3; i++ {
		tr.Record("instA", "u1@s.whatsapp.net", trackedMsg{
			convID: 1, msgID: i, body: "A", postedAt: now,
		})
	}
	for i := 1; i <= 2; i++ {
		tr.Record("instB", "u1@s.whatsapp.net", trackedMsg{
			convID: 1, msgID: 100 + i, body: "B", postedAt: now,
		})
	}

	// Esperar que las inserciones async terminen.
	time.Sleep(100 * time.Millisecond)

	n, err := tr.DropInstance(ctx, "instA")
	if err != nil {
		t.Fatalf("DropInstance: %v", err)
	}
	if n != 3 {
		t.Errorf("DropInstance rows = %d, want 3", n)
	}

	// Verificar en DB: instA fuera, instB intacta.
	var countA, countB int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM bridge_msg_history WHERE instance='instA'`).Scan(&countA)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM bridge_msg_history WHERE instance='instB'`).Scan(&countB)
	if countA != 0 {
		t.Errorf("instA rows after drop = %d, want 0", countA)
	}
	if countB != 2 {
		t.Errorf("instB rows after drop = %d, want 2", countB)
	}
}
