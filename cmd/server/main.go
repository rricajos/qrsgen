// Command server arranca el servicio qrsgen.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rricajos/qrsgen/internal/audit"
	"github.com/rricajos/qrsgen/internal/banwatch"
	"github.com/rricajos/qrsgen/internal/bridge"
	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/config"
	"github.com/rricajos/qrsgen/internal/lib"
	"github.com/rricajos/qrsgen/internal/manager"
	"github.com/rricajos/qrsgen/internal/outbox"
	"github.com/rricajos/qrsgen/internal/usage"
	"github.com/rricajos/qrsgen/internal/wameow"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.mau.fi/whatsmeow/types/events"
)

func main() {
	// -healthcheck: hace un GET corto contra /api/health del propio binario
	// (a través de 127.0.0.1:PORT) y exitea 0 si HTTP 200, 1 en cualquier otro caso.
	// Pensado para HEALTHCHECK del Dockerfile sin tener que instalar curl en distroless.
	for _, a := range os.Args[1:] {
		if a == "-healthcheck" || a == "--healthcheck" {
			runHealthcheck()
			return
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	logger := lib.NewLogger(cfg.LogLevel)
	logger.Info("starting qrsgen", "default_instance", cfg.InstanceName)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := lib.NewPool(ctx, cfg.PostgresDSN())
	if err != nil {
		logger.Error("postgres pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := lib.EnsureBridgeSchema(ctx, pool); err != nil {
		logger.Error("ensure schema", "err", err)
		os.Exit(1)
	}
	if err := usage.EnsureSchema(ctx, pool); err != nil {
		logger.Error("ensure usage schema", "err", err)
		os.Exit(1)
	}
	usageTracker := usage.New(pool, logger)
	usageTracker.Start(ctx, 60*time.Second)

	if err := audit.EnsureSchema(ctx, pool); err != nil {
		logger.Error("ensure audit schema", "err", err)
		os.Exit(1)
	}
	auditLog := audit.New(pool, logger)
	auditLog.Record(ctx, "system", "backend.boot", "", "", map[string]any{
		"instance_name": cfg.InstanceName,
	})
	// El manager registra paired events en el audit log; el endpoint
	// /api/public/stats los cuenta para "QRs Escaneados".

	cw := downstream.New(cfg.DownstreamBaseURL, cfg.DownstreamAPIToken, cfg.DownstreamAccountID)
	dedup := bridge.NewDeduper(pool, cfg.InstanceName, cfg.DedupWindowMs, cfg.DedupEnabled)

	// Declaración temprana de mgr para que resolveInbox pueda capturarlo en closure.
	var mgr *manager.Manager

	// resolveInbox: multi-tenant. Busca bridge_instance.inbox_id por nombre.
	// Si no está configurado, cae al DOWNSTREAM_INBOX_ID del env (compat single-tenant).
	resolveInbox := func(instance string) int {
		if mgr != nil {
			lookupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if id := mgr.InboxIDFor(lookupCtx, instance); id > 0 {
				return id
			}
		}
		return cfg.DownstreamInboxID
	}
	incoming := bridge.NewIncomingDynamic(cw, dedup, logger, resolveInbox)

	onMsg := func(ctx context.Context, instance string, msg *events.Message, r wameow.WAResolver) {
		incoming.Handle(ctx, instance, msg, r)
	}

	mgrInstance, err := manager.New(ctx, cfg.PostgresDSN(), pool, logger, onMsg)
	if err != nil {
		logger.Error("manager.New", "err", err)
		os.Exit(1)
	}
	mgr = mgrInstance
	if err := mgr.EnsureSchema(ctx); err != nil {
		logger.Error("manager schema", "err", err)
		os.Exit(1)
	}

	// auto-bootstrap: reconectar todas las instancias previamente persistidas.
	if err := mgr.Bootstrap(ctx); err != nil {
		logger.Error("bootstrap", "err", err)
	}
	// Anuncia backend_started a cada QR-X conv tras un breve delay (dejamos que
	// las conexiones whatsmeow se estabilicen para reportar `connected: true`).
	go func() {
		time.Sleep(8 * time.Second)
		mgr.BroadcastBackendStarted()
	}()

	// auto-crear la instancia por defecto si no existía. Idempotente.
	if cfg.InstanceName != "" {
		if _, err := mgr.Create(ctx, cfg.InstanceName); err != nil {
			logger.Error("create default instance", "name", cfg.InstanceName, "err", err)
		}
	}
	defer mgr.Shutdown()

	sgTracker := bridge.NewSpamguardTracker()
	outgoing := bridge.NewOutgoing(senderAdapter{mgr: mgr}, cw, dedup, spamguardAdapter{mgr: mgr}, sgTracker, logger)
	outgoing.SetUsage(usageTracker)
	incoming.SetUsage(usageTracker)
	mgr.SetUsage(usageTracker)
	mgr.SetAudit(auditLog)

	banWatcher := banwatch.New(banwatch.DefaultConfig(), spamguardAdapter{mgr: mgr}, logger)
	banWatcher.Start(ctx, 30*time.Second)
	outgoing.SetBanwatch(banWatcher)

	if err := outbox.EnsureSchema(ctx, pool); err != nil {
		logger.Error("ensure outbox schema", "err", err)
		os.Exit(1)
	}
	outboxQueue := outbox.New(outbox.DefaultConfig(), pool, outgoing, mgr, spamguardAdapter{mgr: mgr}, auditLog, logger)
	outboxQueue.Start(ctx)

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	// X-Migration-Id: si el cliente lo manda, se loguea en cada línea
	// para correlacionar migraciones a través de qrsgen ↔ orquestador.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if mid := c.Request().Header.Get("X-Migration-Id"); mid != "" {
				c.Set("migration_id", mid)
				c.Response().Header().Set("X-Migration-Id", mid)
			}
			return next(c)
		}
	})

	e.Static("/static", "/app/assets")

	// Prometheus metrics endpoint (sin auth — solo contadores operacionales,
	// no PII). Scrape desde Prometheus en la overlay LAN.
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	api := e.Group("/api")

	// Middleware de auth: si QRSGEN_API_TOKEN está configurado, exige
	// `Authorization: Bearer <token>` en todas las rutas /api/* EXCEPTO:
	//   - /api/health (sin auth, util para liveness)
	//   - /api/instances/:name/webhook (el downstream típicamente no manda headers de auth;
	//      sus webhooks tienen su propia firma HMAC verificable aparte)
	if cfg.APIToken != "" {
		expected := "Bearer " + cfg.APIToken
		api.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				p := c.Request().URL.Path
				if p == "/api/health" || p == "/api/public/stats" || strings.HasSuffix(p, "/webhook") {
					return next(c)
				}
				if c.Request().Header.Get("Authorization") != expected {
					logger.Warn("api auth failed",
						"path", p,
						"src", c.Request().RemoteAddr)
					return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				}
				return next(c)
			}
		})
		logger.Info("api auth enabled (QRSGEN_API_TOKEN set)")
	} else {
		logger.Warn("api auth DISABLED (QRSGEN_API_TOKEN empty) — set this env var in production")
	}

	api.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"status":    "ok",
			"instances": mgr.List(),
			"version":   "0.2.0",
			"ts":        time.Now().Format(time.RFC3339),
		})
	})

	// GET /api/public/stats — opt-in vía PUBLIC_STATS_ENABLED.
	// Endpoint sin auth pensado para landing pages estáticas que muestren
	// telemetría en vivo. CORS configurable vía PUBLIC_STATS_ALLOW_ORIGIN.
	api.GET("/public/stats", func(c echo.Context) error {
		if cfg.PublicStatsAllowOrigin != "" {
			c.Response().Header().Set(echo.HeaderAccessControlAllowOrigin, cfg.PublicStatsAllowOrigin)
			c.Response().Header().Set(echo.HeaderAccessControlAllowMethods, http.MethodGet)
		}
		if !cfg.PublicStatsEnabled {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "public stats disabled"})
		}
		var connected, total int
		for _, i := range mgr.List() {
			total++
			if i.State == "ready" || i.State == "connected" {
				connected++
			}
		}
		totals, err := usageTracker.AllTimeTotals(c.Request().Context())
		if err != nil {
			logger.Warn("public stats: totals query failed", "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "totals unavailable"})
		}
		// QRs escaneados: contador histórico de eventos paired desde el audit log
		// (sobrevive a borrado de instancias).
		var qrsScannedTotal int64
		if err := pool.QueryRow(c.Request().Context(),
			`SELECT COUNT(*) FROM bridge_audit_log WHERE action='instance.paired'`,
		).Scan(&qrsScannedTotal); err != nil {
			logger.Warn("public stats: qrs scanned query failed", "err", err)
		}
		// Instalaciones activas: instancias que tienen jid configurado (alguna
		// vez se han pareado y siguen registradas).
		var installationsActive int64
		if err := pool.QueryRow(c.Request().Context(),
			`SELECT COUNT(*) FROM bridge_instance WHERE jid IS NOT NULL AND jid <> ''`,
		).Scan(&installationsActive); err != nil {
			logger.Warn("public stats: installations active query failed", "err", err)
		}
		// Instalaciones totales históricas: cualquier instancia que haya
		// aparecido en el audit log alguna vez (incluye las ya borradas).
		// Sobrevive a DELETE — el audit log es append-only.
		var installationsTotal int64
		if err := pool.QueryRow(c.Request().Context(),
			`SELECT COUNT(DISTINCT instance) FROM bridge_audit_log WHERE instance IS NOT NULL AND instance <> ''`,
		).Scan(&installationsTotal); err != nil {
			logger.Warn("public stats: installations total query failed", "err", err)
		}
		return c.JSON(http.StatusOK, map[string]any{
			"instances_connected":  connected,
			"instances_total":      total,
			"installations_active": installationsActive,
			"installations_total":  installationsTotal,
			"qrs_scanned_total":    qrsScannedTotal,
			"messages_in_total":    totals.MessagesIn,
			"messages_out_total":   totals.MessagesOut,
			"version":              "0.23.0",
			"last_updated":         time.Now().UTC().Format(time.RFC3339),
		})
	})
	// CORS preflight para el endpoint público.
	api.OPTIONS("/public/stats", func(c echo.Context) error {
		if cfg.PublicStatsAllowOrigin != "" {
			c.Response().Header().Set(echo.HeaderAccessControlAllowOrigin, cfg.PublicStatsAllowOrigin)
			c.Response().Header().Set(echo.HeaderAccessControlAllowMethods, http.MethodGet)
			c.Response().Header().Set(echo.HeaderAccessControlMaxAge, "300")
		}
		return c.NoContent(http.StatusNoContent)
	})

	// POST /api/instances {name, events_webhook_url?, inbox_id?, owner_tag?} → crea/inicia instancia
	api.POST("/instances", func(c echo.Context) error {
		var req struct {
			Name             string  `json:"name"`
			EventsWebhookURL *string `json:"events_webhook_url,omitempty"`
			InboxID          *int    `json:"inbox_id,omitempty"`
			OwnerTag         *string `json:"owner_tag,omitempty"`
		}
		if err := c.Bind(&req); err != nil || req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing name"})
		}
		conn, err := mgr.CreateWithOpts(c.Request().Context(), req.Name, manager.CreateOpts{
			EventsWebhookURL: req.EventsWebhookURL,
			InboxID:          req.InboxID,
			OwnerTag:         req.OwnerTag,
		})
		if err != nil {
			logger.Error("create instance failed", "name", req.Name, "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		auditLog.Record(c.Request().Context(), "api", "instance.create", req.Name, "",
			map[string]any{"owner_tag_set": req.OwnerTag != nil, "inbox_id_set": req.InboxID != nil})
		return c.JSON(http.StatusOK, manager.InstanceInfo{
			Name:  conn.Name(),
			State: conn.State(),
			JID:   conn.JID(),
		})
	})

	// PATCH /api/instances/:name {inbox_id?, events_webhook_url?, spamguard_enabled?, owner_tag?} → actualiza config existente
	api.PATCH("/instances/:name", func(c echo.Context) error {
		name := c.Param("name")
		var req struct {
			EventsWebhookURL *string `json:"events_webhook_url,omitempty"`
			InboxID          *int    `json:"inbox_id,omitempty"`
			SpamguardEnabled *bool   `json:"spamguard_enabled,omitempty"`
			LastQRMsgID      *int    `json:"last_qr_msg_id,omitempty"`
			OwnerTag         *string `json:"owner_tag,omitempty"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad json"})
		}
		conn, err := mgr.CreateWithOpts(c.Request().Context(), name, manager.CreateOpts{
			EventsWebhookURL: req.EventsWebhookURL,
			InboxID:          req.InboxID,
			OwnerTag:         req.OwnerTag,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if req.SpamguardEnabled != nil {
			if err := mgr.SetSpamguard(c.Request().Context(), name, *req.SpamguardEnabled); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
		}
		if req.LastQRMsgID != nil {
			if err := mgr.SetLastQRMsgID(c.Request().Context(), name, *req.LastQRMsgID); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
		}
		sgEnabled, sgWin, sgMin := mgr.SpamguardConfig(c.Request().Context(), name)
		auditLog.Record(c.Request().Context(), "api", "instance.patch", name, "", map[string]any{
			"owner_tag_set":         req.OwnerTag != nil,
			"inbox_id_set":          req.InboxID != nil,
			"events_webhook_set":    req.EventsWebhookURL != nil,
			"spamguard_enabled_set": req.SpamguardEnabled != nil,
		})
		return c.JSON(http.StatusOK, map[string]any{
			"name":                conn.Name(),
			"state":               conn.State(),
			"jid":                 conn.JID(),
			"spamguard_enabled":   sgEnabled,
			"spamguard_window_ms": sgWin,
			"spamguard_min_chars": sgMin,
		})
	})

	api.GET("/instances", func(c echo.Context) error {
		return c.JSON(http.StatusOK, mgr.List())
	})

	api.GET("/instances/:name/state", func(c echo.Context) error {
		conn, ok := mgr.Get(c.Param("name"))
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"instance": conn.Name(),
			"state":    conn.State(),
			"jid":      conn.JID(),
		})
	})

	// GET /api/instances/:name — rich status para orquestadores.
	// Incluye spamguard_enabled + spamguard_blocks (contador acumulado in-memory).
	api.GET("/instances/:name", func(c echo.Context) error {
		name := c.Param("name")
		st, err := mgr.Status(c.Request().Context(), name)
		if errors.Is(err, manager.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		sgEnabled, _, _ := mgr.SpamguardConfig(c.Request().Context(), name)
		out := map[string]any{
			"name":              st.Name,
			"state":             st.State,
			"jid":               st.JID,
			"phone":             st.Phone,
			"qr":                st.QR,
			"created_at":        st.CreatedAt,
			"paired_at":         st.PairedAt,
			"ready_at":          st.ReadyAt,
			"last_event_at":     st.LastEventAt,
			"owner_tag":         st.OwnerTag,
			"spamguard_enabled": sgEnabled,
			"spamguard_blocks":  sgTracker.BlockCount(name),
		}
		return c.JSON(http.StatusOK, out)
	})

	// GET /api/instances/:name/usage?from=YYYY-MM-DD&to=YYYY-MM-DD
	// Devuelve filas diarias para una instancia. Si from/to no se pasan, default
	// es últimos 30 días terminando hoy (UTC).
	api.GET("/instances/:name/usage", func(c echo.Context) error {
		name := c.Param("name")
		from, to := parseUsageRange(c)
		rows, err := usageTracker.Query(c.Request().Context(), name, from, to)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"instance": name,
			"from":     from,
			"to":       to,
			"rows":     rows,
		})
	})

	// GET /api/audit?instance=&limit=
	// Append-only log con triggers en DB que rechazan UPDATE/DELETE — útil para
	// compliance y forensics. limit default 100, máximo 500.
	api.GET("/audit", func(c echo.Context) error {
		limit := 0
		if v := c.QueryParam("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		entries, err := auditLog.Query(c.Request().Context(), c.QueryParam("instance"), limit)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"entries": entries})
	})

	// GET /api/instances/:name/outbox
	// Stats del buffer de outgoing para esa instancia: pending/sent/expired/failed.
	api.GET("/instances/:name/outbox", func(c echo.Context) error {
		s, err := outboxQueue.Stats(c.Request().Context(), c.Param("name"))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, s)
	})

	// GET /api/instances/:name/ban-risk
	// Snapshot del detector de ban-risk para una instancia (velocity/diversity/
	// delivery_ratio + score + alerts activas). Útil para que el integrador
	// reduzca el ritmo antes de que WhatsApp tome acciones.
	api.GET("/instances/:name/ban-risk", func(c echo.Context) error {
		return c.JSON(http.StatusOK, banWatcher.Snapshot(c.Param("name")))
	})

	// GET /api/usage/summary?from=YYYY-MM&to=YYYY-MM
	// Resumen mensual agregado por (owner_tag, mes). Pensado para billing —
	// el integrador mapea owner_tag a su modelo de tenant y suma los contadores.
	// Defaults: últimos 3 meses naturales.
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

	// GET /api/usage?from=YYYY-MM-DD&to=YYYY-MM-DD
	// Devuelve filas diarias agregadas por (instance, day), todas las instancias.
	// Pensado para que el integrador haga billing/reporting.
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

	// POST /api/instances/bulk — crea/reusa varias. Idempotente, errores parciales NO abortan.
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

	// GET /api/instances/bulk/status?names=a,b,c — status rico para múltiples.
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

	// GET /api/instances/:name/wait-ready?timeout=180
	// Long-poll: bloquea hasta state=ready o expirar timeout. Diseñado para
	// orquestadores (n8n) que necesitan saber EXACTAMENTE cuándo conectó.
	api.GET("/instances/:name/wait-ready", func(c echo.Context) error {
		name := c.Param("name")
		timeoutSec, _ := strconv.Atoi(c.QueryParam("timeout"))
		if timeoutSec <= 0 {
			timeoutSec = 120
		}
		if timeoutSec > 600 {
			timeoutSec = 600 // hard cap
		}
		waitCtx, cancel := context.WithTimeout(c.Request().Context(), time.Duration(timeoutSec)*time.Second)
		defer cancel()
		st, err := mgr.WaitReady(waitCtx, name)
		if err != nil {
			if errors.Is(err, manager.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
			}
			if errors.Is(err, context.DeadlineExceeded) {
				current, _ := mgr.Status(c.Request().Context(), name)
				return c.JSON(http.StatusRequestTimeout, map[string]any{
					"error": "timeout waiting for ready",
					"state": current.State,
					"qr":    current.QR,
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, st)
	})

	// POST /api/instances/:name/refresh-qr — fuerza nuevo canal QR si la sesión expiró.
	api.POST("/instances/:name/refresh-qr", func(c echo.Context) error {
		if err := mgr.RefreshQR(c.Request().Context(), c.Param("name")); err != nil {
			if errors.Is(err, manager.ErrNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "qr refresh initiated"})
	})

	api.GET("/instances/:name/qr", func(c echo.Context) error {
		conn, ok := mgr.Get(c.Param("name"))
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		png := conn.LatestQR()
		if png == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "no QR available (already connected?)"})
		}
		return c.Blob(http.StatusOK, "image/png", png)
	})

	api.POST("/instances/:name/restart", func(c echo.Context) error {
		if err := mgr.Restart(c.Request().Context(), c.Param("name")); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "restarting"})
	})

	api.POST("/instances/:name/logout", func(c echo.Context) error {
		if err := mgr.Logout(c.Request().Context(), c.Param("name")); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "logged out, new QR will be generated"})
	})

	api.DELETE("/instances/:name", func(c echo.Context) error {
		name := c.Param("name")
		if err := mgr.Delete(c.Request().Context(), name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		auditLog.Record(c.Request().Context(), "api", "instance.delete", name, "", nil)
		return c.JSON(http.StatusOK, map[string]string{"message": "deleted"})
	})

	api.POST("/instances/:name/webhook", func(c echo.Context) error {
		instance := c.Param("name")

		// Leemos el body crudo: necesitamos persistirlo intacto si la instancia
		// no está conectada (el outbox lo replay-eará tal cual cuando vuelva).
		raw, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "read body"})
		}
		var payload bridge.WebhookPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad json"})
		}

		// Si la instancia está disconnected (restart en curso, sesión perdida,
		// reconectando), encolamos en outbox y devolvemos 202. El drainer
		// entregará en cuanto vuelva. Mensajes a `qrsgen-qr-*` (ops contacts)
		// se manejan en HandleFor sin tocar la red, así que NO se encolan —
		// los pasamos al camino síncrono para que la safety net actúe.
		isQrsgenOps := payload.Conversation != nil && payload.Conversation.Meta != nil &&
			payload.Conversation.Meta.Sender != nil &&
			strings.HasPrefix(payload.Conversation.Meta.Sender.Identifier, "qrsgen-qr-")
		isOutgoingForRealClient := payload.MessageType == "outgoing" && !payload.Private && !isQrsgenOps
		if isOutgoingForRealClient && !mgr.IsConnected(instance) {
			var remoteJID string
			if payload.Conversation != nil && payload.Conversation.Meta != nil && payload.Conversation.Meta.Sender != nil {
				remoteJID = payload.Conversation.Meta.Sender.Identifier
			}
			res, qerr := outboxQueue.Enqueue(c.Request().Context(), instance, remoteJID, raw)
			if qerr != nil {
				if errors.Is(qerr, outbox.ErrQueueFull) {
					return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "queue full"})
				}
				logger.Error("outbox enqueue failed, falling back to sync", "err", qerr, "instance", instance)
				// fallthrough → intent síncrono (probablemente fallará pero no perdemos información)
			} else {
				return c.JSON(http.StatusAccepted, map[string]any{
					"status":     "queued",
					"queue_id":   res.QueueID,
					"expires_at": res.ExpiresAt,
				})
			}
		}

		if err := outgoing.HandleFor(c.Request().Context(), instance, payload); err != nil {
			logger.Error("outgoing handle failed", "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal"})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "sent"})
	}, webhookHMACMiddleware(cfg.WebhookHMACSecret, logger))

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
			cancel()
		}
	}()

	logger.Info("qrsgen ready", "port", cfg.Port)
	<-ctx.Done()

	// Antes de bajar: avisamos a cada panel QR-X que estamos reiniciando.
	// Esto da contexto al agente cuando ve los pills de unreachable/reconnect
	// que vienen justo después por el restart del container.
	mgr.BroadcastBackendRestarting()

	// Damos margen para que el downstream procese los message_created y dispare sus
	// propios webhooks a bridge_bridge:3100. Sin esto, los webhooks downstream→qrsgen
	// llegan cuando ya bajamos y se marcan como "Error al enviar" en la conv.
	logger.Info("shutdown grace: waiting 12s for downstream to drain")
	time.Sleep(12 * time.Second)

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = e.Shutdown(shutdownCtx)
	logger.Info("shutdown complete")
}

// senderAdapter implementa bridge.Sender resolviendo la instancia desde el manager.
type senderAdapter struct{ mgr *manager.Manager }

func (s senderAdapter) SendText(ctx context.Context, instance, remoteJid, content string) (string, error) {
	conn, ok := s.mgr.Get(instance)
	if !ok {
		return "", fmt.Errorf("instance %q not found", instance)
	}
	return conn.SendText(ctx, remoteJid, content)
}

func (s senderAdapter) SendMedia(ctx context.Context, instance, remoteJid, kind, mimetype, filename, caption string, data []byte) (string, error) {
	conn, ok := s.mgr.Get(instance)
	if !ok {
		return "", fmt.Errorf("instance %q not found", instance)
	}
	return conn.SendMedia(ctx, remoteJid, kind, mimetype, filename, caption, data)
}

// spamguardAdapter expone al bridge la config + emisión de eventos del Manager.
type spamguardAdapter struct{ mgr *manager.Manager }

func (s spamguardAdapter) IsSpamguardEnabled(ctx context.Context, instance string) bool {
	return s.mgr.IsSpamguardEnabled(ctx, instance)
}

func (s spamguardAdapter) EmitLifecycle(name, event string, extras map[string]any) {
	s.mgr.EmitCustomLifecycle(name, event, extras)
}

// parseUsageRange reads ?from / ?to as YYYY-MM-DD (UTC). Defaults to the last
// 30 days ending today. Validates loosely — invalid input is replaced by the
// default rather than returned as 400 (this is a read-only reporting endpoint).
func parseUsageRange(c echo.Context) (from, to string) {
	const dayLayout = "2006-01-02"
	now := time.Now().UTC()
	from = c.QueryParam("from")
	to = c.QueryParam("to")
	if _, err := time.Parse(dayLayout, from); err != nil {
		from = now.AddDate(0, 0, -30).Format(dayLayout)
	}
	if _, err := time.Parse(dayLayout, to); err != nil {
		to = now.Format(dayLayout)
	}
	return from, to
}

// parseMonthRange reads ?from / ?to as YYYY-MM (UTC). Defaults to the last
// 3 calendar months ending in the current month. Invalid input is replaced
// by defaults.
func parseMonthRange(c echo.Context) (from, to string) {
	const monthLayout = "2006-01"
	now := time.Now().UTC()
	from = c.QueryParam("from")
	to = c.QueryParam("to")
	if _, err := time.Parse(monthLayout, from); err != nil {
		from = now.AddDate(0, -2, 0).Format(monthLayout)
	}
	if _, err := time.Parse(monthLayout, to); err != nil {
		to = now.Format(monthLayout)
	}
	return from, to
}

// webhookHMACMiddleware verifies X-Qrsgen-Signature: sha256=<hex> against
// HMAC-SHA256(secret, raw body). Returns 401 on mismatch. If secret is empty,
// the middleware is a no-op (backward-compat).
//
// Reads the body once, validates, then restores it so the downstream handler's
// c.Bind() still works.
func webhookHMACMiddleware(secret string, logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		if secret == "" {
			return next
		}
		secretBytes := []byte(secret)
		return func(c echo.Context) error {
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "read body"})
			}
			c.Request().Body = io.NopCloser(bytes.NewBuffer(body))

			sig := c.Request().Header.Get("X-Qrsgen-Signature")
			const prefix = "sha256="
			if !strings.HasPrefix(sig, prefix) {
				logger.Warn("webhook hmac: missing/invalid signature header", "instance", c.Param("name"))
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing signature"})
			}
			gotHex := sig[len(prefix):]
			got, err := hex.DecodeString(gotHex)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid signature encoding"})
			}

			mac := hmac.New(sha256.New, secretBytes)
			mac.Write(body)
			expected := mac.Sum(nil)

			if !hmac.Equal(got, expected) {
				logger.Warn("webhook hmac: signature mismatch", "instance", c.Param("name"))
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "signature mismatch"})
			}
			return next(c)
		}
	}
}

// runHealthcheck pings /api/health on localhost:$PORT (default 3100) and exits
// 0 if 200 OK, 1 otherwise. Designed for Docker HEALTHCHECK on distroless.
func runHealthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3100"
	}
	url := "http://127.0.0.1:" + port + "/api/health"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}

