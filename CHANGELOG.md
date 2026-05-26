# Changelog

Todos los cambios notables se documentan aquí. Sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) y [SemVer](https://semver.org/).

## [Unreleased]

## [0.23.0] - 2026-05-26

Robustez producción + capa de monetización ligera.

### Added

- **Outbox persistido** (`internal/outbox` + tabla `bridge_outgoing_queue`).
  El endpoint `POST /api/instances/:name/webhook` ahora encola el payload
  cuando la instancia no está conectada y devuelve `202 {status:"queued"}`.
  Un drainer reentrega cada 5s; mensajes sin entregar a los 5 min expiran
  y emiten el evento lifecycle `outgoing_expired`. Per-instance backlog
  hard-cap (200) + retry budget (5 attempts) + audit hooks.
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

[Unreleased]: https://github.com/rricajos/qrsgen/compare/v0.23.0...HEAD
[0.23.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.23.0
[0.21.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.21.0
