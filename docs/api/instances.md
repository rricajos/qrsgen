# Instancias

Endpoints para crear, listar, actualizar y borrar instancias qrsgen.

## `POST /api/instances`

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

| Campo | Tipo | Requerido | Descripción |
|---|---|---|---|
| `name` | string | ✓ | Identificador único de la instancia. Aparece en todas las URLs. Usa nombres descriptivos (`whatsapp-main`, `whatsapp-sales`). |
| `events_webhook_url` | string | – | URL donde qrsgen POSTea lifecycle events (ver [Lifecycle webhooks](lifecycle-webhooks.md)). |
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
  "inbox_id": 91,
  "spamguard_enabled": true,
  "last_qr_msg_id": 12345,
  "owner_tag": "tenant-acme"
}
```

Pasar `owner_tag: ""` (string vacío) **borra** el tag previo. Omitirlo
lo deja intacto. Misma semántica para los demás campos opcionales.

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
