package main

import (
	"bytes"
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
