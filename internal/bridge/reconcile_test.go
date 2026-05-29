package bridge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rricajos/qrsgen/internal/downstream"
	"go.mau.fi/whatsmeow/types"
)

// stubContact representa el estado de un contacto en el downstream fake.
type stubContact struct {
	ID    int
	Phone string
	Name  string
}

// renameCall captura una llamada PUT /contacts/{id}.
type renameCall struct {
	contactID int
	newName   string
}

// newReconcileIncoming arma un Incoming con un downstream stubbed que
// soporta: GET /contacts/search (encuentra por phone), PUT /contacts/{id}
// (rename), y PATCH /conversations/{c}/messages/{m} (content update).
func newReconcileIncoming(t *testing.T, contacts map[string]*stubContact) (*Incoming, *[]renameCall, *[]patchRecord, *sync.Mutex) {
	t.Helper()
	renames := &[]renameCall{}
	patches := &[]patchRecord{}
	mu := &sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/accounts/7/contacts/search"):
			q := r.URL.Query().Get("q")
			if c, ok := contacts[q]; ok && c != nil {
				resp := map[string]any{
					"payload": []map[string]any{
						{"id": c.ID, "name": c.Name, "phone_number": "+" + c.Phone, "identifier": ""},
					},
				}
				b, _ := json.Marshal(resp)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
				return
			}
			_, _ = w.Write([]byte(`{"payload":[]}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/accounts/7/contacts/"):
			// /api/v1/accounts/7/contacts/{id}
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) < 7 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			id, _ := strconv.Atoi(parts[6])
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			*renames = append(*renames, renameCall{contactID: id, newName: payload.Name})
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch:
			parts := strings.Split(r.URL.Path, "/")
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
			*patches = append(*patches, patchRecord{convID: convID, msgID: msgID, content: payload.Content})
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := downstream.New(srv.URL, "tok", 7)
	router := &fakeRouter{client: client}
	inc := NewIncomingDynamic(router, nil, slog.Default(), func(string) int { return 11 })
	inc.EnableRetroactiveNameUpdate(50)
	return inc, renames, patches, mu
}

func TestHandleContactUpdate_RenamesChatwootContact(t *testing.T) {
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	contacts := map[string]*stubContact{
		"34604021705": {ID: 99, Phone: "34604021705", Name: "Richard"},
	}
	inc, renames, _, mu := newReconcileIncoming(t, contacts)

	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "Ricard", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*renames) != 1 {
		t.Fatalf("got %d renames, want 1", len(*renames))
	}
	if (*renames)[0].contactID != 99 || (*renames)[0].newName != "Ricard Penin" {
		t.Errorf("rename = %+v", (*renames)[0])
	}
}

func TestHandleContactUpdate_DoesNotRenameIfNameAlreadyMatches(t *testing.T) {
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	contacts := map[string]*stubContact{
		"34604021705": {ID: 99, Phone: "34604021705", Name: "Ricard Penin"},
	}
	inc, renames, _, mu := newReconcileIncoming(t, contacts)

	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*renames) != 0 {
		t.Errorf("expected no rename (name matches), got %d", len(*renames))
	}
}

func TestHandleContactUpdate_RenamesAndPatchesBoth(t *testing.T) {
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	contacts := map[string]*stubContact{
		"34604021705": {ID: 99, Phone: "34604021705", Name: "Richard"},
	}
	inc, renames, patches, mu := newReconcileIncoming(t, contacts)

	// Sembramos msg tracked también
	inc.msgHistory.Record("inst1", pn.String(), trackedMsg{
		convID: 100, msgID: 1, phone: "+34604021705",
		nameUsed: "Richard", wasSaved: false,
		body: "hola", postedAt: time.Now(),
	})

	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*renames) != 1 {
		t.Errorf("expected 1 rename, got %d", len(*renames))
	}
	if len(*patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(*patches))
	}
	want := "`+34604021705 · Ricard Penin`\n\nhola"
	if len(*patches) > 0 && (*patches)[0].content != want {
		t.Errorf("patch content = %q, want %q", (*patches)[0].content, want)
	}
}

func TestHandleContactUpdate_RenamesEvenWith1on1NoTrackedMsgs(t *testing.T) {
	pn := types.NewJID("34604021705", types.DefaultUserServer)
	contacts := map[string]*stubContact{
		"34604021705": {ID: 99, Phone: "34604021705", Name: "Richard"},
	}
	inc, renames, patches, mu := newReconcileIncoming(t, contacts)

	// NO sembramos msgs tracked — caso 1:1 puro.
	inc.HandleContactUpdate(context.Background(), "inst1", pn, "Ricard Penin", "", false, nil)
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*renames) != 1 {
		t.Errorf("1:1 rename expected, got %d renames", len(*renames))
	}
	if len(*patches) != 0 {
		t.Errorf("expected 0 patches (no tracked msgs), got %d", len(*patches))
	}
}

func TestReconcileSavedContacts_DispatchesPerContact(t *testing.T) {
	contacts := map[string]*stubContact{
		"34600000001": {ID: 1, Phone: "34600000001", Name: "Old1"},
		"34600000002": {ID: 2, Phone: "34600000002", Name: "Old2"},
	}
	inc, renames, _, mu := newReconcileIncoming(t, contacts)

	jid1 := types.NewJID("34600000001", types.DefaultUserServer)
	jid2 := types.NewJID("34600000002", types.DefaultUserServer)
	r := &fakeResolver{
		names: map[string]string{
			jid1.String(): "New1",
			jid2.String(): "New2",
		},
		savedJIDs: map[string]bool{
			jid1.String(): true,
			jid2.String(): true,
		},
	}

	result, err := inc.ReconcileSavedContacts(context.Background(), "inst1", r)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Scanned != 2 || result.Triggered != 2 {
		t.Errorf("result = %+v", result)
	}
	inc.WaitRetroactivePatches()

	mu.Lock()
	defer mu.Unlock()
	if len(*renames) != 2 {
		t.Errorf("expected 2 renames, got %d: %+v", len(*renames), *renames)
	}
	gotNames := map[int]string{}
	for _, rc := range *renames {
		gotNames[rc.contactID] = rc.newName
	}
	if gotNames[1] != "New1" || gotNames[2] != "New2" {
		t.Errorf("rename map = %+v", gotNames)
	}
}

func TestReconcileSavedContacts_NoOpIfDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	client := downstream.New(srv.URL, "tok", 7)
	inc := NewIncomingDynamic(&fakeRouter{client: client}, nil, slog.Default(), func(string) int { return 11 })
	// NO EnableRetroactiveNameUpdate

	r := &fakeResolver{}
	_, err := inc.ReconcileSavedContacts(context.Background(), "inst1", r)
	if err == nil {
		t.Error("expected error when disabled")
	}
}
