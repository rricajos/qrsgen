# Changelog

Todos los cambios notables se documentan aquí. Sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) y [SemVer](https://semver.org/).

## [Unreleased]

## [0.25.0] - 2026-05-27

Observabilidad per-tenant: cuando un proceso qrsgen sirve varios
clientes vía `owner_tag`, ahora puedes separar métricas y entradas de
audit por tenant sin tener que cruzar contra DB.

### Added

- **Label `owner_tag`** en los counters Prometheus per-instance:
  `qrsgen_messages_total`, `qrsgen_spamguard_blocks_total`,
  `qrsgen_lifecycle_events_total`, `qrsgen_message_dispatch_errors_total`.
  Vacío para instancias sin tenant configurado. Las queries existentes
  sin filtrar por `owner_tag` siguen funcionando (Prometheus agrega).
- **`Router.OwnerTagFor(ctx, instance)`** nuevo método en la interfaz —
  `*Client` devuelve `""` (single-downstream), `*Registry` resuelve
  desde el cache TTL existente. Permite a callsites de métricas
  etiquetar sin nuevos lookups DB.
- **`GET /api/audit?owner_tag=tenant-acme`** — filtro nuevo en el
  endpoint de audit. Subquery sobre `bridge_instance.owner_tag`. Si se
  combina con `?instance=X` el AND es lógico. Sin filtro = todo.
  Permite que un admin de tenant solo vea sus entradas sin acceso al
  resto del audit log.

### Changed

- `audit.Logger.Query` mantiene firma original; nueva `QueryFiltered`
  acepta el segundo filtro opcional. `Query` delega en `QueryFiltered`
  con `ownerTag=""` para backward compat.
- Manager expone `SetOwnerTagResolver(r)` para inyectar el `Registry`
  (o cualquier impl de la interface). Sin él, el label sale como `""`.

## [0.24.2] - 2026-05-27

Bug fix de "🎉 espurio" + robustez en `backend_started` + cache 30s
del endpoint público + timestamps en tenant API.

### Fixed

- **`connected` espurio tras EventConnected duplicado de whatsmeow**:
  el dispatcher ahora trackea `connectedEmitted` per-instance. Mientras
  la sesión esté viva, EventConnected adicionales (re-handshake silent,
  session renewal sin Disconnect intermedio) **no re-emiten** el evento
  `connected` (que en n8n se renderiza como "Conexión establecida 🎉").
  El flag se limpia en `disconnect` / `logged_out`, dejando que la
  próxima sesión emita normalmente. Caso reportado: SAT-MARC mostrando
  🎉 a las 5:17 AM sin desconexión previa visible.

### Changed

- **`BroadcastBackendStarted` ahora espera a `state=ready`** (timeout
  20s) por instancia antes de reportar `connected: true`. Sustituye al
  snapshot fijo de `IsConnected()` a los 8s post-boot, que daba falsos
  negativos cuando WhatsApp negociaba handshake lento. El pre-sleep de
  8s en `cmd/server` queda eliminado (la espera vive en el broadcast).
- **`/api/public/stats` cacheado in-memory 30s**: la landing hace
  polling cada 10s; sin cache hacíamos 5 SELECTs por request. Ahorra
  ~95% de hits a DB sin sacrificar frescura.
- **`tenant.Resolver.{List,Get,Warmup,Set}` devuelven `created_at` y
  `updated_at`**: visibles vía `/api/tenants*` para UIs de admin. El
  `PUT` los recibe del `RETURNING` del upsert y los persiste en cache.

## [0.24.1] - 2026-05-27

Fix de `version` hardcodeada y simetría con multi-downstream en el
endpoint público.

### Added

- **`tenants_total`** en `/api/public/stats` — cuenta filas en
  `bridge_tenant`. Completa la familia `instances_total` /
  `installations_total` / `tenants_total`.

### Fixed

- **`/api/health` y `/api/public/stats` ahora reportan la versión real
  de cada release**. Antes devolvían `"0.23.0"` hardcoded sin importar
  el tag. Ahora la inyectamos en build via
  `-X main.version={{.Version}}` (GoReleaser). Builds locales reportan
  `"dev"`.

## [0.24.0] - 2026-05-27

Multi-downstream real: un solo proceso puede servir varios clientes con
config downstream propia por `owner_tag`, manteniendo backward compat
total con el fallback `DOWNSTREAM_*` del env.

### Added

- **Multi-downstream real** (`internal/tenant` + `internal/downstream/registry.go`):
  un proceso qrsgen puede servir varios downstreams distintos, enrutados
  por `bridge_instance.owner_tag` vía la nueva tabla `bridge_tenant`
  (owner_tag PK + downstream URL/token/account/inbox). Cache in-memory per
  tenant con invalidación en cada upsert/delete; instance→owner_tag con
  TTL 30s. Si una instancia no tiene `owner_tag`, o no hay tenant para él,
  cae al fallback global (`DOWNSTREAM_*` del env) — totalmente backward
  compatible.
- **Endpoints `/api/tenants/*`**: `GET` (list/detail sin tokens),
  `PUT /:owner_tag` (upsert con `downstream_api_token` solo de escritura),
  `DELETE /:owner_tag` (instancias caen al fallback global). Cada cambio
  invalida el cache del `*Client` por tenant.

### Changed

- `bridge.Incoming` y `bridge.Outgoing` dependen del nuevo interface
  `downstream.Router` en lugar de `*downstream.Client`. `*Client` lo
  implementa devolviéndose a sí mismo, así el callsite single-downstream
  no necesita ningún cambio.
- `resolveInbox` (incoming flow) ahora prioriza
  `bridge_tenant.downstream_inbox_id` antes que el per-instance y el env.

## [0.23.0] - 2026-05-26

Robustez producción + capa de monetización ligera + cero pérdida en
restarts + telemetría pública opt-in + 64 sub-páginas docs.

### Added (post rc1)

- **Lifecycle webhook retry exponencial** para eventos críticos
  (`strike`, `ban_risk`, `outgoing_expired`, `logged_out`,
  `spam_blocked`, `backend_restarting`). 3 reintentos async con
  backoff 5s → 30s → 5min. Métrica
  `qrsgen_lifecycle_webhook_retries_total{event,outcome}` para
  alerting (`outcome=exhausted` → el downstream lleva ≥5min caído).
- **Health endpoint enriquecido**: `/api/health` ahora hace DB ping
  (timeout 2s), reporta `outbox_pending`, `uptime_seconds`,
  `checks.db.{ok, latency_ms}`. Devuelve `503` con
  `status: "degraded"` si DB no responde → Docker HEALTHCHECK falla
  → Swarm restart automático.
- **`installations_total`** en `/api/public/stats` (simetría con
  `instances_total`). Cuenta DISTINCT instances en el audit log
  → sobrevive a DELETE.
- **`instance.paired` registrado en audit log**: ahora cada pairing
  exitoso queda como entrada en `bridge_audit_log`. La métrica
  `qrs_scanned_total` del endpoint público lo cuenta.
- **Cards live en la landing** (`docs/home/status.md`): 6 cards
  conectadas a `/api/public/stats` con polling 10s y toggle
  on/off persistido en localStorage. Fetch con AbortController
  (timeout 3s) para no bloquear UI si el endpoint público está
  caído.
- **Documentación masivamente ampliada**:
  - 64 sub-páginas en sidebar Material (estructura por dominio).
  - Sección **Migrations** con 7 páginas (Evolution / wajs / Baileys
    / SaaS overview / Whapi.cloud / MaytAPI / Salir de qrsgen).
    Schemas de origen detallados en cada una.
  - Sección **Integrations** con n8n + Python (cliente httpx
    + FastAPI receiver).
  - Glosario al final de cada sub-página (>50 términos técnicos
    explicados consistentemente).
- **`tools/migrate/`**: `bulk-provision.py`, `validate.py`,
  `export-config.py` (Python httpx) para automatizar migraciones
  desde plataformas existentes.

### Added (en rc1, inalterado)

- **Outbox persistido** (`internal/outbox` + tabla `bridge_outgoing_queue`).
  El endpoint `POST /api/instances/:name/webhook` ahora encola el payload
  cuando la instancia no está conectada y devuelve `202 {status:"queued"}`.
  Un drainer reentrega cada 5s; mensajes sin entregar a los 5 min expiran
  y emiten el evento lifecycle `outgoing_expired`. Per-instance backlog
  hard-cap (200) + retry budget (5 attempts) + audit hooks.

### Added

- **BanWatcher** (`internal/banwatch` + `GET /api/instances/:name/ban-risk`):
  detector proactivo con tres señales (velocity / diversity / delivery
  ratio) sobre ventanas rolling, score 0-1 + nivel ok|low|moderate|high.
  Emite el evento lifecycle `ban_risk` en rising edge para que el
  integrador reduzca el ritmo.
- **Usage tracking persistido** (`internal/usage` + `bridge_usage_daily`):
  contadores diarios in/out + spamguard + lifecycle, flush a Postgres
  cada 60s. Endpoints `/api/instances/:name/usage` (por instancia),
  `/api/usage` (todas) y `/api/usage/summary` (agregado mensual por
  `owner_tag` — el campo principal para billing).
- **Audit log inmutable** (`internal/audit` + `bridge_audit_log`). Tabla
  append-only con triggers plpgsql que rechazan `UPDATE` y `DELETE`.
  Hooks en provision / patch / delete / boot / outbox.{enqueue,expire,failed}.
  Endpoint `GET /api/audit?instance=&limit=`.
- **`owner_tag` en `bridge_instance`**: string libre que el integrador
  usa para correlacionar instancias con su modelo de tenants. qrsgen no
  lo interpreta. Aceptado en `POST /api/instances` y `PATCH …`, devuelto
  en `GET …`, agrupado en `/api/usage/summary`.
- **HMAC opcional** del webhook entrante (`WEBHOOK_HMAC_SECRET`). Cuando
  está set, exige `X-Qrsgen-Signature: sha256=<hex>` calculado como
  HMAC-SHA256(secret, raw body). Mismatches devuelven 401. Cuando está
  vacío, comportamiento previo (backward compat).
- **HEALTHCHECK** en `Dockerfile` y `Dockerfile.release` vía flag
  `-healthcheck` del propio binario (distroless friendly, no necesita
  curl). Interval 30s, timeout 5s, start-period 20s.
- **Backups Postgres** automatizados: script `ops/backup/postgres-backup.sh`
  + systemd timer (`qrsgen-postgres-backup.timer`) — daily 03:00, retention
  7 daily + 4 weekly, restore runbook en `ops/backup/README.md`.
- **Read-only rootfs** del container + tmpfs `/tmp` 64 MB en compose. El
  binario no escribe a disco; cualquier mutación del fs es signal de
  compromiso.
- **Lifecycle suavizado** para evitar paneles ruidosos:
  - 60s de silencio antes de emitir `unreachable` (blip silencioso si
    reconecta en ese rango).
  - 5s de estabilidad antes de emitir `reconnected`.
- Tests: `internal/config` (100%), `internal/downstream` (79%),
  `internal/banwatch` (95%), `internal/usage`, `internal/audit`
  (integration gated por `INTEGRATION_PG_DSN`). Tests HMAC dedicados.
- Documentación: API completa para todos los endpoints nuevos,
  arquitectura reescrita reflejando outbox / banwatch / audit / usage,
  sección hardening en security.md.

### Changed

- Migración a APIs no-deprecadas de whatsmeow: `DownloadAny` ahora rutea
  por `client.Download` con `DownloadableMessage`; `simpleTextMessage`
  devuelve `*waE2E.Message` (drop `binary/proto`). `SA1019` re-activado en
  golangci-lint.
- `errorlint` reactivado: 5 comparaciones `err == sentinel` migradas a
  `errors.Is`.
- Naming genérico en docs y ejemplos: `SAT-XXX` → `whatsapp-main`,
  `SAT-ALBERT` → `whatsapp-sales`, `<INSTANCE_NAME>` como placeholder.
- `update_config.order: stop-first` en compose (previene race de dos
  containers compitiendo por el mismo JID WhatsApp durante deploy — a
  cambio de ~15s downtime; el outbox lo cubre).

### Security

- HMAC verify opcional del webhook entrante.
- Read-only rootfs + tmpfs `/tmp` 64 MB.
- Audit log inmutable a nivel DB.
- Dependencias actualizadas: `github.com/jackc/pgx/v5` 5.7.1→5.9.2 (cierra
  GHSA-9jj7-4m8r-rfcm critical + GHSA-j88v-2chj-qfwx low),
  `filippo.io/edwards25519` 1.1.0→1.1.1 (GHSA-fw7p-63qq-7hpr),
  `github.com/caarlos0/env/v11` 11.2.2→11.4.1, Echo v4 (CVE
  GO-2025-3553 en `golang-jwt/jwt v3` transitivo cerrado).
- Workflows actualizados: `actions/setup-go@v6`, `docker/login-action@v4`,
  `goreleaser/goreleaser-action@v7`, golang docker 1.25 → 1.26.

### Known limitations

- Outbox payloads sin cifrado en disco — para multi-tenant serio
  se debería cifrar por tenant.
- Single downstream por proceso aún — usar `owner_tag` + un proceso
  por downstream como workaround.
- BanWatcher per-process (no comparte estado entre réplicas — qrsgen
  corre `replicas: 1` por diseño así que no es problema en producción
  típica).

## [0.21.0] - 2026-05-22

Primera release pública.

### Added

- Multi-instance orchestrator: un binario gestiona N sesiones WhatsApp.
- Lifecycle events: `qr_generated`, `paired`, `connected`, `reconnected`, `unreachable`, `disconnected`, `logged_out`, `strike`, `spam_blocked`, `backend_restarting`, `backend_started`.
- Bridge incoming/outgoing con formato `Channel::Api`-compatible.
- Spamguard: tracker in-memory de últimos 2 mensajes por (instance, jid) — bloquea duplicados back-to-back.
- LID/PN twin dedup con ventana configurable (default 10s).
- Bearer auth (`QRSGEN_API_TOKEN`) protege endpoints administrativos. `/api/instances/:name/webhook` exento.
- Egress firewall script + watcher systemd: allowlist Meta CIDRs + LAN, DROP el resto.
- Métricas Prometheus en `/metrics`: messages_total, spamguard_blocks_total, lifecycle_events_total, message_dispatch_errors_total, active_instances, total_instances.
- Healthcheck `/api/health` (sin auth) para liveness/readiness probes.
- Idempotencia de outgoing por `chatwoot_msg_id` (vía `Deduper.SeenIncomingMsg`).
- Stack Docker Compose portable con env vars + ejemplo `.env.example`.
- Documentación: architecture, api, deployment, security, operations, n8n-example.
- 12 unit tests en `internal/bridge` (spamguard tracker, normalize, hashContent).

### Security

- Auth Bearer en API.
- Egress filtering vía iptables.
- Distroless container image.

### Known limitations

- Multi-tenant aún no soportado: un proceso qrsgen sirve un solo downstream.
- Spamguard counter in-memory: se resetea en cada restart.
- LID twin del cliente: dedup limpia downstream pero el destinatario sigue recibiendo 2 msgs si WhatsApp hace dispatch dual.

[Unreleased]: https://github.com/rricajos/qrsgen/compare/v0.25.0...HEAD
[0.25.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.25.0
[0.24.2]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.2
[0.24.1]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.1
[0.24.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.0
[0.23.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.23.0
[0.21.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.21.0
