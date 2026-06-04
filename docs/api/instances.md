# Instancias

Endpoints para crear, listar, actualizar y borrar instancias qrsgen.

## `POST /api/instances`

Crea (o reusa, idempotente) una instancia.

**Request:**
```json
{
  "name": "whatsapp-main",
  "events_webhook_url": "https://workflows.example.com/qrsgen-events",
  "events_webhook_subscribers": [
    {"url": "https://primary.example.com/lifecycle"},
    {"url": "https://observability.example.com/qrsgen", "events": ["disconnected", "spam_blocked"]}
  ],
  "inbox_id": 90,
  "owner_tag": "tenant-acme"
}
```

| Campo | Tipo | Requerido | Descripción |
|---|---|---|---|
| `name` | string | ✓ | Identificador único de la instancia. Aparece en todas las URLs. Usa nombres descriptivos (`whatsapp-main`, `whatsapp-sales`). |
| `events_webhook_url` | string | – | URL donde qrsgen POSTea lifecycle events. **Modo legacy** (single subscriber). Si `events_webhook_subscribers` está set, este campo se mantiene en DB pero NO se usa para fan-out. Ver [Lifecycle webhooks](lifecycle-webhooks.md). |
| `events_webhook_subscribers` | array | – | **v0.65.0+** Fan-out a múltiples destinos con filter de eventos por suscriptor. Si está set, sobreescribe el modo legacy. Cada item tiene shape `WebhookSubscriber` (ver tabla abajo). |
| `inbox_id` | int | – | ID arbitrario para routing downstream. qrsgen lo propaga en el payload de incoming msgs. |
| `owner_tag` | string | – | String libre para correlación tenant ↔ instancia. Aparece en `/api/usage/summary`. |

**Shape `WebhookSubscriber`** (items de `events_webhook_subscribers`):

| Campo | Tipo | Requerido | Descripción |
|---|---|---|---|
| `url` | string | ✓ | URL HTTPS donde qrsgen POSTea el payload del evento (mismo shape que `events_webhook_url`). |
| `events` | array<string> | – | Lista de nombres de eventos que este suscriptor recibe. Si está vacío u omitido, recibe **todos** los eventos. Nombres válidos: ver [Lifecycle webhooks](lifecycle-webhooks.md) (`paired`, `connected`, `disconnected`, `unreachable`, `logged_out`, `qr_generated`, `spam_blocked`, `backend_started`, etc.). |

Cada suscriptor recibe el evento independientemente con su propio
retry budget — un suscriptor lento no bloquea a los demás.

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

## `GET /api/instances`

Lista todas las instancias con estado simple. Side-effect: actualiza
gauges Prometheus `qrsgen_active_instances` + `qrsgen_total_instances`.

**Response 200:**
```json
[
  {"name":"whatsapp-main","state":"ready","jid":"34650367855:28@s.whatsapp.net"},
  {"name":"whatsapp-sales","state":"qr_pending"}
]
```

---

## `GET /api/instances/:name`

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

## `GET /api/instances/:name/state`

Versión mínima — sólo `instance`, `state`, `jid`. Útil para polling barato.

```json
{"instance":"whatsapp-main","state":"ready","jid":"34650367855:28@s.whatsapp.net"}
```

---

## `PATCH /api/instances/:name`

Actualiza configuración existente. Campos `null`/omitidos no se tocan.

**Request:**
```json
{
  "events_webhook_url": "https://nueva-url",
  "events_webhook_subscribers": [
    {"url": "https://primary.example.com/lifecycle"},
    {"url": "https://metrics.example.com/qrsgen", "events": ["disconnected"]}
  ],
  "inbox_id": 91,
  "spamguard_enabled": true,
  "last_qr_msg_id": 12345,
  "owner_tag": "tenant-acme"
}
```

Pasar `owner_tag: ""` (string vacío) **borra** el tag previo. Omitirlo
lo deja intacto. Misma semántica para los demás campos opcionales.
Para `events_webhook_subscribers`: pasar array vacío `[]` resetea al
modo legacy (`events_webhook_url` solo); omitirlo conserva el array
previo intacto.

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

