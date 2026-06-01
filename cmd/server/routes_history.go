package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"go.mau.fi/whatsmeow/types"

	"github.com/rricajos/qrsgen/internal/bridge"
	"github.com/rricajos/qrsgen/internal/manager"
)

// registerHistoryRoutes monta los 3 endpoints de history import:
//
//   - POST /api/instances/:name/history/import-all-async (v0.52.0)
//   - POST /api/instances/:name/history/import-all (v0.46.1)
//   - POST /api/instances/:name/history/import (v0.46.0)
//
// `resolveInbox` mapea nombre de instancia → inbox_id del downstream
// (lookup en bridge_instance + fallback a tenant config). Inyectado
// para no acoplar este archivo a la lógica del Registry. Extraído de
// main.go en v0.54.3.
func registerHistoryRoutes(api *echo.Group, mgr *manager.Manager, incoming *bridge.Incoming, jobs *bridge.JobStore, resolveInbox func(name string) int) {
	// POST /api/instances/:name/history/import-all-async (v0.52.0)
	// Variante async del bulk history import. Devuelve inmediatamente
	// con `job_id`; cliente sondea GET /jobs/:id para progreso/resultado.
	// Pensado para inboxes grandes (>100 contactos).
	api.POST("/instances/:name/history/import-all-async", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		inboxID := resolveInbox(instance)
		if inboxID <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "no inbox_id for instance"})
		}
		count := 50
		if v := c.QueryParam("count_per_chat"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				count = n
			}
		}
		timeoutSec := 30
		if v := c.QueryParam("timeout_per_chat"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				timeoutSec = n
			}
		}
		job := jobs.Create("bulk_history_import", instance)
		jobs.RunAsync(job, func(ctx context.Context) (any, error) {
			return incoming.BulkImportHistory(
				ctx,
				instance,
				inboxID,
				count,
				time.Duration(timeoutSec)*time.Second,
				conn,
			)
		})
		return c.JSON(http.StatusAccepted, map[string]any{
			"job_id": job.ID,
			"status": job.Status,
		})
	})

	// POST /api/instances/:name/history/import-all (v0.46.1)
	// Bulk import síncrono — itera TODOS los contactos del inbox y
	// dispara on-demand history sync para cada uno. Bloquea hasta
	// terminar; para inboxes grandes considera la variante async.
	//
	// Query params: count_per_chat=N (default 50), timeout_per_chat=N (default 30s).
	api.POST("/instances/:name/history/import-all", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		inboxID := resolveInbox(instance)
		if inboxID <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "no inbox_id for instance"})
		}
		count := 50
		if v := c.QueryParam("count_per_chat"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				count = n
			}
		}
		timeoutSec := 30
		if v := c.QueryParam("timeout_per_chat"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				timeoutSec = n
			}
		}
		result, err := incoming.BulkImportHistory(
			c.Request().Context(),
			instance,
			inboxID,
			count,
			time.Duration(timeoutSec)*time.Second,
			conn,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, result)
	})

	// POST /api/instances/:name/history/import (v0.46.0, days añadido v0.54.4)
	// On-demand history sync para un chat específico.
	//
	// Query params:
	//   - chat=<jid>           (requerido) chat JID a importar
	//   - count=N              (opcional, default 50, max 200)
	//   - timeout_sec=N        (opcional, default 30)
	//   - days=N               (opcional, v0.54.4) — cota de antigüedad
	//                          per-request. Si está set, sobreescribe el
	//                          QRSGEN_HISTORY_IMPORT_DAYS global. Útil para
	//                          importar sólo los últimos N días sin tocar
	//                          la config del proceso. Clamp [1, 30].
	//
	// Bloquea hasta recibir el HistorySync ON_DEMAND del phone.
	// Requiere QRSGEN_HISTORY_IMPORT_ENABLED=true.
	api.POST("/instances/:name/history/import", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		chatStr := c.QueryParam("chat")
		if chatStr == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "chat query param required"})
		}
		chatJID, err := types.ParseJID(chatStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid chat jid: " + err.Error()})
		}
		count := 50
		if v := c.QueryParam("count"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				count = n
			}
		}
		timeoutSec := 30
		if v := c.QueryParam("timeout_sec"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				timeoutSec = n
			}
		}
		// v0.54.4: days override per-request. Clamp [1, 30] consistente
		// con QRSGEN_HISTORY_IMPORT_DAYS. 0 = usar el default global.
		var maxAge time.Duration
		if v := c.QueryParam("days"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				if n > 30 {
					n = 30
				}
				maxAge = time.Duration(n) * 24 * time.Hour
			}
		}
		result, err := incoming.ImportHistoryOnDemandWithMaxAge(
			c.Request().Context(),
			instance,
			chatJID,
			count,
			time.Duration(timeoutSec)*time.Second,
			maxAge,
			conn,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, result)
	})
}
