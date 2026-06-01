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
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/rricajos/qrsgen/internal/audit"
	"github.com/rricajos/qrsgen/internal/banwatch"
	"github.com/rricajos/qrsgen/internal/bridge"
	"github.com/rricajos/qrsgen/internal/config"
	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/errcode"
	"github.com/rricajos/qrsgen/internal/lib"
	"github.com/rricajos/qrsgen/internal/manager"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/outbox"
	"github.com/rricajos/qrsgen/internal/tenant"
	"github.com/rricajos/qrsgen/internal/usage"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// version es el tag de release, inyectado por GoReleaser via
// `-X main.version={{.Version}}`. En builds locales queda "dev".
var version = "dev"

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

	processStart := time.Now()

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

	// Multi-tenant downstream routing: cada instancia puede tener un
	// `owner_tag` que apunta a una config downstream propia (bridge_tenant).
	// Si no hay tenant para ese owner_tag (o no hay owner_tag), cae al global
	// (cw) configurado vía env DOWNSTREAM_*.
	if err := tenant.EnsureSchema(ctx, pool); err != nil {
		logger.Error("tenant schema", "err", err)
		os.Exit(1)
	}
	tenants := tenant.New(pool)
	if err := tenants.Warmup(ctx); err != nil {
		logger.Warn("tenant warmup (continuando con cache vacío)", "err", err)
	}
	dsRegistry := downstream.NewRegistry(pool, tenants, cw)

	// Declaración temprana de mgr para que resolveInbox pueda capturarlo en closure.
	var mgr *manager.Manager

	// resolveInbox: multi-tenant. Prioridad:
	//   1. bridge_tenant.downstream_inbox_id (vía owner_tag de la instancia)
	//   2. bridge_instance.inbox_id (override per-instancia)
	//   3. env DOWNSTREAM_INBOX_ID (default global, compat single-tenant)
	resolveInbox := func(instance string) int {
		lookupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if id := dsRegistry.InboxIDFor(lookupCtx, instance); id > 0 {
			return id
		}
		if mgr != nil {
			if id := mgr.InboxIDFor(lookupCtx, instance); id > 0 {
				return id
			}
		}
		return cfg.DownstreamInboxID
	}
	incoming := bridge.NewIncomingDynamic(dsRegistry, dedup, logger, resolveInbox)
	jobs := bridge.NewJobStore()
	incoming.SetGroupPrefixSender(cfg.GroupPrefixSender)
	incoming.SetGroupHeaderTTL(cfg.GroupHeaderTTL)
	incoming.SetHeaderSep(bridge.ResolveHeaderSep(cfg.GroupHeaderSep))
	incoming.SetReactionSep(bridge.ResolveHeaderSep(cfg.ReactionHeaderSep))
	incoming.SetHeaderTemplate(cfg.HeaderTemplate)
	if cfg.HistoryImportEnabled {
		incoming.EnableHistoryImport(cfg.HistoryImportDays, cfg.HistoryImportRatePerSec)
	}
	if cfg.GroupEventsEnabled {
		incoming.SetGroupEventsEnabled(true)
	}
	incoming.SetAvatarSync(cfg.AvatarSync)
	incoming.SetAvatarRefreshTTL(cfg.AvatarRefreshTTL)
	incoming.SetReactionsSync(cfg.ReactionsSync)
	incoming.SetTypingSync(cfg.TypingSync)
	incoming.SetReadReceiptsSync(cfg.ReadReceiptsSync)

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
	// `BroadcastBackendStarted` se dispara MÁS ABAJO, después de que el HTTP
	// server esté listening, para evitar la race en la que Chatwoot trata de
	// dispatchar el body del pill antes de que aceptemos POSTs (failed body).

	// auto-crear la instancia por defecto si no existía. Idempotente.
	if cfg.InstanceName != "" {
		if _, err := mgr.Create(ctx, cfg.InstanceName); err != nil {
			logger.Error("create default instance", "name", cfg.InstanceName, "err", err)
		}
	}
	defer mgr.Shutdown()

	sgTracker := bridge.NewSpamguardTracker()
	// Spamguard persistence (v0.28.0): historial sobrevive a restart.
	if err := bridge.EnsureSpamguardSchema(ctx, pool); err != nil {
		logger.Error("spamguard schema", "err", err)
		os.Exit(1)
	}
	sgTracker.SetPool(pool, logger)
	if err := sgTracker.Warmup(ctx); err != nil {
		logger.Warn("spamguard warmup (continuando con cache vacío)", "err", err)
	}
	// Cleanup cron: cada 30 min borra filas con updated_at > 1h.
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := sgTracker.CleanupOldRecent(cleanCtx, time.Hour); err != nil {
					logger.Warn("spamguard cleanup", "err", err)
				}
				cancel()
			}
		}
	}()
	outgoing := bridge.NewOutgoing(senderAdapter{mgr: mgr}, dsRegistry, dedup, spamguardAdapter{mgr: mgr}, sgTracker, logger)
	outgoing.SetUsage(usageTracker)
	// v0.39.0: mark-as-read outgoing. Activamos el tracker compartido
	// entre Incoming (registra WAIDs) y Outgoing (los drena cuando
	// llega el evento conversation_updated del downstream).
	if cfg.MarkAsReadOutgoing {
		waids := incoming.EnableMarkAsRead()
		outgoing.EnableMarkAsRead(waids, senderAdapter{mgr: mgr})
	}
	incoming.SetUsage(usageTracker)
	mgr.SetUsage(usageTracker)
	mgr.SetAudit(auditLog)
	mgr.SetOwnerTagResolver(dsRegistry)
	mgr.SetVersion(version)
	// v0.31.2: real-time avatar refresh. Cuando whatsmeow emite Picture
	// (cambio de foto), forzar el re-sync del contact correspondiente.
	mgr.SetPictureHandler(func(ctx context.Context, instance string, jid types.JID, pictureID string, removed bool, r wameow.WAResolver) {
		incoming.HandlePictureChange(ctx, instance, jid, pictureID, removed, r)
	})
	// v0.34.0: typing indicators. Cuando whatsmeow emite ChatPresence
	// (composing/paused), propagar al downstream via toggle_typing_status.
	mgr.SetChatPresenceHandler(func(ctx context.Context, instance string, chat types.JID, sender types.JID, composing bool, media string, r wameow.WAResolver) {
		incoming.HandleChatPresence(ctx, instance, chat, sender, composing, media, r)
	})
	// v0.34.1: read receipts. Cuando whatsmeow emite Receipt con Type=read,
	// actualizar contact_last_seen_at de la conv en el downstream.
	mgr.SetReceiptHandler(func(ctx context.Context, instance string, chat types.JID, sender types.JID, kind string, messageIDs []string, ts time.Time, r wameow.WAResolver) {
		incoming.HandleReceipt(ctx, instance, chat, sender, kind, messageIDs, ts, r)
	})
	// v0.47.0: group events (info changes, join, identity change)
	// renderizados como activity msgs en la conv del grupo/1:1.
	if cfg.GroupEventsEnabled {
		mgr.SetGroupInfoHandler(func(ctx context.Context, instance string, evt *events.GroupInfo, r wameow.WAResolver) {
			incoming.HandleGroupInfo(ctx, instance, evt, r)
		})
		mgr.SetJoinedGroupHandler(func(ctx context.Context, instance string, evt *events.JoinedGroup, r wameow.WAResolver) {
			incoming.HandleJoinedGroup(ctx, instance, evt, r)
		})
		mgr.SetIdentityChangeHandler(func(ctx context.Context, instance string, evt *events.IdentityChange, r wameow.WAResolver) {
			incoming.HandleIdentityChange(ctx, instance, evt, r)
		})
	}

	// v0.46.0: history import. Cuando whatsmeow emite HistorySync
	// (al parear o como respuesta on-demand), Incoming.HandleHistorySync
	// procesa el blob y postea los msgs al downstream con created_at
	// backdated. Opt-in vía QRSGEN_HISTORY_IMPORT_ENABLED.
	if cfg.HistoryImportEnabled {
		mgr.SetHistorySyncHandler(func(ctx context.Context, instance string, data *waHistorySync.HistorySync, r wameow.WAResolver) {
			incoming.HandleHistorySync(ctx, instance, data, r)
		})
	}

	// v0.40.0: retroactive name update. Cuando whatsmeow emite Contact
	// (contacto añadido/editado en la agenda local del dueño), reescribir
	// el content de los mensajes históricos posteados al downstream para
	// que reflejen el nuevo nombre / sin tilde.
	// v0.49.0: chat_anchor tracker — registra el último incoming por
	// chat para que history import on-demand tenga anchor en todos los
	// chats activos (no solo los con prefix de grupo).
	if err := bridge.EnsureChatAnchorSchema(ctx, pool); err != nil {
		logger.Error("ensure chat_anchor schema", "err", err)
		os.Exit(1)
	}
	incoming.SetChatAnchorPool(pool, logger)
	// keep últimos 30 días — más allá WhatsApp ya no devuelve histórico
	// anyway, no merece tener anchors.
	if err := incoming.WarmupChatAnchor(ctx, 30*24*time.Hour); err != nil {
		logger.Warn("chat_anchor warmup", "err", err)
	}
	// Cleanup cron: cada 12h borra anchors > 30 días.
	go func() {
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if n, err := incoming.CleanupChatAnchorOld(cleanCtx, 30*24*time.Hour); err != nil {
					logger.Warn("chat_anchor cleanup", "err", err)
				} else if n > 0 {
					logger.Info("chat_anchor cleanup", "deleted", n)
				}
				cancel()
			}
		}
	}()

	if cfg.RetroactiveNameUpdate {
		incoming.EnableRetroactiveNameUpdate(cfg.RetroactiveCapPerSender)
		// v0.41.0: persistencia opcional. Si está activada, el histórico
		// sobrevive a restart.
		if cfg.RetroactivePersist {
			if err := bridge.EnsureMsgHistorySchema(ctx, pool); err != nil {
				logger.Error("ensure msg_history schema", "err", err)
				os.Exit(1)
			}
			incoming.SetRetroactivePool(pool, logger)
			if err := incoming.WarmupRetroactive(ctx, cfg.RetroactiveTTL); err != nil {
				logger.Warn("msg_history warmup (continuando con cache vacío)", "err", err)
			}
			// Cleanup cron: cada 6h borra entries con posted_at > TTL.
			go func() {
				ticker := time.NewTicker(6 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						if n, err := incoming.CleanupRetroactiveOld(cleanCtx, cfg.RetroactiveTTL); err != nil {
							logger.Warn("msg_history cleanup", "err", err)
						} else if n > 0 {
							logger.Info("msg_history cleanup", "deleted", n)
						}
						cancel()
					}
				}
			}()
		}
		mgr.SetContactHandler(func(ctx context.Context, instance string, jid types.JID, fullName, firstName string, fromFullSync bool, r wameow.WAResolver) {
			incoming.HandleContactUpdate(ctx, instance, jid, fullName, firstName, fromFullSync, r)
		})
		// v0.44.0: el outgoing reusa el msg_history tracker para resolver
		// Chatwoot msgID → WAID cuando el agente quote-replea desde el
		// composer (content_attributes.in_reply_to en el webhook).
		outgoing.EnableReplyToOutgoing(incoming)
	}
	metrics.VersionInfo.WithLabelValues(version).Set(1)

	banWatcher := banwatch.New(banwatch.DefaultConfig(), spamguardAdapter{mgr: mgr}, logger)
	banWatcher.Start(ctx, 30*time.Second)
	outgoing.SetBanwatch(banWatcher)

	if err := outbox.EnsureSchema(ctx, pool); err != nil {
		logger.Error("ensure outbox schema", "err", err)
		os.Exit(1)
	}
	outboxQueue := outbox.New(outbox.DefaultConfig(), pool, outgoing, mgr, spamguardAdapter{mgr: mgr}, auditLog, logger)
	if cfg.OutboxEncryptionKey != "" {
		key, err := outbox.DecodeEncryptionKey(cfg.OutboxEncryptionKey)
		if err != nil {
			logger.Error("outbox encryption key", "err", err)
			os.Exit(1)
		}
		if err := outboxQueue.SetEncryptionKey(key); err != nil {
			logger.Error("outbox set key", "err", err)
			os.Exit(1)
		}
		logger.Info("outbox encryption enabled (AES-256-GCM)")
	}
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
		ctx := c.Request().Context()
		now := time.Now()

		// DB liveness con timeout corto.
		dbCtx, dbCancel := context.WithTimeout(ctx, 2*time.Second)
		dbStart := time.Now()
		var dbOK bool
		if err := pool.Ping(dbCtx); err == nil {
			dbOK = true
		}
		dbCancel()
		dbLatencyMs := time.Since(dbStart).Milliseconds()

		// Snapshot de instancias y outbox.
		instances := mgr.List()
		var connected int
		for _, i := range instances {
			if i.State == "ready" || i.State == "connected" {
				connected++
			}
		}

		// Outbox total pending across all instances.
		var outboxPending int
		outboxCtx, oc := context.WithTimeout(ctx, 2*time.Second)
		_ = pool.QueryRow(outboxCtx,
			`SELECT COUNT(*) FROM bridge_outgoing_queue WHERE status='pending'`,
		).Scan(&outboxPending)
		oc()

		status := "ok"
		code := http.StatusOK
		if !dbOK {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}

		return c.JSON(code, map[string]any{
			"status":         status,
			"version":        version,
			"ts":             now.UTC().Format(time.RFC3339),
			"uptime_seconds": int64(time.Since(processStart).Seconds()),
			"checks": map[string]any{
				"db":                  map[string]any{"ok": dbOK, "latency_ms": dbLatencyMs},
				"instances_connected": connected,
				"instances_total":     len(instances),
				"outbox_pending":      outboxPending,
			},
			"instances": instances,
		})
	})

	// GET /api/public/stats — opt-in vía PUBLIC_STATS_ENABLED.
	// Endpoint sin auth pensado para landing pages estáticas que muestren
	// telemetría en vivo. CORS configurable vía PUBLIC_STATS_ALLOW_ORIGIN.
	//
	// Cache 30s in-memory: el landing hace polling cada 10s, no necesitamos
	// hit-DB en cada request. Reduce ~95% de queries sin sacrificar frescura.
	const publicStatsCacheTTL = 30 * time.Second
	var (
		publicStatsCacheMu  sync.Mutex
		publicStatsCacheBuf map[string]any
		publicStatsCacheExp time.Time
	)
	api.GET("/public/stats", func(c echo.Context) error {
		if cfg.PublicStatsAllowOrigin != "" {
			c.Response().Header().Set(echo.HeaderAccessControlAllowOrigin, cfg.PublicStatsAllowOrigin)
			c.Response().Header().Set(echo.HeaderAccessControlAllowMethods, http.MethodGet)
		}
		if !cfg.PublicStatsEnabled {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "public stats disabled"})
		}
		// Cache hit
		publicStatsCacheMu.Lock()
		if publicStatsCacheBuf != nil && time.Now().Before(publicStatsCacheExp) {
			payload := publicStatsCacheBuf
			publicStatsCacheMu.Unlock()
			return c.JSON(http.StatusOK, payload)
		}
		publicStatsCacheMu.Unlock()

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
		// Tenants configurados: filas en bridge_tenant. Refleja cuántos clientes
		// multi-downstream tienen config propia. Si la tabla aún no existe
		// (deploy nuevo previo a tenant.EnsureSchema), reportamos 0.
		var tenantsTotal int64
		if err := pool.QueryRow(c.Request().Context(),
			`SELECT COUNT(*) FROM bridge_tenant`,
		).Scan(&tenantsTotal); err != nil {
			logger.Warn("public stats: tenants total query failed", "err", err)
		}
		payload := map[string]any{
			"instances_connected":  connected,
			"instances_total":      total,
			"installations_active": installationsActive,
			"installations_total":  installationsTotal,
			"tenants_total":        tenantsTotal,
			"qrs_scanned_total":    qrsScannedTotal,
			"messages_in_total":    totals.MessagesIn,
			"messages_out_total":   totals.MessagesOut,
			"version":              version,
			"last_updated":         time.Now().UTC().Format(time.RFC3339),
		}
		// Guarda en cache para la próxima request dentro del TTL.
		publicStatsCacheMu.Lock()
		publicStatsCacheBuf = payload
		publicStatsCacheExp = time.Now().Add(publicStatsCacheTTL)
		publicStatsCacheMu.Unlock()
		return c.JSON(http.StatusOK, payload)
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
		entries, err := auditLog.QueryFiltered(c.Request().Context(),
			c.QueryParam("instance"), c.QueryParam("owner_tag"), limit)
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

	// POST /api/instances/:name/history/import-all-async
	// v0.52.0: variante async del bulk history import. Devuelve
	// inmediatamente con `job_id`; cliente sondea GET /jobs/:id para
	// progreso/resultado. Pensado para inboxes grandes (>100 contactos)
	// que pueden tardar varios minutos.
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

	// GET /api/jobs/:id
	// v0.52.0: estado + resultado de un job async.
	api.GET("/jobs/:id", func(c echo.Context) error {
		id := c.Param("id")
		job, ok := jobs.Get(id)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "job not found"})
		}
		return c.JSON(http.StatusOK, job)
	})

	// GET /api/jobs
	// v0.52.0: lista de jobs vivos (no purgados). Útil para debug.
	api.GET("/jobs", func(c echo.Context) error {
		return c.JSON(http.StatusOK, jobs.List())
	})

	// POST /api/instances/:name/history/import-all
	// v0.46.1: bulk import — itera TODOS los contactos del inbox de la
	// instancia y dispara on-demand history sync para cada uno.
	// Funciona sobre instancia ya conectada — NO requiere desconectar
	// ni re-parear. Útil para backfillear toda una agenda al adoptar
	// la feature.
	//
	// Query params:
	//   - count_per_chat=N     (opcional, default 50)
	//   - timeout_per_chat=N   (opcional, default 30 sec)
	//
	// Secuencial: procesa chat tras chat para no estresar al phone.
	// Bloquea hasta terminar — para inboxes grandes puede tardar
	// minutos. Considera ejecutarlo con un cliente que tolere
	// timeouts largos (curl -m 600 por ejemplo).
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

	// POST /api/instances/:name/history/import
	// v0.46.0: trigger on-demand history sync para un chat específico.
	// Query params:
	//   - chat=<jid>           (requerido) chat JID a importar
	//   - count=N              (opcional, default 50, max 200)
	//   - timeout_sec=N        (opcional, default 30)
	//
	// Bloquea hasta recibir el HistorySync ON_DEMAND del phone (con
	// timeout) o devuelve error. Devuelve HistoryImportResult JSON.
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
		result, err := incoming.ImportHistoryOnDemand(
			c.Request().Context(),
			instance,
			chatJID,
			count,
			time.Duration(timeoutSec)*time.Second,
			conn,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, result)
	})

	// GET /api/instances/:name/groups/:jid
	// v0.48.0: información del grupo (subject, topic, settings,
	// participantes con sus roles). Round-trip al server WA.
	api.GET("/instances/:name/groups/:jid", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid jid: " + err.Error()})
		}
		if jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "jid must be a group (@g.us)"})
		}
		info, err := conn.GroupInfo(c.Request().Context(), jid)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, info)
	})

	// POST /api/instances/:name/groups/:jid/name
	// v0.48.0: cambia el nombre (subject) del grupo. Body: {"name":"X"}.
	// Requiere que el bot sea admin del grupo.
	api.POST("/instances/:name/groups/:jid/name", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name required"})
		}
		if err := conn.SetGroupName(c.Request().Context(), jid, body.Name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "name": body.Name})
	})

	// POST /api/instances/:name/groups/:jid/participants
	// v0.48.0: añadir/expulsar/promover/degradar miembros del grupo.
	// Body: {"action":"add|remove|promote|demote", "jids":["34...@s.whatsapp.net", ...]}.
	// Requiere que el bot sea admin.
	api.POST("/instances/:name/groups/:jid/participants", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Action string   `json:"action"`
			JIDs   []string `json:"jids"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if len(body.JIDs) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "jids required"})
		}
		parsed := make([]types.JID, 0, len(body.JIDs))
		for _, s := range body.JIDs {
			p, err := types.ParseJID(s)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid participant jid: " + s})
			}
			parsed = append(parsed, p)
		}
		if err := conn.UpdateGroupParticipants(c.Request().Context(), jid, body.Action, parsed); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"jid":    jid.String(),
			"action": body.Action,
			"count":  len(parsed),
		})
	})

	// POST /api/instances/:name/groups/:jid/topic
	// v0.50.0: cambia el topic (descripción) del grupo. Body: {"topic":"X"}.
	// topic vacío = quitar descripción. Requiere bot admin.
	api.POST("/instances/:name/groups/:jid/topic", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Topic string `json:"topic"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if err := conn.SetGroupTopic(c.Request().Context(), jid, body.Topic); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "topic": body.Topic})
	})

	// POST /api/instances/:name/groups/:jid/locked
	// v0.50.0: toggle "solo admins editan info del grupo".
	// Body: {"locked": true|false}. Requiere bot admin.
	api.POST("/instances/:name/groups/:jid/locked", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Locked bool `json:"locked"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if err := conn.SetGroupLocked(c.Request().Context(), jid, body.Locked); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "locked": body.Locked})
	})

	// POST /api/instances/:name/groups/:jid/announce
	// v0.50.0: toggle "modo anuncio" (solo admins envían msgs).
	// Body: {"announce": true|false}. Requiere bot admin.
	api.POST("/instances/:name/groups/:jid/announce", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Announce bool `json:"announce"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if err := conn.SetGroupAnnounce(c.Request().Context(), jid, body.Announce); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "announce": body.Announce})
	})

	// POST /api/instances/:name/groups
	// v0.50.0: crear grupo nuevo. Body: {"name": "X", "participants":
	// ["34...@s.whatsapp.net", ...]}. Max 25 chars en name.
	api.POST("/instances/:name/groups", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		var body struct {
			Name         string   `json:"name"`
			Participants []string `json:"participants"`
		}
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name required"})
		}
		if len(body.Participants) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "participants required"})
		}
		parsed := make([]types.JID, 0, len(body.Participants))
		for _, s := range body.Participants {
			p, err := types.ParseJID(s)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid participant: " + s})
			}
			parsed = append(parsed, p)
		}
		groupJID, err := conn.CreateGroup(c.Request().Context(), body.Name, parsed)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]any{
			"jid":  groupJID,
			"name": body.Name,
		})
	})

	// DELETE /api/instances/:name/groups/:jid
	// v0.50.0: el bot abandona el grupo.
	api.DELETE("/instances/:name/groups/:jid", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		if err := conn.LeaveGroup(c.Request().Context(), jid); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "left": true})
	})

	// POST /api/instances/:name/retroactive/reconcile
	// Bulk reconcile (v0.43.0): itera el contact store local de whatsmeow
	// y dispara HandleContactUpdate por cada saved. Útil tras adoptar
	// v0.40.0+ por primera vez, o si el agente nota contactos renombrados
	// en WhatsApp que no se han propagado a Chatwoot.
	//
	// Devuelve {instance, scanned, triggered}. Las goroutines en vuelo
	// se rastrean via WaitRetroactivePatches; el endpoint NO espera —
	// devuelve cuando todas se han disparado, no cuando todas terminan.
	api.POST("/instances/:name/retroactive/reconcile", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		result, err := incoming.ReconcileSavedContacts(c.Request().Context(), instance, conn)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, result)
	})

	// POST /api/instances/:name/avatars/resync
	// Backfill bulk: itera todos los contactos del inbox de la instancia y
	// dispara avatar sync para cada uno con identifier parseable como JID.
	// Bypassea el tracker — fuerza re-chequeo aunque el TTL no haya expirado.
	// Útil tras adoptar v0.31.0+ por primera vez, para llevar la mejora a
	// contactos viejos que no han recibido mensajes recientes.
	api.POST("/instances/:name/avatars/resync", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		inboxID := resolveInbox(instance)
		if inboxID <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "no inbox_id for instance"})
		}
		result, err := incoming.ResyncInstanceAvatars(c.Request().Context(), instance, conn, inboxID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, result)
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

	// Multi-tenant downstream routing
	// ───────────────────────────────────────────────────────────────────────
	// `bridge_tenant` mapea owner_tag → config downstream (URL/token/account/inbox).
	// El campo `downstream_api_token` JAMÁS se devuelve en GET — solo se escribe.

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
			// Spamguard block → 422 para que Chatwoot marque el mensaje como
			// failed (icono rojo) en lugar de sent (verde). El agente ve
			// inmediatamente que su mensaje no se entregó.
			if errors.Is(err, bridge.ErrSpamguardBlocked) {
				return c.JSON(http.StatusUnprocessableEntity, map[string]any{
					"error_code": errcode.SpamguardBlocked,
					"error":      errcode.HumanText(errcode.SpamguardBlocked),
					"status":     "blocked",
				})
			}
			logger.Error("outgoing handle failed", "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error_code": errcode.Internal,
				"error":      errcode.HumanText(errcode.Internal),
			})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "sent"})
	}, webhookHMACMiddleware(cfg.WebhookHMACSecret, func(ctx context.Context, instance string) string {
		// Lookup per-tenant secret: instance → owner_tag → tenant.WebhookHMACSecret.
		ownerTag := dsRegistry.OwnerTagFor(ctx, instance)
		if ownerTag == "" {
			return ""
		}
		cfg, err := tenants.Get(ctx, ownerTag)
		if err != nil {
			return ""
		}
		return cfg.WebhookHMACSecret
	}, logger))

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
			cancel()
		}
	}()

	// Una vez el HTTP server está listening, anunciamos backend_started. Esto
	// evita el "Error al enviar" rojo en el body del pill: Chatwoot dispara
	// el webhook de vuelta al instante, y necesita encontrar qrsgen escuchando.
	go func() {
		if !waitForHTTPReady(ctx, cfg.Port, 5*time.Second) {
			logger.Warn("backend_started broadcast skipped: HTTP server not ready in time")
			return
		}
		mgr.BroadcastBackendStarted()
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

	// v0.40.1: esperar a que terminen las goroutines de retroactive
	// name update (si alguna en vuelo). Sin esto, un PATCH a medio
	// volar se interrumpe al cerrar el echo server.
	incoming.WaitRetroactivePatches()

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

func (s senderAdapter) SendTextReply(ctx context.Context, instance, remoteJid, content, quotedWAID, quotedSenderJID, quotedText string) (string, error) {
	conn, ok := s.mgr.Get(instance)
	if !ok {
		return "", fmt.Errorf("instance %q not found", instance)
	}
	return conn.SendTextReply(ctx, remoteJid, content, quotedWAID, quotedSenderJID, quotedText)
}

func (s senderAdapter) SendMediaReply(ctx context.Context, instance, remoteJid, kind, mimetype, filename, caption string, data []byte, quotedWAID, quotedSenderJID, quotedText string) (string, error) {
	conn, ok := s.mgr.Get(instance)
	if !ok {
		return "", fmt.Errorf("instance %q not found", instance)
	}
	return conn.SendMediaReply(ctx, remoteJid, kind, mimetype, filename, caption, data, quotedWAID, quotedSenderJID, quotedText)
}

// MarkRead implementa bridge.ReadMarker para que el outgoing pueda
// disparar el read receipt de WhatsApp tras recibir conversation_updated.
func (s senderAdapter) MarkRead(ctx context.Context, instance, chat, sender string, messageIDs []string, ts time.Time) error {
	conn, ok := s.mgr.Get(instance)
	if !ok {
		return fmt.Errorf("instance %q not found", instance)
	}
	return conn.MarkRead(ctx, chat, sender, messageIDs, ts)
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

// tenantHMACSecretLookup resuelve el HMAC secret per-tenant para una instancia.
// Devuelve "" si no hay tenant configurado o no tiene secret propio (el caller
// debe entonces caer al global).
type tenantHMACSecretLookup func(ctx context.Context, instance string) string

// webhookHMACMiddleware verifies X-Qrsgen-Signature: sha256=<hex> against
// HMAC-SHA256(secret, raw body). Returns 401 on mismatch.
//
// Resolución del secret (desde v0.26.0):
//  1. Si la instancia tiene tenant con `webhook_hmac_secret` set → usa ese.
//  2. Si no, fallback al `WEBHOOK_HMAC_SECRET` global del env.
//  3. Si ambos vacíos → middleware no-op (backward compat sin auth).
//
// Reads the body once, validates, then restores it so the downstream handler's
// c.Bind() still works.
func webhookHMACMiddleware(globalSecret string, lookup tenantHMACSecretLookup, logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "read body"})
			}
			c.Request().Body = io.NopCloser(bytes.NewBuffer(body))

			// Resolver secret efectivo: per-tenant si existe, fallback global.
			instance := c.Param("name")
			secret := ""
			if lookup != nil {
				secret = lookup(c.Request().Context(), instance)
			}
			if secret == "" {
				secret = globalSecret
			}
			// Si ningún secret está set, dejar pasar (backward compat sin HMAC).
			if secret == "" {
				return next(c)
			}

			sig := c.Request().Header.Get("X-Qrsgen-Signature")
			const prefix = "sha256="
			if !strings.HasPrefix(sig, prefix) {
				logger.Warn("webhook hmac: missing/invalid signature header", "instance", instance)
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing signature"})
			}
			gotHex := sig[len(prefix):]
			got, err := hex.DecodeString(gotHex)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid signature encoding"})
			}

			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			expected := mac.Sum(nil)

			if !hmac.Equal(got, expected) {
				logger.Warn("webhook hmac: signature mismatch", "instance", instance)
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "signature mismatch"})
			}
			return next(c)
		}
	}
}

// runHealthcheck pings /api/health on localhost:$PORT (default 3100) and exits
// 0 if 200 OK, 1 otherwise. Designed for Docker HEALTHCHECK on distroless.
// waitForHTTPReady hace polling al /api/health propio hasta que responda
// (200 ó 503 — ambos significan que el server está aceptando POSTs) o se
// agote el timeout. Devuelve false si ctx cancela o timeout expira.
//
// Usado tras arrancar `e.Start()` para asegurar que estamos accept-ready
// antes de emitir backend_started.
func waitForHTTPReady(ctx context.Context, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			// 200 = sano, 503 = DB degradada pero HTTP server vivo. Ambos OK.
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusServiceUnavailable {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func runHealthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3100"
	}
	url := "http://127.0.0.1:" + port + "/api/health"
	client := &http.Client{Timeout: 3 * time.Second}
	// URL es constante de localhost, no tainted input — gosec G704 false positive.
	resp, err := client.Get(url) //nolint:gosec
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
