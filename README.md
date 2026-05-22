# qrsgen

WhatsApp ↔ HTTP API bridge en Go. Mantiene una sesión WhatsApp Web (vía [whatsmeow](https://github.com/tulir/whatsmeow)) por instancia y la expone como una API HTTP estándar.

> ⚠️ **Aviso legal importante**: este proyecto **no está afiliado** con WhatsApp / Meta. Usa una API no oficial obtenida por ingeniería inversa. Lee [DISCLAIMER.md](DISCLAIMER.md) antes de desplegar — explica el riesgo de baneo del número, ToS, GDPR, y la limitación de responsabilidad.

## Qué hace

Convierte el protocolo binario de WhatsApp Multi-Device en HTTP estándar bidireccional:

```
Tu sistema (n8n, app custom, CRM, etc.)
        │
        │ HTTP REST + Bearer auth
        ▼
     qrsgen ──── WebSocket TLS ────► Meta servers
        │
        │ HTTP POST con incoming msgs
        ▼
   Tu webhook endpoint
```

- **Incoming** (cliente → tu sistema): qrsgen recibe el msg por WebSocket, lo POSTea a tu webhook configurado.
- **Outgoing** (tu sistema → cliente): tu sistema POSTea a qrsgen, qrsgen lo envía por WhatsApp.
- **Lifecycle events** (conexión, QR generado, baneos, etc.): POST a webhook configurable, una entrada por instancia.

## Por qué existe

- **Multi-instancia real**: un binario gestiona N números independientes, cada uno con su WebSocket contra Meta.
- **HTTP-first**: cualquier sistema que hable HTTP puede integrarse (sin SDKs por cliente, sin dependencias raras).
- **Persistencia robusta**: sessions whatsmeow en Postgres → restarts del backend no requieren reescanear QR.
- **Lifecycle observable**: ~11 eventos distintos (qr_generated, connected, reconnected, unreachable, disconnected, logged_out, strike, spam_blocked, etc.) emitidos como webhooks.
- **Dedup robusto** del problema LID/PN twin del protocolo Multi-Device.

## Integración

qrsgen es agnóstico de tu stack downstream. Como inspiración, puedes usar **[n8n](https://n8n.io/)** (open-source workflow automation) para orquestar:

- Recibir lifecycle webhooks de qrsgen y postearlos a tu app/CRM/dashboard
- Recibir mensajes entrantes y enrutarlos por reglas
- Llamar a la API qrsgen para provisionar nuevas instancias, hacer toggle de spamguard, etc.

Ver [`docs/n8n-example.md`](docs/n8n-example.md) para una referencia. Cualquier otro orquestador (Zapier, Make, Temporal, scripts Python/Bash, una app web propia) sirve igual — la integración es HTTP estándar.

## Estado del proyecto

Producción con 4+ instancias activas. Lifecycle, spamguard, métricas Prometheus, Bearer auth y egress firewall operativos.

## Documentación

- [docs/architecture.md](docs/architecture.md) — arquitectura interna (capas, flujos, persistencia, concurrencia)
- [docs/deployment.md](docs/deployment.md) — stack swarm + portabilidad multi-VPS
- [docs/api.md](docs/api.md) — endpoints HTTP, autenticación, payload schemas
- [docs/security.md](docs/security.md) — Bearer auth + egress firewall iptables
- [docs/operations.md](docs/operations.md) — runbook, métricas, troubleshooting
- [docs/n8n-example.md](docs/n8n-example.md) — ejemplo de integración con n8n

## Stack técnico

- **Go 1.25** + **Echo v4** (HTTP framework)
- **whatsmeow** (`go.mau.fi/whatsmeow`) — cliente WhatsApp Multi-Device. Sessions en Postgres vía `sqlstore`.
- **pgx/v5** — driver Postgres
- **slog** (stdlib) — logger JSON estructurado
- **caarlos0/env** — parseo de env vars
- **skip2/go-qrcode** — generación PNG del QR
- **prometheus/client_golang** — métricas

Binario ~25MB sobre `distroless/static`.

## Quick start

```bash
cp .env.example .env
# editar: POSTGRES_PASSWORD, QRSGEN_API_TOKEN
docker stack deploy -c docker-compose.yml qrsgen
```

Tests:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.25-alpine go test ./...
```

## Eventos de ciclo de vida

qrsgen emite eventos HTTP POST a `events_webhook_url` (configurable per-instancia):

| Evento | Cuándo |
|---|---|
| `qr_generated` | Nuevo QR PNG disponible (cada ~20s mientras pairing) |
| `paired` | `events.PairSuccess` recibido tras escaneo |
| `connected` | WebSocket activo con JID válido |
| `reconnected` | Connected tras un `unreachable` previo confirmado |
| `unreachable` | WebSocket caído (inmediato, antes del grace) |
| `disconnected` | Sigue caído tras 2 min de grace period |
| `logged_out` | Sesión invalidada server-side, requiere nuevo QR |
| `strike` | Ban temporal o ConnectFailure 401/403/405 |
| `spam_blocked` | Outgoing duplicado bloqueado por spamguard |
| `backend_restarting` | Shutdown gracioso iniciado |
| `backend_started` | Bootstrap completo |

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

## Licencia y avisos legales

- [LICENSE](LICENSE) — MIT
- [DISCLAIMER.md](DISCLAIMER.md) — riesgos WhatsApp ToS, GDPR, limitación de responsabilidad
- [NOTICE.md](NOTICE.md) — atribución a librerías de terceros y marcas registradas
