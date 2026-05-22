// Command server arranca el servicio qrsgen.
package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rricajos/qrsgen/internal/bridge"
	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/config"
	"github.com/rricajos/qrsgen/internal/lib"
	"github.com/rricajos/qrsgen/internal/manager"
	"github.com/rricajos/qrsgen/internal/wameow"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.mau.fi/whatsmeow/types/events"
)

func main() {
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
				if p == "/api/health" || strings.HasSuffix(p, "/webhook") {
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

	// POST /api/instances {name, events_webhook_url?, inbox_id?} → crea/inicia instancia
	api.POST("/instances", func(c echo.Context) error {
		var req struct {
			Name             string  `json:"name"`
			EventsWebhookURL *string `json:"events_webhook_url,omitempty"`
			InboxID          *int    `json:"inbox_id,omitempty"`
		}
		if err := c.Bind(&req); err != nil || req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing name"})
		}
		conn, err := mgr.CreateWithOpts(c.Request().Context(), req.Name, manager.CreateOpts{
			EventsWebhookURL: req.EventsWebhookURL,
			InboxID:          req.InboxID,
		})
		if err != nil {
			logger.Error("create instance failed", "name", req.Name, "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, manager.InstanceInfo{
			Name:  conn.Name(),
			State: conn.State(),
			JID:   conn.JID(),
		})
	})

	// PATCH /api/instances/:name {inbox_id?, events_webhook_url?, spamguard_enabled?} → actualiza config existente
	api.PATCH("/instances/:name", func(c echo.Context) error {
		name := c.Param("name")
		var req struct {
			EventsWebhookURL *string `json:"events_webhook_url,omitempty"`
			InboxID          *int    `json:"inbox_id,omitempty"`
			SpamguardEnabled *bool   `json:"spamguard_enabled,omitempty"`
			LastQRMsgID      *int    `json:"last_qr_msg_id,omitempty"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad json"})
		}
		conn, err := mgr.CreateWithOpts(c.Request().Context(), name, manager.CreateOpts{
			EventsWebhookURL: req.EventsWebhookURL,
			InboxID:          req.InboxID,
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
		if err == manager.ErrNotFound {
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
			"spamguard_enabled": sgEnabled,
			"spamguard_blocks":  sgTracker.BlockCount(name),
		}
		return c.JSON(http.StatusOK, out)
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
			if err == manager.ErrNotFound {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
			}
			if err == context.DeadlineExceeded {
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
			if err == manager.ErrNotFound {
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
		if err := mgr.Delete(c.Request().Context(), c.Param("name")); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "deleted"})
	})

	api.POST("/instances/:name/webhook", func(c echo.Context) error {
		var payload bridge.WebhookPayload
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad json"})
		}
		if err := outgoing.HandleFor(c.Request().Context(), c.Param("name"), payload); err != nil {
			logger.Error("outgoing handle failed", "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal"})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "ok"})
	})

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
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

