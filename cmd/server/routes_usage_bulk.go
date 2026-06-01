package main

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/rricajos/qrsgen/internal/manager"
	"github.com/rricajos/qrsgen/internal/usage"
)

// registerUsageRoutes monta los 2 endpoints de reporte de usage:
//
//   - GET /api/usage           — filas diarias por instancia
//   - GET /api/usage/summary   — agregado mensual por owner_tag
//
// Diseñados para que el integrador (n8n/CRM/billing) sume contadores
// según su modelo de tenant. Extraído de main.go en v0.54.3.
func registerUsageRoutes(api *echo.Group, usageTracker *usage.Tracker) {
	api.GET("/usage/summary", func(c echo.Context) error {
		from, to := parseMonthRange(c)
		rows, err := usageTracker.MonthlySummary(c.Request().Context(), from, to)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"from": from,
			"to":   to,
			"rows": rows,
		})
	})

	api.GET("/usage", func(c echo.Context) error {
		from, to := parseUsageRange(c)
		rows, err := usageTracker.QueryAll(c.Request().Context(), from, to)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"from": from,
			"to":   to,
			"rows": rows,
		})
	})
}

// registerBulkRoutes monta los endpoints de operaciones bulk sobre
// instancias:
//
//   - POST /api/instances/bulk           — crea/reusa varias
//   - GET  /api/instances/bulk/status    — status rico para múltiples
//
// Idempotentes; errores parciales NO abortan el batch. Extraído de
// main.go en v0.54.3.
func registerBulkRoutes(api *echo.Group, mgr *manager.Manager) {
	api.POST("/instances/bulk", func(c echo.Context) error {
		var req struct {
			Names []string `json:"names"`
		}
		if err := c.Bind(&req); err != nil || len(req.Names) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing names array"})
		}
		results := mgr.BulkCreate(c.Request().Context(), req.Names)
		return c.JSON(http.StatusOK, results)
	})

	api.GET("/instances/bulk/status", func(c echo.Context) error {
		raw := c.QueryParam("names")
		if raw == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing ?names=a,b,c"})
		}
		var names []string
		for _, n := range strings.Split(raw, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				names = append(names, n)
			}
		}
		return c.JSON(http.StatusOK, mgr.BulkStatus(c.Request().Context(), names))
	})
}
