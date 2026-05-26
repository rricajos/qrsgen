# Migrar desde Whapi.cloud

[Whapi.cloud](https://whapi.cloud/) es el SaaS WhatsApp con más
tracción en EU/US (precio típico $39-129/mes/canal). Su modelo:
**channels** (1 channel = 1 número), con webhook configurable por
channel y API gateway centralizado.

## Mapeo conceptual

| Whapi.cloud | qrsgen |
|---|---|
| `channel` (con `id` y `name`) | Instancia `name` |
| `channel.token` (per-channel) | Bearer global `QRSGEN_API_TOKEN` + `owner_tag` per-tenant |
| `webhook.url` (configurado por channel) | `events_webhook_url` per-instance |
| `POST /messages/text` en `gate.whapi.cloud` | `POST /api/instances/:name/webhook` |
| Webhook payload format Whapi | `WebhookPayload` Channel::Api-compatible de qrsgen |
| Manager UI en `panel.whapi.cloud` | API REST `/api/instances/*` |

## API surface relevante (lo que necesitas leer)

Whapi expone dos hostnames:

```
manager.whapi.cloud   ← admin API (listar channels, crear/borrar)
gate.whapi.cloud      ← runtime API (enviar mensajes, recibir webhooks)
```

### Endpoints útiles para la migración

| Endpoint | Para qué |
|---|---|
| `GET https://manager.whapi.cloud/channels` | Listar todos tus channels + metadata |
| `GET https://manager.whapi.cloud/channels/:id` | Detalle de un channel (incluye webhook config) |
| `GET https://gate.whapi.cloud/settings` | Settings runtime del channel (su token) |

Auth: header `Authorization: Bearer <WHAPI_TOKEN>` en manager,
`Authorization: Bearer <CHANNEL_TOKEN>` en gate (cada channel tiene su
token).

### Estructura del response de `/channels` (resumida)

```json
{
  "channels": [
    {
      "id": "ABCD1234",
      "name": "my-bot-1",
      "phone": "34600000000",
      "status": "AUTH",
      "settings": {
        "webhooks": [
          {"url": "https://my-app.com/whapi-events", "events": ["messages"]}
        ]
      },
      "billing": {"plan": "starter"}
    }
  ]
}
```

Los campos que importan para qrsgen:
- `name` → `name` de instancia.
- `settings.webhooks[0].url` → `events_webhook_url`.
- `phone` → informativo (qrsgen lo descubre tras pairing).

**Whapi NO expone**: las claves criptográficas de la sesión WhatsApp,
ni el historial completo de mensajes vía API. Esto es **expected**:
ningún SaaS las expone (son su moat) y aunque las exportara no
serían portables a otro cliente (limitación del protocolo).

## Receta

### 1. Inventory

```bash
WHAPI_TOKEN="..."   # tu token de manager.whapi.cloud
curl -sS -H "Authorization: Bearer $WHAPI_TOKEN" \
  https://manager.whapi.cloud/channels | jq '.channels[] | {
    name: .name,
    webhook_url: (.settings.webhooks[0].url // null),
    phone: .phone,
    status: .status
  }' > /tmp/whapi-channels.json
```

### 2. Generar plan JSON para qrsgen

```python
import json

with open("/tmp/whapi-channels.json") as f:
    channels = [json.loads(line) for line in f.read().strip().split("\n}\n{")]
# (parsing pragmático — adapta según output real de tu jq)

plan = {
  "instances": [
    {
      "name": c["name"],
      "events_webhook_url": c["webhook_url"] or "https://my-app.com/qrsgen-events",
      "owner_tag": "migrated-from-whapi",
    }
    for c in channels if c.get("name")
  ]
}

with open("/tmp/qrsgen-plan.json", "w") as f:
    json.dump(plan, f, indent=2)
```

### 3. Provisionar en qrsgen

```bash
QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN="$TOK" \
  python3 tools/migrate/bulk-provision.py /tmp/qrsgen-plan.json
```

### 4. Re-pairing (manual, ineludible)

Los usuarios re-escanean. Whapi guarda la sesión en SU
infraestructura — ni siquiera te la exporta. Cualquier migración a
otra plataforma WhatsApp requiere re-pairing.

Comunica una ventana clara (ej. "viernes 22:00 - sábado 09:00").
Mantén Whapi activo hasta que la mayoría haya migrado.

### 5. Switch del webhook del downstream

Cambias la URL que apunta a Whapi por la de qrsgen:

```diff
- webhook_url = https://gate.whapi.cloud/messages?channel_id=ABCD
+ webhook_url = http://qrsgen:3100/api/instances/<INSTANCE_NAME>/webhook
```

Si tu sistema downstream sigue esperando el **formato Whapi**, qrsgen
emite Channel::Api estándar (Chatwoot-style), que es ligeramente
distinto. Mira [Diferencias de payload](#diferencias-de-payload) al
final de esta página.

### 6. Cancelar channels en Whapi

```bash
# Pausar (recomendado: 1-2 semanas por si necesitas revertir)
curl -X PATCH -H "Authorization: Bearer $WHAPI_TOKEN" \
  https://manager.whapi.cloud/channels/ABCD1234 \
  -d '{"status": "STOPPED"}'

# Borrar (irreversible)
curl -X DELETE -H "Authorization: Bearer $WHAPI_TOKEN" \
  https://manager.whapi.cloud/channels/ABCD1234
```

## Diferencias de payload (webhook entrante)

Whapi te envía algo como:

```json
{
  "messages": [{
    "id": "WAID:...",
    "from_me": false,
    "from": "34600000000",
    "type": "text",
    "text": {"body": "Hola"},
    "timestamp": 1717000000
  }],
  "event": {"type": "messages", "event": "post"},
  "channel_id": "ABCD1234"
}
```

qrsgen entrega al downstream con shape Channel::Api:

```json
{
  "event": "message_created",
  "id": <int>,
  "message_type": "incoming",
  "content": "Hola",
  "source_id": "WAID:...",
  "conversation": {
    "id": <int>,
    "inbox_id": 87,
    "meta": {"sender": {"phone_number": "+34600000000", "identifier": "34600000000@s.whatsapp.net"}}
  }
}
```

Si tu downstream es Chatwoot o un proxy n8n que ya entiende
Channel::Api → no cambia nada. Si esperaba el shape Whapi exacto, hay
que adaptar (sustituir `messages[].text.body` por `content`, etc.).

## Coste Whapi vs qrsgen self-host

| Plan Whapi | Precio | Equivalente qrsgen self-host |
|---|---|---|
| Starter (1 channel, 1k msgs/día) | $39/mes | VPS €10/mes — 4× más barato |
| Business (5 channels, 5k msgs/día) | $79/mes | VPS €15/mes — 5× más barato |
| Pro (20 channels, 20k msgs/día) | $189/mes | VPS €20/mes — 9× más barato |

Antes de migrar: **calcula tu TCO real** (incluyendo tu tiempo, no
solo VPS). Si tu hora vale más de lo que ahorras, el SaaS sigue
siendo razonable.

## Glosario

**Whapi.cloud**: SaaS WhatsApp con sede en EU. Precio $39-189/mes/channel.

**Channel**: terminología Whapi para "una sesión WhatsApp" (= un
número). Equivalente a `instance` en qrsgen.

**Manager API** (Whapi): endpoint en `manager.whapi.cloud` para
operaciones administrativas (crear, listar, borrar channels).

**Gate API** (Whapi): endpoint en `gate.whapi.cloud` para runtime
(enviar mensajes, recibir webhooks).

**Channel token**: token específico de un channel, distinto del token
de cuenta. qrsgen usa un único `QRSGEN_API_TOKEN` global y diferencia
clientes con `owner_tag`.

**Channel::Api**: estándar de webhook payload (originario de
Chatwoot) que qrsgen emite. Diferente del payload Whapi nativo.
