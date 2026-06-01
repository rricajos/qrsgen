package main

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/rricajos/qrsgen/internal/bridge"
)

// registerJobRoutes monta los 2 endpoints de inspección de jobs async
// (history import all-async, etc.) sobre `api`. Los jobs viven en memoria
// dentro de `jobs` y se purgan tras un TTL configurado en JobStore.
// Extraído de main.go en v0.54.3.
func registerJobRoutes(api *echo.Group, jobs *bridge.JobStore) {
	// GET /api/jobs/:id — estado + resultado de un job async (v0.52.0).
	api.GET("/jobs/:id", func(c echo.Context) error {
		id := c.Param("id")
		job, ok := jobs.Get(id)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "job not found"})
		}
		return c.JSON(http.StatusOK, job)
	})

	// GET /api/jobs — lista de jobs vivos (no purgados). Útil para debug.
	api.GET("/jobs", func(c echo.Context) error {
		return c.JSON(http.StatusOK, jobs.List())
	})
}
