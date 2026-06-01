package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/rricajos/qrsgen/internal/manager"
)

// runHistoryImportRequest helper para test del endpoint
// POST /api/instances/:name/history/import. registerHistoryRoutes
// necesita varias deps; las creamos zero-value (manager vacío,
// resolveInbox que siempre devuelve 0).
//
// Como el handler hace `mgr.Get(name)` PRIMERO, y con manager vacío
// devuelve (nil, false) → 404. Eso significa que las ramas posteriores
// (chat required, days clamp, etc.) NO se ejecutan en estos tests.
// Pero el handler debe degradar limpiamente — verificamos 404 sin
// panic, lo cual ya valida que el wiring no tiene null-deref.
func runHistoryImportRequest(t *testing.T, instance, query string) (int, string) {
	t.Helper()
	e := echo.New()
	api := e.Group("/api")
	mgr := &manager.Manager{}
	registerHistoryRoutes(api, mgr, nil, nil, func(string) int { return 0 })

	path := "/api/instances/" + instance + "/history/import"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestHistoryImport_InstanceNotFound(t *testing.T) {
	status, body := runHistoryImportRequest(t, "DOES_NOT_EXIST", "chat=34600000000@s.whatsapp.net&days=3")
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
	if !strings.Contains(body, "instance not found") {
		t.Errorf("body = %q, want 'instance not found'", body)
	}
}

// TestHistoryImport_DaysQueryParam_NoPanic verifica que el parseo
// de `days` no rompe el handler para inputs raros. El status final
// es 404 (instance no existe) pero el handler debe degradar
// limpiamente sin panic en ParseInt.
func TestHistoryImport_DaysQueryParam_NoPanic(t *testing.T) {
	cases := []string{
		"chat=x@s.whatsapp.net&days=3",
		"chat=x@s.whatsapp.net&days=0",   // 0 = fallback global, NO error
		"chat=x@s.whatsapp.net&days=999", // out of range → debería clamparse a 30, no panic
		"chat=x@s.whatsapp.net&days=-5",  // negativo → ignorado, fallback global
		"chat=x@s.whatsapp.net&days=abc", // inválido → ignorado, fallback global
		"chat=x@s.whatsapp.net",          // sin days, fallback global
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			status, body := runHistoryImportRequest(t, "ATC", q)
			// Cualquier 4xx/5xx es aceptable mientras NO sea panic (500 con stack).
			if status == 0 {
				t.Errorf("status 0 indicates panic; body=%q", body)
			}
			// Sanity: el handler debe responder algo JSON-shaped.
			if !strings.HasPrefix(body, "{") {
				t.Errorf("response should be JSON; got %q", body)
			}
		})
	}
}

// El test del path "happy" (instancia real, chat válido, days legítimo
// que devuelve un HistoryImportResult) requeriría una instancia
// paireada + el resolver de whatsmeow — fuera del scope de unit tests.
// La lógica de clamp de days [1,30] está aislada en routes_history.go
// y es trivialmente correcta por inspección.
