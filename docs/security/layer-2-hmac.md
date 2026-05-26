# Capa 2 — HMAC del webhook entrante

## Qué hace

El endpoint `POST /api/instances/:name/webhook` está exento del Bearer
token de la [capa 1](layer-1-bearer.md) (los downstream típicos no firman
con auth genérica). En su lugar, qrsgen acepta una firma HMAC en un
header dedicado.

Cuando `WEBHOOK_HMAC_SECRET` está set:

```
X-Qrsgen-Signature: sha256=<hex>

donde <hex> = HMAC-SHA256(WEBHOOK_HMAC_SECRET, raw_body)
```

Mismatches devuelven `401`. Si la env var está vacía, el endpoint queda
abierto en LAN (backward-compat).

## Configuración

```yaml
environment:
  WEBHOOK_HMAC_SECRET: "${WEBHOOK_HMAC_SECRET}"
```

En el downstream, firmar antes de POST:

```javascript
const crypto = require('crypto');
const body = JSON.stringify(payload);
const sig  = 'sha256=' + crypto.createHmac('sha256', secret).update(body).digest('hex');
fetch('http://qrsgen:3100/api/instances/whatsapp-main/webhook', {
  method: 'POST', body,
  headers: { 'Content-Type':'application/json', 'X-Qrsgen-Signature': sig },
});
```

## Qué mitiga

Vector #1 dirigido al webhook: un container LAN que adivine la URL del
endpoint no puede inyectar mensajes outgoing — no tiene el secret. Sin
esta capa, cualquier container del overlay podría POSTear y enviar
mensajes arbitrarios al cliente.

## Cómo verificarla

```bash
# Sin firma → 401
curl -sS -o /dev/null -w "%{http_code}\n" -X POST \
  http://qrsgen:3100/api/instances/whatsapp-main/webhook \
  -H 'Content-Type: application/json' -d '{}'
# 401

# Firma correcta → 200/202
BODY='{"event":"message_created","message_type":"outgoing","content":"hola","conversation":{"id":1,"meta":{"sender":{"identifier":"test@s.whatsapp.net"}}},"id":1}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_HMAC_SECRET" -hex | awk '{print $2}')"
curl -sS -X POST http://qrsgen:3100/api/instances/whatsapp-main/webhook \
  -H 'Content-Type: application/json' \
  -H "X-Qrsgen-Signature: $SIG" \
  -d "$BODY"
```
