# Migrar desde MaytAPI

[MaytAPI](https://maytapi.com/) es un SaaS WhatsApp popular en Brasil
y LATAM (precios $40-200/mes/phone). Su modelo: **products** (tu
cuenta) que contienen **phones** (1 phone = 1 número), con webhooks
configurables por producto.

## Mapeo conceptual

| MaytAPI | qrsgen |
|---|---|
| `product_id` | `owner_tag` (string para correlación tenant) |
| `phone_id` (dentro del product) | Instancia `name` |
| Token por product (`api_token`) | Bearer global `QRSGEN_API_TOKEN` |
| Webhook URL por product | `events_webhook_url` per-instance |
| `POST /sendMessage` en `api.maytapi.com` | `POST /api/instances/:name/webhook` |
| Webhook payload MaytAPI | `WebhookPayload` Channel::Api-compatible |
| Panel `dashboard.maytapi.com` | API REST `/api/instances/*` |

## API surface relevante

MaytAPI usa un esquema product-based:

```
api.maytapi.com/api/<PRODUCT_ID>/...
                              ├── listPhones
                              ├── /<PHONE_ID>/
                              │   ├── sendMessage
                              │   ├── status
                              │   ├── screen        ← QR
                              │   └── ...
                              └── webhook
```

Auth: header `x-maytapi-key: <API_TOKEN>` (no Bearer).

### Endpoints útiles para la migración

| Endpoint | Para qué |
|---|---|
| `GET /api/{product_id}/listPhones` | Listar todos los phones del product |
| `GET /api/{product_id}/{phone_id}/status` | Estado de un phone concreto |
| `GET /api/{product_id}/webhook` | Webhook URL configurada |

### Estructura del response de `/listPhones` (resumida)

```json
{
  "success": true,
  "data": [
    {
      "id": 12345,
      "number": "34600000000",
      "name": "support-line",
      "type": "whatsapp",
      "status": "active",
      "multi_device": true
    }
  ]
}
```

Los campos que importan para qrsgen:
- `id` o `name` → `name` de instancia (usa `name` si lo tienes, más
  legible; si no, `phone-12345`).
- `number` → informativo (qrsgen lo descubre tras pairing).

**MaytAPI NO expone** las claves criptográficas ni historial completo
via API. Mismo limit que cualquier SaaS — re-pairing obligatorio.

## Receta

### 1. Inventory

```bash
MAYT_PRODUCT_ID="12345"
MAYT_TOKEN="..."

curl -sS -H "x-maytapi-key: $MAYT_TOKEN" \
  "https://api.maytapi.com/api/$MAYT_PRODUCT_ID/listPhones" \
  | jq '.data[] | {
    id: .id,
    name: (.name // "phone-\(.id)"),
    number: .number,
    status: .status
  }' > /tmp/maytapi-phones.json
```

### 2. Webhook URL configurada en MaytAPI (para reutilizarla)

```bash
WEBHOOK=$(curl -sS -H "x-maytapi-key: $MAYT_TOKEN" \
  "https://api.maytapi.com/api/$MAYT_PRODUCT_ID/webhook" | jq -r '.webhook // empty')
echo "Webhook actual: $WEBHOOK"
```

MaytAPI tiene **una sola webhook URL por producto** (no per-phone),
mientras qrsgen lo tiene per-instance. Esto te permite simplificar
(misma URL para todas) o aprovechar la oportunidad para separar por
instancia.

### 3. Generar plan JSON para qrsgen

```python
import json, os

with open("/tmp/maytapi-phones.json") as f:
    raw = f.read().strip()
    phones = json.loads(f"[{raw.replace('}\n{', '},{')}]") if raw else []

webhook_default = os.environ.get("NEW_WEBHOOK_URL", "https://my-app.com/qrsgen-events")
plan = {
  "instances": [
    {
      "name": p["name"],
      "events_webhook_url": webhook_default,
      "owner_tag": "migrated-from-maytapi",
    }
    for p in phones if p.get("name")
  ]
}

with open("/tmp/qrsgen-plan.json", "w") as f:
    json.dump(plan, f, indent=2)
```

### 4. Provisionar en qrsgen

```bash
QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN="$TOK" \
  python3 tools/migrate/bulk-provision.py /tmp/qrsgen-plan.json
```

### 5. Re-pairing manual

Igual que cualquier migración: usuarios re-escanean. MaytAPI no
exporta sesiones a otros clientes.

Tip: MaytAPI te muestra el QR via
`GET /api/{product_id}/{phone_id}/screen` — útil para confirmar que
**no** lo tienes (porque ya estaba pareado en MaytAPI) y por tanto sí
necesitas re-pairing en qrsgen.

### 6. Switch del webhook downstream

Cambias la URL que apunta a MaytAPI por la de qrsgen:

```diff
- webhook_url = https://api.maytapi.com/api/12345/<PHONE_ID>/sendMessage
+ webhook_url = http://qrsgen:3100/api/instances/<INSTANCE_NAME>/webhook
```

Si tu downstream procesaba payloads MaytAPI nativos, ver
[Diferencias de payload](#diferencias-de-payload) más abajo.

### 7. Pausar / cancelar en MaytAPI

```bash
# Disconnect (no borra, solo desactiva)
curl -X POST -H "x-maytapi-key: $MAYT_TOKEN" \
  "https://api.maytapi.com/api/$MAYT_PRODUCT_ID/$PHONE_ID/logout"
```

MaytAPI cobra por **phones activos** — desactivar reduce el coste
inmediatamente. El borrado definitivo se hace desde el panel.

## Diferencias de payload (webhook entrante)

MaytAPI te envía algo como:

```json
{
  "type": "message",
  "user": {"id": "34600000000@c.us", "name": "Pepe"},
  "conversation": "34600000000@c.us",
  "message": {
    "type": "text",
    "text": "Hola",
    "id": "true_34600000000@c.us_3EB0..."
  },
  "phoneId": 12345,
  "productId": "..."
}
```

qrsgen emite (Channel::Api):

```json
{
  "event": "message_created",
  "id": <int>,
  "message_type": "incoming",
  "content": "Hola",
  "source_id": "true_34600000000@c.us_3EB0...",
  "conversation": {
    "id": <int>,
    "inbox_id": 87,
    "meta": {"sender": {"phone_number": "+34600000000", "identifier": "34600000000@s.whatsapp.net"}}
  }
}
```

**JID gotcha (importante)**: MaytAPI usa el formato legacy `@c.us`.
qrsgen / whatsmeow normaliza siempre a `@s.whatsapp.net`. Si tu
downstream tiene `@c.us` hardcoded, hay que sustituirlo.

## Coste MaytAPI vs qrsgen self-host

| Plan MaytAPI | Precio | Equivalente qrsgen self-host |
|---|---|---|
| Basic (1 phone) | $40/mes | VPS €10/mes — 4× más barato |
| Pro (5 phones) | $99/mes | VPS €15/mes — 6× más barato |
| Business (20 phones) | $249/mes | VPS €25/mes — 10× más barato |

Antes de migrar: calcula tu TCO incluyendo tiempo de ops.

## Glosario

**MaytAPI**: SaaS WhatsApp con tracción especial en Brasil y LATAM.
Precio $40-249/mes según número de phones.

**Product**: terminología MaytAPI para "tu cuenta" — agrupa
varios phones. Equivalente conceptual al `owner_tag` de qrsgen
(aunque con un único product por cliente).

**Phone**: terminología MaytAPI para "una sesión WhatsApp" (= un
número). Equivalente a `instance` en qrsgen.

**x-maytapi-key**: header de auth que MaytAPI usa en lugar del Bearer
estándar.

**Multi-device** (campo de MaytAPI): indica si el phone está en modo
Multi-Device. qrsgen / whatsmeow solo soporta Multi-Device — el modo
legacy ya no se vende.

**JID legacy `@c.us`**: formato antiguo de WhatsApp para identificar
números. MaytAPI lo expone porque Baileys (su backend) lo conserva
internamente. qrsgen normaliza a `@s.whatsapp.net` (estándar
Multi-Device).
