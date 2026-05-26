# API HTTP

qrsgen expone una API REST en `:3100` sobre la overlay LAN (alias `qrsgen` o
`bridge_bridge`). No tiene DNS público — todo acceso pasa por containers
del mismo overlay (n8n, tu app, etc.).

## Quickstart

End-to-end desde cero hasta el primer mensaje enviado:

```bash
TOK="$QRSGEN_API_TOKEN"
BASE="http://qrsgen:3100"

# 1. Provisionar una instancia.
curl -sS -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -X POST "$BASE/api/instances" \
  -d '{"name":"whatsapp-main","events_webhook_url":"https://wf.example.com/qrsgen-events","inbox_id":90,"owner_tag":"tenant-acme"}'

# 2. Esperar a que aparezca el QR (long-poll).
curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/whatsapp-main/wait-ready?timeout=120"

# 3. Mientras el state == qr_pending, descargar el PNG y mostrarlo al usuario.
curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/whatsapp-main/qr" -o /tmp/qr.png

# 4. Cuando el usuario escanea, el state pasa a "ready" automáticamente.
#    Verificar:
curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/whatsapp-main" | jq '.state'

# 5. Enviar un mensaje (formato Channel::Api-compatible).
curl -sS -H 'Content-Type: application/json' \
  -X POST "$BASE/api/instances/whatsapp-main/webhook" \
  -d '{
    "event":"message_created","message_type":"outgoing","content":"Hola",
    "conversation":{"id":1,"meta":{"sender":{"identifier":"34600000000@s.whatsapp.net"}}},
    "id":42,"private":false
  }'
```

Si `whatsapp-main` está conectada → `200 {"status":"sent"}`.
Si está reconectando → `202 {"status":"queued","queue_id":N,"expires_at":"..."}` y el
outbox la entrega cuando vuelva.

## Convenciones

### Base URL

`http://qrsgen:3100` desde dentro de la overlay docker. Aliases válidos:
`qrsgen` o `bridge_bridge`.

### Autenticación

Casi todos los endpoints requieren `Authorization: Bearer <QRSGEN_API_TOKEN>`.
Exentos:

| Endpoint | Por qué |
|---|---|
| `GET /api/health` | Liveness / readiness probes. |
| `POST /api/instances/:name/webhook` | El downstream rara vez manda headers arbitrarios; usa HMAC en otro header si necesitas autenticación (ver más abajo). |
| `GET /metrics` | Prometheus scrape. |
| `GET /static/*` | Assets públicos. |

Si `QRSGEN_API_TOKEN` está vacío en env, todos los endpoints quedan abiertos
(modo dev — un WARNING aparece al boot).

### HMAC del webhook entrante (opcional)

Cuando `WEBHOOK_HMAC_SECRET` está set en env, `POST /api/instances/:name/webhook`
exige `X-Qrsgen-Signature: sha256=<hex>` donde
`<hex> = HMAC-SHA256(secret, raw_body)`. Mismatches devuelven `401`. Si la
env var está vacía, el endpoint queda abierto en LAN.

### Headers comunes

- `Authorization: Bearer <token>` — auth (ver arriba).
- `Content-Type: application/json` — requests con body JSON.
- `X-Qrsgen-Signature: sha256=<hex>` — sólo en webhook si HMAC activo.
- `X-Migration-Id: <id>` — opcional, se propaga a logs y response para
  correlacionar trazas entre tu orquestador y qrsgen.

### Códigos de error genéricos

| Código | Significado |
|---|---|
| `200 OK` | Operación síncrona completada. |
| `202 Accepted` | Outgoing encolado en outbox (instancia disconnected). |
| `400 Bad Request` | JSON inválido o body malformado. |
| `401 Unauthorized` | Bearer token incorrecto / ausente, o HMAC mismatch. |
| `404 Not Found` | Instancia no existe (o no se encontró el recurso). |
| `408 Request Timeout` | Solo en `wait-ready` cuando expira el timeout. |
| `500 Internal Server Error` | Fallo DB / whatsmeow / downstream. |
| `503 Service Unavailable` | Queue de outbox llena para esa instancia (MAX 200). |

Cada endpoint puede añadir códigos específicos — los anoto en su sección.

---

## Endpoints — Instancias

### `POST /api/instances`

Crea (o reusa, idempotente) una instancia.

**Request:**
```json
{
  "name": "whatsapp-main",
  "events_webhook_url": "https://workflows.example.com/qrsgen-events",
  "inbox_id": 90,
  "owner_tag": "tenant-acme"
}
```

Campos:

