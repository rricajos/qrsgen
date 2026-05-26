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
- Spamguard activado + contenido duplicado de los últimos 2 enviados a ese
  JID → no-op + evento `spam_blocked`.
