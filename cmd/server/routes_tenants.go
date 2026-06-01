package main

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/rricajos/qrsgen/internal/audit"
	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/tenant"
)

// registerTenantRoutes monta los endpoints HTTP de gestión multi-tenant
// (CRUD sobre `bridge_tenant`, mapeo owner_tag → downstream config).
// Extraído de main.go en v0.54.3.
//
// El campo `downstream_api_token` JAMÁS se devuelve en GET — sólo se
// acepta en PUT/PATCH. Esa garantía la cumple `tenant.Config` con
// `json:"-"` en el struct y la limpieza defensiva en el handler de Get.
func registerTenantRoutes(api *echo.Group, tenants *tenant.Resolver, dsRegistry *downstream.Registry, auditLog *audit.Logger) {
	// GET /api/tenants — lista todos (sin tokens).
	api.GET("/tenants", func(c echo.Context) error {
		list, err := tenants.List(c.Request().Context())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if list == nil {
			list = []tenant.Config{}
		}
		return c.JSON(http.StatusOK, list)
	})

	// GET /api/tenants/:owner_tag — sin token.
	api.GET("/tenants/:owner_tag", func(c echo.Context) error {
		cfg, err := tenants.Get(c.Request().Context(), c.Param("owner_tag"))
		if err != nil {
			if errors.Is(err, tenant.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "tenant not found"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		// Defensa en profundidad: no serializamos el token aunque el struct ya
		// lo marca como `json:"-"`.
		out := *cfg
		out.DownstreamAPIToken = ""
		return c.JSON(http.StatusOK, out)
	})

	// PUT /api/tenants/:owner_tag — upsert (replace semantics).
	api.PUT("/tenants/:owner_tag", func(c echo.Context) error {
		ownerTag := c.Param("owner_tag")
		var req struct {
			DownstreamBaseURL   string `json:"downstream_base_url"`
			DownstreamAPIToken  string `json:"downstream_api_token"`
			DownstreamAccountID int    `json:"downstream_account_id"`
			DownstreamInboxID   int    `json:"downstream_inbox_id"`
			WebhookHMACSecret   string `json:"webhook_hmac_secret"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad json"})
		}
		cfg := tenant.Config{
			OwnerTag:            ownerTag,
			DownstreamBaseURL:   req.DownstreamBaseURL,
			DownstreamAPIToken:  req.DownstreamAPIToken,
			DownstreamAccountID: req.DownstreamAccountID,
			DownstreamInboxID:   req.DownstreamInboxID,
			WebhookHMACSecret:   req.WebhookHMACSecret,
		}
		if err := tenants.Set(c.Request().Context(), cfg); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		dsRegistry.InvalidateTenant(ownerTag)
		auditLog.Record(c.Request().Context(), "api", "tenant.upsert", "", ownerTag, map[string]any{
			"downstream_base_url":     req.DownstreamBaseURL,
			"downstream_account_id":   req.DownstreamAccountID,
			"downstream_inbox_id":     req.DownstreamInboxID,
			"webhook_hmac_secret_set": req.WebhookHMACSecret != "",
		})
		return c.JSON(http.StatusOK, map[string]string{"message": "tenant saved", "owner_tag": ownerTag})
	})

	// PATCH /api/tenants/:owner_tag — partial update. Solo se modifican los
	// campos presentes en el body (con valor distinto de "missing").
	api.PATCH("/tenants/:owner_tag", func(c echo.Context) error {
		ownerTag := c.Param("owner_tag")
		raw := map[string]any{}
		if err := c.Bind(&raw); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad json"})
		}
		// Aceptamos solo el subset whitelisteado por Resolver.Patch.
		fields := map[string]any{}
		for _, k := range []string{
			"downstream_base_url", "downstream_api_token",
			"downstream_account_id", "downstream_inbox_id",
			"webhook_hmac_secret",
		} {
			if v, ok := raw[k]; ok {
				fields[k] = v
			}
		}
		if _, err := tenants.Patch(c.Request().Context(), ownerTag, fields); err != nil {
			if errors.Is(err, tenant.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "tenant not found"})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		dsRegistry.InvalidateTenant(ownerTag)
		// Audit metadata reporta SOLO las keys tocadas (no los valores — pueden ser secretos).
		touched := make([]string, 0, len(fields))
		for k := range fields {
			touched = append(touched, k)
		}
		auditLog.Record(c.Request().Context(), "api", "tenant.patch", "", ownerTag, map[string]any{
			"fields": touched,
		})
		return c.JSON(http.StatusOK, map[string]string{"message": "tenant patched", "owner_tag": ownerTag})
	})

	// DELETE /api/tenants/:owner_tag — instancias con ese owner_tag caen al fallback global.
	api.DELETE("/tenants/:owner_tag", func(c echo.Context) error {
		ownerTag := c.Param("owner_tag")
		if err := tenants.Delete(c.Request().Context(), ownerTag); err != nil {
			if errors.Is(err, tenant.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "tenant not found"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		dsRegistry.InvalidateTenant(ownerTag)
		auditLog.Record(c.Request().Context(), "api", "tenant.delete", "", ownerTag, nil)
		return c.JSON(http.StatusOK, map[string]string{"message": "tenant deleted", "owner_tag": ownerTag})
	})
}
