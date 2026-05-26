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
  "inbox_id": 90,
  "owner_tag": "tenant-acme"
}
```

`owner_tag` (opcional) es un string libre que el integrador usa para
correlacionar la instancia con su modelo de tenants/facturación.
qrsgen no lo interpreta; aparece en `GET /api/instances/:name` y en el
agregado `GET /api/usage/summary`.

### `PATCH /api/instances/:name`

Actualiza config existente.

```json
{
  "events_webhook_url": "https://...",
  "inbox_id": 90,
  "spamguard_enabled": true,
  "last_qr_msg_id": 12345,
  "owner_tag": "tenant-acme"
}
```

Respuesta incluye el estado actual + config spamguard. Pasar `owner_tag: ""`
borra el tag previo; omitirlo lo deja intacto.

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

**Entrypoint paral downstream**. Sin auth Bearer por defecto.

Body: payload del downstream estándar (`{event, message_type, content, attachments, conversation, ...}`).

Procesado por `bridge.Outgoing.HandleFor`.

Si `WEBHOOK_HMAC_SECRET` está configurado en env, qrsgen exige el header
`X-Qrsgen-Signature: sha256=<hex>` donde `<hex>` es
`HMAC-SHA256(secret, raw body)`. Mismatches devuelven `401`. Si la env var
está vacía, el endpoint queda público (backward-compat).

### `GET /api/instances/:name/usage?from=YYYY-MM-DD&to=YYYY-MM-DD`

Filas diarias en UTC para una instancia con contadores `messages_in`,
`messages_out`, `spamguard_blocks`, `lifecycle_events`. Default: últimos
30 días.

```json
{
  "instance": "whatsapp-main",
  "from": "2026-04-26",
  "to":   "2026-05-26",
  "rows": [
    {"instance":"whatsapp-main","day":"2026-05-26","messages_in":24,"messages_out":31,"spamguard_blocks":0,"lifecycle_events":2}
  ]
}
```

### `GET /api/usage?from=YYYY-MM-DD&to=YYYY-MM-DD`

Igual que el anterior pero para todas las instancias (`rows` ordenado por
instance, day). Pensado para dashboards / exports.

### `GET /api/usage/summary?from=YYYY-MM&to=YYYY-MM`

Agregado mensual por `(owner_tag, mes)`. Default: últimos 3 meses naturales.

```json
{
  "from": "2026-03",
  "to":   "2026-05",
  "rows": [
    {
      "owner_tag": "tenant-acme",
      "month": "2026-05",
      "messages_in": 4821,
      "messages_out": 5102,
      "spamguard_blocks": 14,
      "lifecycle_events": 23,
      "active_instances": 2
    },
    {
      "owner_tag": "",
      "month": "2026-05",
      "messages_in": 18, "messages_out": 22,
      "spamguard_blocks": 0, "lifecycle_events": 1, "active_instances": 1
    }
  ]
}
```

Endpoint pensado para billing — el integrador mapea `owner_tag` a su
tenant y suma los contadores que tarifique.

### `GET /api/instances/:name/ban-risk`

Snapshot del detector proactivo. Útil para que el integrador reduzca
ritmo antes de que WhatsApp tome medidas.

```json
{
  "instance": "whatsapp-main",
  "velocity_msgs_per_window": 12, "velocity_threshold": 30,
  "diversity_unique_jids": 8,     "diversity_threshold": 20,
  "delivery_ratio": 0.97,         "delivery_samples": 30,
  "delivery_threshold": 0.7,      "delivery_min_samples": 10,
  "alerts": [],
  "score": 0.13,
  "level": "low"
}
```

Cuando un signal cruza su threshold, qrsgen emite un evento lifecycle
`ban_risk` (con el `alert` activo) sólo en el flanco de subida — no se
re-emite hasta que se haya limpiado.

### `GET /api/audit?instance=&limit=`

Append-only log de operaciones (provision, patch, delete, boot). La tabla
subyacente tiene triggers que rechazan UPDATE/DELETE; las entradas son
inmutables a nivel DB. Default: últimas 100 entradas, máximo 500.

```json
{
  "entries": [
    {
      "id": 412, "ts": "2026-05-26T08:15:33Z",
      "actor": "api", "action": "instance.patch",
      "instance": "whatsapp-main", "target": "",
      "metadata": {"owner_tag_set": true, "spamguard_enabled_set": false}
    }
  ]
}
```

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
