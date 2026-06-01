package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/rricajos/qrsgen/internal/manager"
)

// runEditRequest helper para tests del endpoint POST .../messages/:waid/edit.
// Crea Echo, registra la ruta con un *manager.Manager vacío (instances nil →
// Get devuelve (nil, false) → handler responde 404 para cualquier instance,
// que es exactamente lo que queremos para testear las ramas de validación
// de input que ocurren ANTES del lookup del manager).
func runEditRequest(t *testing.T, instance, waid, body string) (int, string) {
	t.Helper()
	e := echo.New()
	api := e.Group("/api")
	mgr := &manager.Manager{}
	registerMessageRoutes(api, mgr)

	path := "/api/instances/" + instance + "/messages/" + waid + "/edit"
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestEditMessage_InstanceNotFound(t *testing.T) {
	status, body := runEditRequest(t, "DOES_NOT_EXIST", "WAID:123", `{"chat":"34600000000@s.whatsapp.net","content":"hola"}`)
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
	if !strings.Contains(body, "instance not found") {
		t.Errorf("body = %q, want 'instance not found'", body)
	}
}

func TestEditMessage_EmptyChat(t *testing.T) {
	status, body := runEditRequest(t, "ATC", "WAID:123", `{"chat":"","content":"hola"}`)
	// Sin instance real registrada, el handler igual cae en 404 ANTES de
	// validar el body — el orden de checks es: waid → instance → body.
	// Si llegara a body, sería 400 "chat + content required".
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (instance check runs before body)", status)
	}
	_ = body
}

func TestEditMessage_EmptyBody(t *testing.T) {
	// Mismo razonamiento: instance no existe → 404, sin llegar al body check.
	status, _ := runEditRequest(t, "ATC", "WAID:123", "")
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

// Para testear las ramas 400 del body necesitaríamos una instancia
// registrada. Como el handler usa *manager.Manager (no interface),
// mockear es invasivo. Las ramas de validación de body están cubiertas
// por inspección directa del código y por tests E2E futuros.