## `DELETE /api/instances/:name`

Para la instancia y elimina la fila de `bridge_instance`. **No** elimina
las tablas `whatsmeow_*` asociadas — la sesión queda inválida pero las
keys permanecen (limpieza manual si las necesitas borrar).

**Response 200:** `{"message":"deleted"}`. Quedará registrado en
`bridge_audit_log` como `instance.delete`.

---

## `POST /api/instances/bulk`

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

## `GET /api/instances/bulk/status`

Estado rico de todas las instancias en una sola request. Equivalente a
hacer `/api/instances/:name` por cada nombre en `/api/instances` pero en
una sola consulta.

---

## `POST /api/instances/:name/avatars/resync`

Fuerza un re-sync del avatar para **todos** los contactos del inbox
asociado a la instancia. Útil como backfill tras adoptar la feature
(v0.31.0+) sobre un inbox con contactos pre-existentes que tienen
letter-avatars. Desde v0.31.3.

Itera el endpoint Chatwoot
`/api/v1/accounts/X/inboxes/Y/contacts?page=Z` paginando hasta agotar
contactos o alcanzar el cap defensivo de 200 páginas (~3000 contactos).
Por cada contact cuyo `identifier` parsea como JID válido, spawnea
una goroutine `syncAvatar` bypassing el tracker (resetea `LastID`).

**Headers:**
```
Authorization: Bearer ${QRSGEN_API_TOKEN}
```

**Response 200:**
```json
{
  "instance": "whatsapp-main",
  "scanned": 234,
  "skipped": 5,
  "queued": 229,
  "pages": 16
}
```

| Campo | Significado |
|---|---|
| `scanned` | Contactos totales devueltos por el downstream. |
| `skipped` | Contactos cuyo `identifier` no parsea como JID (placeholders, sintéticos). |
| `queued` | Goroutines `syncAvatar` lanzadas. No refleja éxito real — mira logs (`avatar synced`). |
| `pages` | Páginas iteradas. |

**Códigos posibles:** `200`, `401` (token inválido o ausente), `404`
(instancia no existe), `500` (downstream inaccesible). Fallos
individuales de sync NO suben a 500 — se loguean como warn y siguen.

Ver [Avatar sync](../integrations/avatar-sync.md) para flujo completo
y modos de fallo.

## Glosario

**Instancia**: una sesión WhatsApp dentro del proceso qrsgen. Cada
instancia tiene su propio WebSocket contra Meta, sus propios buffers en
memoria y su fila en `bridge_instance`.

**JID** (JabberID): identificador interno de WhatsApp. Para un número de
teléfono normal es `<phone>@s.whatsapp.net`. Para cuentas anónimas
Multi-Device es `<id>@lid`. qrsgen lo guarda tras el primer pairing.

**LID** (Linked Identifier): identificador anónimo que WhatsApp asigna
a clientes Multi-Device. Coexiste con el JID PN (phone-number) tradicional.

**Phone (E.164)**: número de teléfono en formato internacional sin
prefijo `+` (ej. `34650367855`). qrsgen lo extrae del JID PN cuando está
disponible.

**owner_tag**: string libre del integrador para mapear instancias a
tenants/clientes. qrsgen lo pasa por los reportes de uso pero nunca lo
interpreta semánticamente.

**Inbox ID**: identificador numérico opcional que qrsgen propaga al
downstream. Útil cuando el sistema downstream organiza conversaciones por
"inbox" (ej. Chatwoot, Help Scout).

**Spamguard**: filtro interno que detecta cuando un mismo mensaje se
intenta enviar dos veces al mismo destinatario consecutivamente. Se
activa con `spamguard_enabled: true`.

**Idempotencia (en POST /api/instances)**: si llamas con un `name` que
ya existe, qrsgen no crea una nueva — devuelve la existente con su
estado actual.

**Bulk**: operación que procesa varias instancias en una sola request,
para evitar latencias acumuladas en provisioning masivo.
