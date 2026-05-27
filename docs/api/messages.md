# Mensajes (downstream → WhatsApp)

## `POST /api/instances/:name/webhook`

**Entrypoint** del downstream para enviar un mensaje. Sin auth Bearer por
defecto; HMAC opcional vía `WEBHOOK_HMAC_SECRET`
(ver [Convenciones](conventions.md)).

## WebhookPayload schema

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

## Responses

**200 — instancia conectada, mensaje entregado a WhatsApp:**
```json
{"status":"sent"}
```

**202 — instancia disconnected, encolado para retry:**
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

**422 — spamguard bloqueó el outgoing como duplicado** (desde v0.28.4):
```json
{
  "status": "blocked",
  "reason": "spamguard: duplicate of one of the 2 most recent outgoings to this contact"
}
```

Chatwoot (y cualquier downstream `api_channel` que respete el contrato)
marca entonces el mensaje como `failed` (icono rojo). El agente sabe al
instante que su outgoing no se entregó. En paralelo, qrsgen emite el
evento lifecycle `spam_blocked` con `msg_id`, `conv_id`, `remote_jid`,
`preview` y `count` — útil para que el orquestador linkee al mensaje
desde su panel de ops.

**Otros códigos:**

- `400` — JSON inválido.
- `401` — HMAC signature mismatch (si `WEBHOOK_HMAC_SECRET` activo).
- `500` — fallo whatsmeow / downstream blob download.
- `503` — queue llena (>200 pending para la instancia).

## Safety nets

qrsgen aplica varios filtros antes de despachar a whatsmeow. Cualquiera
de ellos hace **no-op** (devuelve 200 sin enviar):

- `message_type != "outgoing"`.
- `private == true` (nota privada — visible solo en el panel del downstream).
- `source_id` empieza con `"WAID:"` (echo del propio mensaje saliente).
- `conversation.meta.sender.identifier` empieza con `"qrsgen-qr-"`
  (contacto sintético del propio bridge — panel de ops).
- Spamguard activado + contenido duplicado de los últimos 2 enviados a
  ese JID → 422 + evento `spam_blocked`. **Desde v0.28.4 devuelve 422
  en lugar de 200 silencioso**, así Chatwoot lo marca como failed.

## Glosario

**WebhookPayload**: estructura JSON que el downstream envía a qrsgen
para pedir el envío de un mensaje. Sigue el formato de Channel::Api
estándar.

**Outgoing**: mensaje que va de tu sistema hacia el cliente WhatsApp
(opuesto a "incoming", que va del cliente a tu sistema).

**Echo del propio mensaje**: cuando un cliente WhatsApp recibe un
mensaje que **tú mismo enviaste**, su app lo emite como "fromMe" tras
sync. qrsgen lo detecta por el prefijo `WAID:` en `source_id` y lo
ignora, evitando dobles entregas.

**WAID** (WhatsApp ID): identificador único que WhatsApp asigna a cada
mensaje enviado. qrsgen lo recibe tras un `SendText` exitoso y lo
sincroniza con el downstream (`PATCH source_id="WAID:..."`).

**JID del destinatario**: identificador WhatsApp del receptor del
mensaje. Va en `conversation.meta.sender.identifier`. Formato
`<phone>@s.whatsapp.net` o `<id>@lid`.

**Safety net**: filtros que qrsgen aplica antes de enviar un mensaje
para descartar casos peligrosos (notas privadas, ecos, contactos
sintéticos del propio bridge). Devuelven `200` sin hacer nada.

**Nota privada** (`private: true`): mensaje que el agente humano escribe
en el panel del downstream para uso interno. **No se envía a WhatsApp**
— qrsgen lo descarta automáticamente.

**qrsgen-qr-***: prefijo de contactos sintéticos que el propio bridge
crea para mostrar paneles de status. No tienen número real de WhatsApp;
qrsgen rechaza cualquier outgoing dirigido a ellos.

**Outbox queued (202)**: cuando la instancia está disconnected, qrsgen
guarda el payload crudo en `bridge_outgoing_queue` y devuelve `202` con
el `queue_id` y `expires_at`. El drainer lo entrega cuando vuelva.

**MaxQueueDepth**: límite máximo de mensajes pending por instancia (200
default). Evita acumulación infinita si una instancia muere
permanentemente. Cuando se alcanza, nuevos POSTs devuelven `503`.
