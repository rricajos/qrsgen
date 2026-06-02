package downstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureRequest construye un Client apuntado a un httptest.Server que
// captura el header y path del primer request entrante. Útil para asertar
// que los Options aplican correctamente sin acoplar a una API completa.
func captureRequest(t *testing.T, opts ...Option) (capturedHeader func() http.Header, capturedPath func() string, c *Client) {
	t.Helper()
	var header http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"payload":[]}`))
	}))
	t.Cleanup(srv.Close)
	c = New(srv.URL, "tok-test", 7, opts...)
	return func() http.Header { return header }, func() string { return path }, c
}

func TestClient_DefaultsBackwardCompat(t *testing.T) {
	// Sin opts → comportamiento exacto pre-v0.65.0: header api_access_token
	// con el token raw, path /api/v1/accounts/{accountID}/...
	hdr, path, c := captureRequest(t)
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("api_access_token"); got != "tok-test" {
		t.Errorf("header api_access_token = %q, want tok-test", got)
	}
	if got := hdr().Get("Authorization"); got != "" {
		t.Errorf("Authorization should be empty in default mode, got %q", got)
	}
	if path() != "/api/v1/accounts/7/contacts/search" {
		t.Errorf("path = %q, want /api/v1/accounts/7/contacts/search", path())
	}
}

func TestClient_WithAuthHeader_CustomName(t *testing.T) {
	hdr, _, c := captureRequest(t, WithAuthHeader("X-API-Key"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("X-API-Key"); got != "tok-test" {
		t.Errorf("X-API-Key = %q, want tok-test", got)
	}
	if got := hdr().Get("api_access_token"); got != "" {
		t.Errorf("api_access_token should be empty when WithAuthHeader changes name, got %q", got)
	}
}

func TestClient_WithAuthScheme_Bearer(t *testing.T) {
	hdr, _, c := captureRequest(t, WithAuthHeader("Authorization"), WithAuthScheme("Bearer"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("Authorization"); got != "Bearer tok-test" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok-test")
	}
}

func TestClient_WithAuthScheme_Basic(t *testing.T) {
	// El token ya debe llegar base64-encoded (responsabilidad del operador).
	hdr, _, c := captureRequest(t, WithAuthHeader("Authorization"), WithAuthScheme("Basic"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("Authorization"); got != "Basic tok-test" {
		t.Errorf("Authorization = %q, want %q", got, "Basic tok-test")
	}
}

func TestClient_WithAuthScheme_CustomPrefix(t *testing.T) {
	// Cualquier scheme ≠ raw/Bearer/Basic se usa literal + espacio + token.
	hdr, _, c := captureRequest(t, WithAuthHeader("Authorization"), WithAuthScheme("Token"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("Authorization"); got != "Token tok-test" {
		t.Errorf("Authorization = %q, want %q", got, "Token tok-test")
	}
}

func TestClient_WithAuthScheme_RawExplicit(t *testing.T) {
	// raw explícito === sin scheme === comportamiento default.
	hdr, _, c := captureRequest(t, WithAuthScheme("raw"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("api_access_token"); got != "tok-test" {
		t.Errorf("api_access_token = %q, want tok-test", got)
	}
}

func TestClient_WithAPIPathPrefix_AccountIDSubst(t *testing.T) {
	_, path, c := captureRequest(t, WithAPIPathPrefix("/v2/tenants/{account_id}"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if path() != "/v2/tenants/7/contacts/search" {
		t.Errorf("path = %q, want /v2/tenants/7/contacts/search", path())
	}
}

func TestClient_WithAPIPathPrefix_Empty(t *testing.T) {
	// Prefix vacío → paths absolutos directos desde baseURL.
	_, path, c := captureRequest(t, WithAPIPathPrefix(""))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if path() != "/contacts/search" {
		t.Errorf("path = %q, want /contacts/search", path())
	}
}

func TestClient_WithAPIPathPrefix_NoAccountIDToken(t *testing.T) {
	// Prefix sin {account_id} → se usa literal sin substitución.
	_, path, c := captureRequest(t, WithAPIPathPrefix("/api/v3"))
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if path() != "/api/v3/contacts/search" {
		t.Errorf("path = %q, want /api/v3/contacts/search", path())
	}
}

func TestClient_OptionsCombined_ZendeskShape(t *testing.T) {
	// Combo realista para un downstream tipo Zendesk:
	// - Header "Authorization: Bearer TOKEN"
	// - Prefix sin tenant token
	hdr, path, c := captureRequest(t,
		WithAuthHeader("Authorization"),
		WithAuthScheme("Bearer"),
		WithAPIPathPrefix("/api/v2"),
	)
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if got := hdr().Get("Authorization"); got != "Bearer tok-test" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok-test")
	}
	if path() != "/api/v2/contacts/search" {
		t.Errorf("path = %q, want /api/v2/contacts/search", path())
	}
}
