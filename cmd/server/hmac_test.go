package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func signBody(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func runWebhookMW(t *testing.T, secret string, header, bodyStr string) (status int, gotBody string) {
	t.Helper()
	e := echo.New()
	logger := slog.New(slog.DiscardHandler)

	handler := func(c echo.Context) error {
		b, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.String(http.StatusInternalServerError, "read")
		}
		return c.String(http.StatusOK, string(b))
	}

	mw := webhookHMACMiddleware(secret, nil, logger)(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/instances/x/webhook", bytes.NewBufferString(bodyStr))
	if header != "" {
		req.Header.Set("X-Qrsgen-Signature", header)
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := mw(c); err != nil {
		t.Fatalf("middleware err: %v", err)
	}
	return rec.Code, rec.Body.String()
}

func TestWebhookHMAC_NoSecret_AllowsAll(t *testing.T) {
	status, body := runWebhookMW(t, "", "", `{"hello":"world"}`)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != `{"hello":"world"}` {
		t.Errorf("body = %q, want passthrough", body)
	}
}

func TestWebhookHMAC_ValidSignature_Passes(t *testing.T) {
	secret := "supersecret"
	bodyStr := `{"hello":"world"}`
	header := signBody([]byte(secret), []byte(bodyStr))
	status, body := runWebhookMW(t, secret, header, bodyStr)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != bodyStr {
		t.Errorf("body = %q, want %q (body must be restored for downstream Bind)", body, bodyStr)
	}
}

func TestWebhookHMAC_InvalidSignature_Rejects(t *testing.T) {
	status, _ := runWebhookMW(t, "supersecret", "sha256=deadbeef", `{"hello":"world"}`)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestWebhookHMAC_MissingHeader_Rejects(t *testing.T) {
	status, _ := runWebhookMW(t, "supersecret", "", `{"hello":"world"}`)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestWebhookHMAC_NonHexSignature_Rejects(t *testing.T) {
	status, _ := runWebhookMW(t, "supersecret", "sha256=not-hex", `{"hello":"world"}`)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestWebhookHMAC_WrongPrefix_Rejects(t *testing.T) {
	secret := "supersecret"
	bodyStr := `{"hello":"world"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(bodyStr))
	hexSig := hex.EncodeToString(mac.Sum(nil))
	// Valid hex but missing "sha256=" prefix.
	status, _ := runWebhookMW(t, secret, hexSig, bodyStr)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// runWebhookMWWithLookup es como runWebhookMW pero pasa una función
// lookup que simula el resolver per-tenant. v0.57.0.
func runWebhookMWWithLookup(t *testing.T, globalSecret string, lookup tenantHMACSecretLookup, header, bodyStr string) (status int) {
	t.Helper()
	e := echo.New()
	logger := slog.New(slog.DiscardHandler)
	handler := func(c echo.Context) error { return c.String(http.StatusOK, "ok") }
	mw := webhookHMACMiddleware(globalSecret, lookup, logger)(handler)
	req := httptest.NewRequest(http.MethodPost, "/api/instances/x/webhook", bytes.NewBufferString(bodyStr))
	if header != "" {
		req.Header.Set("X-Qrsgen-Signature", header)
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("name")
	c.SetParamValues("x")
	if err := mw(c); err != nil {
		t.Fatalf("middleware err: %v", err)
	}
	return rec.Code
}

// TestWebhookHMAC_PerTenantOverridesGlobal: si el lookup devuelve un
// secret distinto al global, el middleware debe validar contra ese
// (el global se ignora). v0.57.0.
func TestWebhookHMAC_PerTenantOverridesGlobal(t *testing.T) {
	globalSecret := "GLOBAL"
	tenantSecret := "TENANT_X"
	bodyStr := `{"hello":"world"}`
	header := signBody([]byte(tenantSecret), []byte(bodyStr))

	// Lookup devuelve el tenant secret → debería pasar (firmado con tenant).
	lookup := func(_ context.Context, _ string) string { return tenantSecret }
	if got := runWebhookMWWithLookup(t, globalSecret, lookup, header, bodyStr); got != http.StatusOK {
		t.Errorf("tenant secret should pass, got status %d", got)
	}

	// Si firmamos con el global pero el lookup dice usar el tenant, debe rechazar.
	wrongHeader := signBody([]byte(globalSecret), []byte(bodyStr))
	if got := runWebhookMWWithLookup(t, globalSecret, lookup, wrongHeader, bodyStr); got != http.StatusUnauthorized {
		t.Errorf("global-signed body should be rejected when tenant secret active, got %d", got)
	}
}

// TestWebhookHMAC_TenantEmpty_FallsBackToGlobal: cuando el lookup
// devuelve "", el middleware usa el global. v0.57.0.
func TestWebhookHMAC_TenantEmpty_FallsBackToGlobal(t *testing.T) {
	globalSecret := "GLOBAL"
	bodyStr := `{"hello":"world"}`
	header := signBody([]byte(globalSecret), []byte(bodyStr))

	lookup := func(_ context.Context, _ string) string { return "" }
	if got := runWebhookMWWithLookup(t, globalSecret, lookup, header, bodyStr); got != http.StatusOK {
		t.Errorf("fallback to global should pass, got %d", got)
	}
}

// TestWebhookHMAC_BothEmpty_AllowsPassthrough: ambos vacíos →
// backward-compat (sin auth). v0.57.0.
func TestWebhookHMAC_BothEmpty_AllowsPassthrough(t *testing.T) {
	lookup := func(_ context.Context, _ string) string { return "" }
	if got := runWebhookMWWithLookup(t, "", lookup, "", `{"hello":"world"}`); got != http.StatusOK {
		t.Errorf("both empty should pass through, got %d", got)
	}
}

// TestWebhookHMAC_EmptyBodyValidSig: edge case — POST con body vacío
// requiere firma válida del string vacío. v0.57.0.
func TestWebhookHMAC_EmptyBodyValidSig(t *testing.T) {
	secret := "supersecret"
	header := signBody([]byte(secret), []byte(""))
	status, _ := runWebhookMW(t, secret, header, "")
	if status != http.StatusOK {
		t.Errorf("empty body with valid sig should pass, got %d", status)
	}
}