| Campo | Tipo | Requerido | Descripción |
|---|---|---|---|
| `name` | string | ✓ | Identificador único de la instancia. Aparece en todas las URLs. Usa nombres descriptivos (`whatsapp-main`, `whatsapp-sales`). |
| `events_webhook_url` | string | – | URL donde qrsgen POSTea lifecycle events (ver [esquema](#lifecycle-webhooks)). |
| `inbox_id` | int | – | ID arbitrario para routing downstream. qrsgen lo propaga en el payload de incoming msgs. |
| `owner_tag` | string | – | String libre para correlación tenant ↔ instancia. Aparece en `/api/usage/summary`. |

**Response 200:**
```json
{"name":"whatsapp-main","state":"qr_pending","jid":""}
```

**Estados posibles** (`state`):

| Estado | Significado |
|---|---|
| `qr_pending` | Esperando escaneo. PNG disponible en `/qr`. |
| `paired` | Escaneado. Esperando primer `Connected` event de whatsmeow. |
| `ready` | Listo para enviar/recibir mensajes. |
| `disconnected` | Sesión perdida. Necesita reconexión o nuevo QR. |
| `connecting` | Reconectando tras restart o blip. |

**Códigos posibles:** `200`, `400` (missing `name`), `500`.

---

### `GET /api/instances`

Lista todas las instancias con estado simple. Side-effect: actualiza gauges
Prometheus `qrsgen_active_instances` + `qrsgen_total_instances`.

**Response 200:**
```json
[
  {"name":"whatsapp-main","state":"ready","jid":"34650367855:28@s.whatsapp.net"},
  {"name":"whatsapp-sales","state":"qr_pending"}
]
```

---

### `GET /api/instances/:name`

Estado rico para orquestadores (incluye timestamps, owner_tag, spamguard).

**Response 200:**
```json
{
  "name": "whatsapp-main",
  "state": "ready",
  "jid": "34650367855:28@s.whatsapp.net",
  "phone": "34650367855",
  "qr": {"available": false},
  "created_at": "2026-05-01T09:00:00Z",
  "paired_at":  "2026-05-01T09:00:42Z",
  "ready_at":   "2026-05-01T09:00:45Z",
  "last_event_at": "2026-05-26T08:20:11Z",
  "owner_tag": "tenant-acme",
  "spamguard_enabled": false,
  "spamguard_blocks": 0
}
```

**Códigos posibles:** `200`, `404`.

---

### `GET /api/instances/:name/state`

Versión mínima — sólo `instance`, `state`, `jid`. Útil para polling barato.

```json
{"instance":"whatsapp-main","state":"ready","jid":"34650367855:28@s.whatsapp.net"}
```

---

### `PATCH /api/instances/:name`

Actualiza configuración existente. Campos `null`/omitidos no se tocan.

**Request:**
```json
{
  "events_webhook_url": "https://nueva-url",
  "inbox_id": 91,
  "spamguard_enabled": true,
  "last_qr_msg_id": 12345,
  "owner_tag": "tenant-acme"
}
```

Pasar `owner_tag: ""` (string vacío) **borra** el tag previo. Omitirlo lo deja
intacto. Misma semántica para los demás campos opcionales.

**Response 200:**
```json
{
  "name": "whatsapp-main",
  "state": "ready",
  "jid": "34650367855:28@s.whatsapp.net",
  "spamguard_enabled": true,
  "spamguard_window_ms": 30000,
  "spamguard_min_chars": 10
}
```

---

### `DELETE /api/instances/:name`

Para la instancia y elimina la fila de `bridge_instance`. **No** elimina las
tablas `whatsmeow_*` asociadas — la sesión queda invalida pero los keys
permanecen (limpieza manual si los necesitas borrar).

**Response 200:** `{"message":"deleted"}`. Quedará registrado en
`bridge_audit_log` como `instance.delete`.

---

### `POST /api/instances/bulk`

Crea/reusa varias instancias en una sola request. Idempotente. Errores
parciales NO abortan la operación.

**Request:**
```json
{"names":["whatsapp-main","whatsapp-sales","whatsapp-support"]}
```

**Response 200:**
```json
[
  {"name":"whatsapp-main","state":"ready","jid":"..."},
  {"name":"whatsapp-sales","state":"qr_pending"},
  {"name":"whatsapp-support","state":"qr_pending"}
]
```

---

### `GET /api/instances/bulk/status`

Estado rico de todas las instancias en una sola request. Equivalente a
hacer `/api/instances/:name` por cada nombre en `/api/instances` pero en
una sola consulta.

---

## Endpoints — QR y ciclo de vida

### `GET /api/instances/:name/qr`

Devuelve el PNG bytes del QR actual.

**Response 200:** binario `image/png`.
**Response 404:** la instancia no tiene QR pendiente (ya está `ready` o aún
no inició el pairing).

---

### `GET /api/instances/:name/wait-ready?timeout=180`

Long-poll. Bloquea hasta que la instancia llega a `ready` o expira el
timeout (segundos, máximo 600).

**Response 200:** ver `GET /api/instances/:name`.
**Response 404:** instancia no existe.
**Response 408:** timeout expirado. Body incluye estado actual + QR si está
disponible.

---

### `POST /api/instances/:name/refresh-qr`

Fuerza la regeneración del canal QR — útil cuando el QR caducó (20s) y
quieres uno fresco sin esperar.

**Response 200:** `{"message":"qr refresh initiated"}`.

---

### `POST /api/instances/:name/restart`

Cierra y re-abre la conexión whatsmeow. Útil si la instancia está en un
estado raro pero la sesión no se ha perdido.

---

### `POST /api/instances/:name/logout`

Invalida la sesión a nivel server-side (Meta). El siguiente uso requiere
nuevo QR. Distinto de `DELETE` — el row de `bridge_instance` permanece.

---

## Endpoints — Mensajes (downstream → WhatsApp)

### `POST /api/instances/:name/webhook`

**Entrypoint** del downstream para enviar un mensaje. Sin auth Bearer por
defecto; HMAC opcional vía `WEBHOOK_HMAC_SECRET`.

**Request — WebhookPayload:**

```json
{
  "event": "message_created",
  "id": 12345,
  "message_type": "outgoing",
  "private": false,
  "content": "Hola, ¿cómo estás?",
  "source_id": "",
  "attachments": [
    {
      "id": 1,
      "file_type": "image",
      "data_url": "https://downstream.example.com/rails/.../foto.jpg",
      "extension": "jpg",
      "file_size": 84720,
      "file_name": "foto.jpg"
    }
  ],
  "conversation": {
    "id": 7,
    "inbox_id": 90,
    "meta": {
      "sender": {
        "phone_number": "+34600000000",
        "identifier": "34600000000@s.whatsapp.net"
      }
    }
  }
}
```

Campos:

| Campo | Tipo | Significado |
|---|---|---|
| `event` | string | Siempre `"message_created"`. Reservado para futuras extensiones. |
| `id` | int | Id del mensaje en TU sistema. Sirve de idempotencia: si POSTeas dos veces el mismo `id`, qrsgen lo dedup-ea. |
| `message_type` | string | `"outgoing"` para enviar al cliente. Otros valores (`incoming`, `activity`, `template`) se ignoran. |
| `private` | bool | Si `true`, NO se envía a WhatsApp (es nota interna del agente). |
| `content` | string | Texto. Si hay `attachments`, va como caption del primero. |
| `source_id` | string | Si empieza con `"WAID:"` se considera echo y se ignora (evita re-envíos). |
| `attachments` | array | Adjuntos. qrsgen descarga `data_url` y los envía como media (image/audio/video/document). |
| `conversation.id` | int | Id de la conversación. qrsgen lo usa para PATCH `source_id="WAID:..."` post-envío. |
| `conversation.meta.sender.identifier` | string | JID del destinatario (`<phone>@s.whatsapp.net` o `<lid>@lid`). **Requerido**. |

**Response 200** (instancia conectada, mensaje entregado a WhatsApp):
```json
{"status":"sent"}
```

**Response 202** (instancia disconnected, encolado para retry):
```json
{
  "status": "queued",
  "queue_id": 7421,
  "expires_at": "2026-05-26T09:13:42Z"
}
```

El outbox reintentará cada 5s mientras la instancia no esté conectada.
A los 5 min (TTL default), el mensaje expira y se emite el evento
lifecycle `outgoing_expired`.

**Otros códigos:**
- `400` — JSON inválido.
- `401` — HMAC signature mismatch (si `WEBHOOK_HMAC_SECRET` activo).
- `500` — fallo whatsmeow / downstream blob download.
- `503` — queue llena (>200 pending para la instancia).

**Safety nets aplicadas** antes de despachar a whatsmeow:
- Si `message_type != "outgoing"` → no-op (200 sent, nada hace).
- Si `private == true` → no-op (nota privada).
- Si `source_id` empieza con `"WAID:"` → no-op (echo).
- Si `conversation.meta.sender.identifier` empieza con `"qrsgen-qr-"` →
  no-op (contacto sintético del propio bridge).
- Si el spamguard está activado y el contenido es duplicado del último o
  penúltimo enviado a ese JID → no-op + evento `spam_blocked`.

---

## Endpoints — Observabilidad

### `GET /api/instances/:name/outbox`

Stats del buffer outgoing por instancia.

```json
{"pending":0,"sent":127,"expired":1,"failed":0}
```

Cuenta sobre todas las filas históricas de `bridge_outgoing_queue` para esa
instancia. `pending` es el indicador que importa para alerting en caliente.

---

### `GET /api/instances/:name/ban-risk`

Snapshot del detector proactivo de WhatsApp ban-risk (ver
[arquitectura](architecture.md) para detalle de las tres señales).

```json
{
  "instance": "whatsapp-main",
  "velocity_msgs_per_window": 12, "velocity_threshold": 30,
  "velocity_window_ns": 60000000000,
  "diversity_unique_jids": 8, "diversity_threshold": 20,
  "diversity_window_ns": 300000000000,
  "delivery_ratio": 0.97, "delivery_samples": 30,
  "delivery_threshold": 0.7, "delivery_min_samples": 10,
  "delivery_window_ns": 600000000000,
  "alerts": [],
  "score": 0.13,
  "level": "low"
}
```

`level`: `ok` | `low` | `moderate` | `high`. Cuando un signal cruza su
threshold (`alerts` no vacío), qrsgen emite el evento lifecycle `ban_risk`
en rising-edge (una sola vez hasta que se limpie).

---

### `GET /api/instances/:name/usage?from=YYYY-MM-DD&to=YYYY-MM-DD`

Counters diarios en UTC para una instancia. Default: últimos 30 días.

```json
{
  "instance": "whatsapp-main",
  "from": "2026-04-26",
  "to":   "2026-05-26",
  "rows": [
    {"instance":"whatsapp-main","day":"2026-05-26",
     "messages_in":24,"messages_out":31,
     "spamguard_blocks":0,"lifecycle_events":2}
  ]
}
```

---

### `GET /api/usage?from=YYYY-MM-DD&to=YYYY-MM-DD`

Igual que el anterior pero para **todas las instancias**. `rows` ordenado
por `instance, day`. Pensado para dashboards y exports CSV.

---

### `GET /api/usage/summary?from=YYYY-MM&to=YYYY-MM`

Agregado mensual por `(owner_tag, mes)`. Default: últimos 3 meses naturales.

```json
{
  "from": "2026-03", "to": "2026-05",
  "rows": [
    {
      "owner_tag": "tenant-acme", "month": "2026-05",
      "messages_in": 4821, "messages_out": 5102,
      "spamguard_blocks": 14, "lifecycle_events": 23,
      "active_instances": 2
    },
    {
      "owner_tag": "", "month": "2026-05",
      "messages_in": 18, "messages_out": 22,
      "spamguard_blocks": 0, "lifecycle_events": 1,
      "active_instances": 1
    }
  ]
}
```

Pensado para billing: el integrador mapea `owner_tag` a su tenant y suma
los counters que tarifique.

---

### `GET /api/audit?instance=&limit=`

Append-only log de operaciones (`instance.create / patch / delete`,
`outbox.enqueue / expire / failed`, `backend.boot`). La tabla subyacente
tiene triggers que rechazan UPDATE/DELETE — inmutable a nivel DB.

Query params:

| Param | Default | Notas |
|---|---|---|
| `instance` | (vacío) | Filtrar por nombre de instancia. |
| `limit` | 100 | Máximo 500. |

```json
{
  "entries": [
    {
      "id": 412,
      "ts": "2026-05-26T08:15:33Z",
      "actor": "api",
      "action": "instance.patch",
      "instance": "whatsapp-main",
      "target": "",
      "metadata": {"owner_tag_set": true, "spamguard_enabled_set": false}
    }
  ]
}
```

---

### `GET /api/health`

Liveness check. Sin auth.

```json
{
  "status": "ok",
  "instances": [{"name":"whatsapp-main","state":"ready","jid":"..."}],
  "version": "0.23.0",
  "ts": "2026-05-26T11:30:00Z"
}
```

---

### `GET /metrics`

Prometheus scrape. Sin auth.

| Métrica | Tipo | Labels | Descripción |
|---|---|---|---|
| `qrsgen_messages_total` | counter | `direction`, `instance` | Mensajes procesados (in/out). |
| `qrsgen_spamguard_blocks_total` | counter | `instance` | Outgoings bloqueados por dup. |
| `qrsgen_lifecycle_events_total` | counter | `instance`, `event` | Eventos lifecycle emitidos. |
| `qrsgen_message_dispatch_errors_total` | counter | `direction`, `instance`, `kind` | Fallos de despacho. |
| `qrsgen_active_instances` | gauge | – | Instancias en `connected` o `ready`. |
| `qrsgen_total_instances` | gauge | – | Total gestionadas. |

Plus métricas estándar Go runtime (`go_*`, `process_*`).

---

### `GET /static/brand-asset.png`

Asset estático (ej. avatar genérico). Útil si el downstream necesita
descargar un PNG por URL para asociar a un contacto sintético.

---

## Lifecycle webhooks

Cuando una instancia tiene `events_webhook_url` configurado, qrsgen
POSTea cambios de estado a esa URL. Esquema común:

```json
{
  "instance": "whatsapp-main",
  "event": "connected",
  "occurred_at": "2026-05-26T11:30:00Z",
  "jid": "34650367855:28@s.whatsapp.net"
}
```

Algunos eventos llevan campos extra (`extras`). Catálogo completo:

| Event | Descripción | Extras |
|---|---|---|
| `qr_generated` | Hay un QR nuevo listo en `/qr`. | `last_qr_msg_id` (si lo configuraste vía PATCH) |
| `paired` | Usuario escaneó. Esperando primer `Connected`. | – |
| `connected` | Sesión activa, listo para enviar/recibir. | – |
| `reconnected` | Sesión vuelve tras un `unreachable`. Sólo se emite tras 5s de estabilidad. | – |
| `unreachable` | Disconnected silencioso 60s. Si vuelve antes → blip silencioso (no se emite). | – |
| `disconnected` | Confirmación de desconexión prolongada. | – |
| `logged_out` | Sesión invalidada server-side. Necesita nuevo QR. | – |
| `strike` | WhatsApp emitió ConnectFailure o TemporaryBan. **Acción inmediata recomendada.** | – |
| `spam_blocked` | El spamguard descartó un outgoing duplicado. | `count`, `preview` |
| `ban_risk` | Detector cruzó un threshold (velocity / diversity / delivery_ratio). | `alert`, `score`, `level`, `velocity`, `diversity`, `delivery_ratio` |
| `outgoing_expired` | Un mensaje en el outbox no se pudo entregar antes del TTL. | `queue_id`, `remote_jid`, `preview` |
| `backend_restarting` | Emitido al SIGTERM, antes del shutdown. | – |
| `backend_started` | Emitido por instancia tras `Bootstrap` (8s post-boot). | – |

Los webhooks salen en goroutines independientes — qrsgen no bloquea
cuando el orquestador tarda en responder. Si el POST falla (timeout
10s, 4xx, 5xx), se loguea y se sigue (no hay retry queue del lifecycle —
diseño intencional: WhatsApp ya seguirá emitiendo eventos).

---

## Flujos comunes

### Provisión + primer pairing

```
POST /api/instances              { name, events_webhook_url, inbox_id, owner_tag? }
   ↓ event: qr_generated → events_webhook_url
GET  /api/instances/:name/qr     (PNG, mostrar al usuario)
   ↓ usuario escanea
   ↓ event: paired → events_webhook_url
   ↓ event: connected → events_webhook_url
   ↓ ya puedes enviar mensajes
```

Alternativa: `GET /api/instances/:name/wait-ready?timeout=120` bloquea
hasta `ready` sin necesidad de polling.

### Enviar un mensaje

```
POST /api/instances/:name/webhook   { message_type:"outgoing", content, ... }
   → 200 {status:"sent"}         si la instancia está conectada
   → 202 {status:"queued", ...}  si está reconectando (outbox)
```

### Sesión perdida (logged_out)

```
event: logged_out → events_webhook_url
POST /api/instances/:name/refresh-qr
   ↓ event: qr_generated → events_webhook_url
GET  /api/instances/:name/qr     (PNG nuevo)
   ↓ usuario escanea
   ↓ event: paired / connected
```

### Borrado limpio

```
POST   /api/instances/:name/logout    (invalida server-side)
DELETE /api/instances/:name           (borra bridge_instance row + audit)
```

### Billing al cierre de mes

```
GET /api/usage/summary?from=2026-05&to=2026-05
   ↓ iterar rows agrupando por owner_tag → tarifar
```

### Investigar un strike

```
GET /api/audit?instance=whatsapp-main&limit=200
   ↓ buscar entradas anteriores al evento "strike"
GET /api/instances/whatsapp-main/usage?from=...&to=...
   ↓ ver pico de envíos
GET /api/instances/whatsapp-main/ban-risk
   ↓ ver snapshot actual
```
