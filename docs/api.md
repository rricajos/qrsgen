# API HTTP

qrsgen expone un servidor Echo en `:3100`. No tiene DNS público — solo accesible desde la overlay LAN del swarm (alias `qrsgen` o `bridge_bridge`).

## Autenticación

Casi todos los endpoints requieren `Authorization: Bearer <QRSGEN_API_TOKEN>` (var env). Los endpoints exentos son:

- `GET /api/health` — liveness
- `POST /api/instances/:name/webhook` — entrypoint del webhook downstream
- `GET /metrics` — Prometheus scrape
- `GET /static/*` — assets estáticos (brand-asset.png)

Si `QRSGEN_API_TOKEN` está vacío, no se exige auth (backward-compat para dev).

## Endpoints

### `GET /api/health`

Liveness check sin auth.

```json
{"status":"ok","instances":[...],"version":"0.2.0","ts":"2026-05-22T11:30:00Z"}
```

### `POST /api/instances`

Crea/reusa una instancia.

```json
{
  "name": "whatsapp-main",
  "events_webhook_url": "https://workflows.example.com/webhook/qrsgen-events",
  "inbox_id": 90
}
```

### `PATCH /api/instances/:name`

Actualiza config existente.

```json
{
  "events_webhook_url": "https://...",
  "inbox_id": 90,
  "spamguard_enabled": true,
  "last_qr_msg_id": 12345
}
```

Respuesta incluye el estado actual + config spamguard.

### `GET /api/instances`

Lista todas las instancias en memoria con estado simple.

```json
[{"name":"whatsapp-main","state":"ready","jid":"34650367855:28@s.whatsapp.net"}]
```

Side-effect: actualiza gauges Prometheus `qrsgen_active_instances` + `qrsgen_total_instances`.

### `GET /api/instances/:name`

Estado rico para orquestadores (n8n, etc.).

```json
{
  "name": "whatsapp-main",
  "state": "ready",
  "jid": "34650367855:28@s.whatsapp.net",
  "phone": "34650367855",
  "qr": {"available": false},
  "created_at": "...", "paired_at": "...", "ready_at": "...", "last_event_at": "...",
  "spamguard_enabled": false,
  "spamguard_blocks": 0
}
```

### `GET /api/instances/:name/state`

Versión mínima (solo `instance`, `state`, `jid`).

### `GET /api/instances/:name/qr`

PNG bytes del QR actual. Devuelve 404 si no hay QR disponible (instancia ya conectada o aún arrancando).

### `GET /api/instances/:name/wait-ready?timeout=180`

Long-poll. Bloquea hasta que la instancia llega a `ready` o expira el timeout (segundos).

### `POST /api/instances/:name/refresh-qr`

Fuerza regeneración del canal QR.

### `POST /api/instances/:name/restart`

Cierra y re-abre la conexión.

### `POST /api/instances/:name/logout`

Invalida la sesión server-side. El siguiente uso requiere nuevo QR.

### `DELETE /api/instances/:name`

Para la instancia y borra `bridge_instance` row.

### `POST /api/instances/bulk`

```json
{"names":["whatsapp-main","whatsapp-sales"]}
```

Crea/reusa varias. Idempotente.

### `GET /api/instances/bulk/status`

Estado de todas las instancias en una request.

### `POST /api/instances/:name/webhook`

**Entrypoint paral downstream**. Sin auth Bearer.

Body: payload del downstream estándar (`{event, message_type, content, attachments, conversation, ...}`).

Procesado por `bridge.Outgoing.HandleFor`.

### `GET /metrics`

Prometheus metrics, sin auth.

```
qrsgen_messages_total{direction,instance}
qrsgen_spamguard_blocks_total{instance}
qrsgen_lifecycle_events_total{instance,event}
qrsgen_message_dispatch_errors_total{direction,instance,kind}
qrsgen_active_instances
qrsgen_total_instances
```

Plus métricas estándar de Go runtime (`go_*`, `process_*`).

### `GET /static/brand-asset.png`

Asset estático (ej. avatar). Útil si el downstream necesita descargar un PNG por URL para asociar a un contacto.

## Headers comunes

- `X-Migration-Id` — si lo manda el cliente, se propaga a logs y a la response para correlacionar trazas.

## Códigos de error

- `200 OK` — éxito
- `400 Bad Request` — JSON inválido
- `401 Unauthorized` — Bearer token incorrecto o ausente
- `404 Not Found` — instancia no existe
- `500 Internal Server Error` — fallo DB / whatsmeow / downstream
