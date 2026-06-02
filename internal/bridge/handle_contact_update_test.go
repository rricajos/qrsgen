package bridge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rricajos/qrsgen/internal/downstream"
	"go.mau.fi/whatsmeow/types"
)

// fakeRouter wraps a single *downstream.Client. Returns it for any
// instance lookup. Used to satisfy downstream.Router in tests.
type fakeRouter struct {
	client *downstream.Client
}

func (f *fakeRouter) For(_ context.Context, _ string) downstream.DownstreamAPI {
	if f.client == nil {
		return nil
	}
	return f.client
}
func (f *fakeRouter) OwnerTagFor(_ context.Context, _ string) string { return "" }

// patchRecord captura una llamada PATCH al endpoint update message.
type patchRecord struct {
	convID  string
	msgID   string
	content string
}

// newRetroIncoming arma un Incoming con un downstream stubbed por
// httptest.Server que graba todos los PATCHes recibidos. Devuelve
// el incoming + un puntero a la slice de records para assertions.
func newRetroIncoming(t *testing.T) (*Incoming, *[]patchRecord, *sync.Mutex) {
	t.Helper()
	records := &[]patchRecord{}
	mu := &sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /contacts/search → empty array (silences v0.43.0 contact
		// rename lookup warnings en tests que no testean ese path).
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contacts/search") {
			_, _ = w.Write([]byte(`{"payload":[]}`))
			return
		}
		// Solo procesamos PATCH /conversations/{c}/messages/{m}
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusOK)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		// /api/v1/accounts/7/conversations/{c}/messages/{m}
		// idx:  0  1  2   3       4      5         6   7         8
		if len(parts) < 9 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		convID := parts[6]
		msgID := parts[8]
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		*records = append(*records, patchRecord{convID: convID, msgID: msgID, content: payload.Content})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := downstream.New(srv.URL, "tok", 7)
	router := &fakeRouter{client: client}
	inc := NewIncomingDynamic(router, nil, slog.Default(), func(string) int { return 11 })
	inc.EnableRetroactiveNameUpdate(50)
	return inc, records, mu
}

func TestHandleContactUpdate_PatchesStaleEntries(t *testing.T) {
	inc, records, mu := newRetroIncoming(t)
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	key := pn.String()

	// Sembramos 2 mensajes posteados sin name saved.
	inc.msgHistory.Record("inst1", key, trackedMsg{
		convID: 100, msgID: 1, phone: "+34604021705",
		nameUsed: "Richard", wasSaved: false, hasPrefix: true,
		body: "hola", postedAt: time.Now(),
	})
	inc.msgHistory.Record("inst1", key, trackedMsg{
		convID: 100, msgID: 2, phone: "+34604021705",
		nameUsed: "Richard", wasSaved: false, hasPrefix: true,
		body: "qué tal", postedAt: time.Now(),
	})

	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "Ricard", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 2 {
		t.Fatalf("expected 2 PATCHes, got %d: %+v", len(*records), *records)
	}
	wantContent1 := "`+34604021705 · Ricard Penin`\n\nhola"
	wantContent2 := "`+34604021705 · Ricard Penin`\n\nqué tal"
	got := map[string]string{}
	for _, r := range *records {
		got[r.msgID] = r.content
	}
	if got["1"] != wantContent1 {
		t.Errorf("msg 1: got %q, want %q", got["1"], wantContent1)
	}
	if got["2"] != wantContent2 {
		t.Errorf("msg 2: got %q, want %q", got["2"], wantContent2)
	}

	// Tracker debe haberse actualizado (nameUsed=Ricard Penin, wasSaved=true).
	entries := inc.msgHistory.ListBySender("inst1", key)
	for _, e := range entries {
		if e.nameUsed != "Ricard Penin" || !e.wasSaved {
			t.Errorf("post-patch entry: %+v", e)
		}
	}
}

func TestHandleContactUpdate_SkipsFromFullSync(t *testing.T) {
	inc, records, mu := newRetroIncoming(t)
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	inc.msgHistory.Record("inst1", pn.String(), trackedMsg{
		convID: 100, msgID: 1, nameUsed: "Richard", wasSaved: false, hasPrefix: true, body: "hola",
	})

	// fromFullSync=true → no PATCH (evitamos burst al conectar).
	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "", true, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 0 {
		t.Errorf("fromFullSync should skip, got %d PATCHes", len(*records))
	}
}

func TestHandleContactUpdate_SkipsWhenDisabled(t *testing.T) {
	records := &[]patchRecord{}
	mu := &sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*records = append(*records, patchRecord{convID: r.URL.Path})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	client := downstream.New(srv.URL, "tok", 7)
	inc := NewIncomingDynamic(&fakeRouter{client: client}, nil, slog.Default(), func(string) int { return 11 })
	// NO EnableRetroactiveNameUpdate → msgHistory nil.

	pn := types.NewJID("34604021705", types.DefaultUserServer)
	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard", "", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 0 {
		t.Errorf("disabled tracker should no-op, got %d requests", len(*records))
	}
}

func TestHandleContactUpdate_NoEntriesForSender(t *testing.T) {
	inc, records, mu := newRetroIncoming(t)
	pn := types.NewJID("34999999999", types.DefaultUserServer)
	// No msgs sembrados — sender desconocido.
	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Random", "", false, nil)
	inc.WaitRetroactivePatches()
	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 0 {
		t.Errorf("unknown sender: got %d PATCHes", len(*records))
	}
}

func TestHandleContactUpdate_EmptyNameSkips(t *testing.T) {
	inc, records, mu := newRetroIncoming(t)
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	inc.msgHistory.Record("inst1", pn.String(), trackedMsg{
		convID: 100, msgID: 1, nameUsed: "Richard", wasSaved: false, hasPrefix: true, body: "hola",
	})
	// fullName="" AND firstName="" → contacto eliminado de agenda, no
	// tenemos PushName original guardado → saltamos.
	inc.HandleContactUpdate(context.Background(), "inst1", pn, "", "", false, nil)
	inc.WaitRetroactivePatches()
	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 0 {
		t.Errorf("empty name should skip, got %d PATCHes", len(*records))
	}
}

func TestHandleContactUpdate_FirstNameFallback(t *testing.T) {
	inc, records, mu := newRetroIncoming(t)
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	inc.msgHistory.Record("inst1", pn.String(), trackedMsg{
		convID: 100, msgID: 1, nameUsed: "Richard", wasSaved: false, hasPrefix: true, body: "hola",
	})
	// Solo firstName presente → se usa como nombre.
	inc.HandleContactUpdate(context.Background(), "inst1", pn, "", "Ricardo", false, nil)
	inc.WaitRetroactivePatches()
	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 1 {
		t.Fatalf("got %d PATCHes, want 1", len(*records))
	}
	want := "`+34604021705 · Ricardo`\n\nhola"
	if (*records)[0].content != want {
		t.Errorf("content = %q, want %q", (*records)[0].content, want)
	}
}

func TestHandleContactUpdate_AlreadyUpToDateNoPatch(t *testing.T) {
	inc, records, mu := newRetroIncoming(t)
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	// Entry ya tracked con el nombre actual + saved=true → no-op.
	inc.msgHistory.Record("inst1", pn.String(), trackedMsg{
		convID: 100, msgID: 1, phone: "+34604021705",
		nameUsed: "Ricard Penin", wasSaved: true, hasPrefix: true,
		body: "hola", postedAt: time.Now(),
	})

	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "Ricard", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*records) != 0 {
		t.Errorf("up-to-date entry should skip, got %d PATCHes", len(*records))
	}
}
