package manager

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	// Manager con campos mínimos suficientes para postWebhookOnce. NO se
	// llama a New() porque eso requiere whatsmeow container — sólo
	// instanciamos manualmente lo necesario.
	return &Manager{
		logger: slog.New(slog.DiscardHandler),
	}
}

func TestPostWebhookOnce_Success(t *testing.T) {
	m := newTestManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := m.postWebhookOnce(srv.URL, []byte(`{"ok":true}`)); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPostWebhookOnce_4xx(t *testing.T) {
	m := newTestManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	err := m.postWebhookOnce(srv.URL, []byte(`{}`))
	if err == nil {
		t.Error("expected error on 4xx, got nil")
	}
}

func TestPostWebhookOnce_5xx(t *testing.T) {
	m := newTestManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := m.postWebhookOnce(srv.URL, []byte(`{}`))
	if err == nil {
		t.Error("expected error on 5xx, got nil")
	}
}

func TestPostWebhookOnce_NetworkError(t *testing.T) {
	m := newTestManager(t)
	err := m.postWebhookOnce("http://127.0.0.1:1/nope", []byte(`{}`))
	if err == nil {
		t.Error("expected network error to closed port")
	}
}

func TestCriticalEvents_AllExpected(t *testing.T) {
	expected := []string{"strike", "ban_risk", "outgoing_expired", "logged_out", "spam_blocked", "backend_restarting"}
	for _, ev := range expected {
		if !criticalLifecycleEvents[ev] {
			t.Errorf("event %q must be marked critical", ev)
		}
	}
}

func TestNonCriticalEvents_NotInList(t *testing.T) {
	// Estos NO deben tener retry porque qrsgen los re-emite naturalmente o
	// son de tan alta frecuencia que la pérdida es irrelevante.
	notCritical := []string{"qr_generated", "paired", "connected", "reconnected", "unreachable", "disconnected", "backend_started"}
	for _, ev := range notCritical {
		if criticalLifecycleEvents[ev] {
			t.Errorf("event %q must NOT be in critical list (it's frequent / self-recoverable)", ev)
		}
	}
}

// Smoke test del flujo: si el evento es no-crítico y el POST falla, no se
// reintenta. Verificamos contando hits al server.
func TestPostWithRetry_NonCriticalNoRetry(t *testing.T) {
	m := newTestManager(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m.postWebhookWithRetry("X", "qr_generated", srv.URL, []byte(`{}`))
	// qr_generated NO es crítico → exactly 1 hit (initial attempt only)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("non-critical event must NOT retry, got %d hits", got)
	}
}
