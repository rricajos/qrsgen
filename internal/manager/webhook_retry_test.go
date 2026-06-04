package manager

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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

func TestSetLifecycleWebhookTimeout_Default(t *testing.T) {
	// Sin SetLifecycleWebhookTimeout, el postWebhookOnce usa 10s default.
	// Sanity check: el getter retorna 0 (no configurado), señal a
	// postWebhookOnce de usar default.
	m := newTestManager(t)
	if m.lifecycleWebhookTimeout != 0 {
		t.Errorf("unset lifecycleWebhookTimeout = %v, want 0 (default branch)", m.lifecycleWebhookTimeout)
	}
}

func TestSetLifecycleWebhookTimeout_Override(t *testing.T) {
	m := newTestManager(t)
	m.SetLifecycleWebhookTimeout(45 * time.Second)
	if m.lifecycleWebhookTimeout != 45*time.Second {
		t.Errorf("lifecycleWebhookTimeout = %v, want 45s", m.lifecycleWebhookTimeout)
	}
}

func TestPostWebhookOnce_RespectsCustomTimeout(t *testing.T) {
	// Server que tarda 200ms en responder. Con timeout 50ms el cliente
	// debe abortar antes con error de timeout. Valida que el timeout
	// custom realmente se aplica (no es solo cosmético en el field).
	m := newTestManager(t)
	m.SetLifecycleWebhookTimeout(50 * time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	err := m.postWebhookOnce(srv.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
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
