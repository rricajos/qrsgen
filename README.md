# qrsgen

[![CI](https://github.com/rricajos/qrsgen/actions/workflows/test.yml/badge.svg)](https://github.com/rricajos/qrsgen/actions/workflows/test.yml)
[![CodeQL](https://github.com/rricajos/qrsgen/actions/workflows/codeql.yml/badge.svg)](https://github.com/rricajos/qrsgen/actions/workflows/codeql.yml)
[![Go Report](https://goreportcard.com/badge/github.com/rricajos/qrsgen)](https://goreportcard.com/report/github.com/rricajos/qrsgen)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-live-blue.svg)](https://rricajos.github.io/qrsgen/)

WhatsApp ↔ HTTP API bridge en Go. Mantiene una sesión WhatsApp Web (vía
[whatsmeow](https://github.com/tulir/whatsmeow)) por instancia y la expone
como una API HTTP REST estándar — con outbox persistido, detector de
ban-risk, audit log inmutable y usage tracking listo para facturación.

> ⚠️ **Aviso legal importante**: este proyecto **no está afiliado** con
> WhatsApp / Meta. Usa una API no oficial obtenida por ingeniería inversa.
> Lee [DISCLAIMER.md](DISCLAIMER.md) antes de desplegar — explica el riesgo
> de baneo del número, ToS, GDPR, y limitación de responsabilidad.

## Qué hace

```
Tu sistema (n8n, app custom, CRM, etc.)
        │
        │ HTTP REST + Bearer auth (+ HMAC opcional)
        ▼
     qrsgen ──── WebSocket TLS ────► Meta servers
        │
        │ HTTP POST con incoming msgs
        ▼ HTTP POST con lifecycle events
   Tu webhook endpoint
```

- **Incoming** (cliente → tu sistema): qrsgen recibe el msg por
  WebSocket, lo POSTea a tu webhook.
- **Outgoing** (tu sistema → cliente): tu sistema POSTea a qrsgen;
  qrsgen lo envía por WhatsApp.
- **Outbox**: si la instancia está disconnected cuando llega un outgoing,
  qrsgen lo encola hasta 5 min y reentrega cuando vuelve. El downstream
  recibe `202 queued` con `queue_id` en lugar de un error.
- **Lifecycle events** (conexión, QR, ban risk, outbox expirations, etc.):
  POST a webhook configurable por instancia.

## Por qué existe

- **Multi-instancia real**: un binario gestiona N números, cada uno con
  su WebSocket contra Meta.
- **Multi-tenant / multi-downstream**: cada instancia puede llevar un
  `owner_tag` que la mapea a un downstream propio (URL/token/account)
  vía la tabla `bridge_tenant`. Un solo proceso qrsgen puede servir
  varios clientes con destinos distintos. `/api/tenants/*` para CRUD.
  Si no hay tenant configurado, fallback al `DOWNSTREAM_*` del env
  (backward compat single-tenant).
- **HTTP-first**: cualquier sistema que hable HTTP integra sin SDK.
- **Persistencia robusta**: sesiones whatsmeow en Postgres → restarts no
  requieren reescanear QR.
- **Outbox 5 min TTL**: cero pérdida durante deploys o blips cortos.
- **BanWatcher proactivo**: tres señales (velocity / diversity /
  delivery ratio) y un score 0-1 + level para reducir ritmo antes de que
  WhatsApp tome medidas.
- **Audit log inmutable**: tabla con triggers que rechazan UPDATE/DELETE
  a nivel DB. Forensics tamper-evident.
- **Usage tracking persistido**: contadores diarios + agregado mensual
  por `owner_tag` → endpoint `/api/usage/summary` listo para facturación.
- **HMAC opcional** del webhook entrante. **Read-only rootfs** del
  container.
- **Lifecycle observable**: 12 eventos distintos, incluyendo
  `ban_risk`, `outgoing_expired`, `spam_blocked`.

## Integración

qrsgen es agnóstico del downstream. Como referencia,
[`docs/integrations/n8n.md`](docs/integrations/n8n.md) muestra cómo orquestar el
flujo con [n8n](https://n8n.io/), pero cualquier otro stack (Zapier, Make,
Temporal, app web propia, scripts) funciona igual — la integración es
HTTP estándar.

## Estado del proyecto

En producción con 4+ instancias activas. Tag estable más reciente: ver
[releases](https://github.com/rricajos/qrsgen/releases). El
[CHANGELOG](CHANGELOG.md) documenta cada release.

## Documentación

Toda la documentación está en https://rricajos.github.io/qrsgen/ y en
[`docs/`](docs/):

- [Architecture](docs/architecture/) — capas, flujos, persistencia, concurrencia.
- [API](docs/api/) — endpoints, payloads, lifecycle webhooks, ejemplos curl.
- [Deployment](docs/deployment/) — stack swarm + portabilidad multi-VPS + Traefik.
- [Security](docs/security/) — 7 capas (Bearer / HMAC / firewall / TLS / hardening / audit / backups).
- [Operations](docs/operations/) — runbook, métricas, troubleshooting.
- [Integrations](docs/integrations/) — recetas para n8n, Python, etc.
- [Migrations](docs/migrations/) — venir desde Evolution API / wajs / Baileys / SaaS, o salir de qrsgen.
- [Backup runbook](ops/backup/README.md) — install, restore, drop-in config.
- [Migration tools](tools/migrate/) — `bulk-provision.py` / `validate.py` / `export-config.py`.

## Stack técnico

- **Go 1.25+** + **Echo v4** (HTTP framework).
- **whatsmeow** (`go.mau.fi/whatsmeow`) — cliente WhatsApp Multi-Device.
  Sesiones en Postgres vía `sqlstore`.
- **pgx/v5** — driver Postgres.
- **slog** (stdlib) — logger JSON estructurado.
- **caarlos0/env** — parseo de env vars.
- **skip2/go-qrcode** — generación PNG del QR.
- **prometheus/client_golang** — métricas.

Imagen final ~25MB sobre `gcr.io/distroless/static-debian12:nonroot`.
Read-only rootfs + tmpfs 64 MB en `/tmp`.

## Quick start

```bash
cp .env.example .env
# editar: POSTGRES_PASSWORD, QRSGEN_API_TOKEN, DOWNSTREAM_*
docker stack deploy -c docker-compose.yml qrsgen
```

O usa la imagen pre-built desde GHCR:

```bash
docker pull ghcr.io/rricajos/qrsgen:latest
```

## Tests

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.25-alpine go test ./...
```

Tests de integración (con DB real):

```bash
INTEGRATION_PG_DSN='postgres://postgres@localhost:5432/bridge?sslmode=disable' \
docker run --rm -v "$PWD":/src -w /src --network host \
  -e INTEGRATION_PG_DSN \
  golang:1.25-alpine go test ./...
```

## Lifecycle events

qrsgen emite los siguientes eventos HTTP POST a `events_webhook_url`
(configurable por instancia):

| Evento | Cuándo | Extras |
|---|---|---|
| `qr_generated` | Nuevo QR PNG disponible (cada ~20s mientras pairing). | `last_qr_msg_id` |
| `paired` | `events.PairSuccess` tras escaneo. | – |
| `connected` | WebSocket activo con JID válido. | – |
| `reconnected` | Connected confirmado estable tras un `unreachable`. | – |
| `unreachable` | WebSocket caído (tras 60s de silencio: blip silencioso si vuelve antes). | – |
| `disconnected` | Sigue caído tras 2 min de grace. | – |
| `logged_out` | Sesión invalidada server-side. Necesita nuevo QR. | – |
| `strike` | TemporaryBan o ConnectFailure 4xx. **Acción inmediata.** | – |
| `spam_blocked` | Outgoing duplicado bloqueado. | `count`, `preview` |
| `ban_risk` | Detector cruzó threshold (velocity / diversity / delivery). | `alert`, `score`, `level`, ... |
| `outgoing_expired` | Mensaje en outbox no se entregó antes del TTL (5 min). | `queue_id`, `remote_jid`, `preview` |
| `backend_restarting` | SIGTERM recibido, shutdown iniciándose. | – |
| `backend_started` | Bootstrap completo post-restart. | – |

## Métricas Prometheus

`GET /metrics` (sin auth):

```
qrsgen_messages_total{direction="in"|"out",instance}
qrsgen_spamguard_blocks_total{instance}
qrsgen_lifecycle_events_total{instance,event}
qrsgen_message_dispatch_errors_total{direction,instance,kind}
qrsgen_active_instances
qrsgen_total_instances
```

Plus métricas estándar Go runtime (`go_*`, `process_*`).

## Releases

Tagging `vX.Y.Z` dispara GoReleaser:

- Binarios linux/darwin/windows × amd64/arm64.
- Imagen multi-arch en `ghcr.io/rricajos/qrsgen`.
- SBOMs por arquitectura.
- Cosign signatures para checksum y manifest.
- GitHub Release con notas del CHANGELOG.

## Licencia y avisos legales

- [LICENSE](LICENSE) — MIT.
- [DISCLAIMER.md](DISCLAIMER.md) — riesgos WhatsApp ToS, GDPR, limitación
  de responsabilidad.
- [NOTICE.md](NOTICE.md) — atribución a librerías de terceros y marcas.
- [SECURITY.md](SECURITY.md) — política de divulgación responsable.
- [CONTRIBUTING.md](CONTRIBUTING.md) — guía para contribuir.
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Contributor Covenant.
