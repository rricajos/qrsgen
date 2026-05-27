# Capa 2 — HMAC del webhook entrante

## Qué hace

El endpoint `POST /api/instances/:name/webhook` está exento del Bearer
token de la [capa 1](layer-1-bearer.md) (los downstream típicos no firman
con auth genérica). En su lugar, qrsgen acepta una firma HMAC en un
header dedicado.

Formato del header:

```
X-Qrsgen-Signature: sha256=<hex>

donde <hex> = HMAC-SHA256(secret, raw_body)
```

Mismatches devuelven `401`.

### Resolución del `secret` (desde v0.26.0)

qrsgen elige el secret efectivo por request en este orden:

1. **Per-tenant**: si la instancia (`:name` en la URL) tiene `owner_tag`
   mapeado a un tenant con `webhook_hmac_secret` configurado en
   `bridge_tenant` (vía `PUT/PATCH /api/tenants/:owner_tag`), se usa
   ese secret.
2. **Fallback global**: si no hay per-tenant, se usa el
   `WEBHOOK_HMAC_SECRET` del env (compat con el modelo single-tenant).
3. **Sin HMAC**: si ambos están vacíos, el endpoint queda abierto en
   LAN (backward-compat para deploys sin firma).

En arquitecturas multi-downstream / SaaS, configura per-tenant para
aislar credenciales entre clientes — un secret comprometido solo
afecta a un cliente.

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

## Glosario

**HMAC** (Hash-based Message Authentication Code): firma criptográfica
que prueba que un mensaje viene de alguien que conoce un secret
compartido **y** que el mensaje no fue alterado en tránsito.

**SHA-256**: función de hash criptográfica que produce un digest de 256
bits. Usada como parte interna del HMAC. Resistente a colisiones para
todos los efectos prácticos.

**Body crudo** (raw body): el body de la HTTP request tal cual llegó,
sin re-serializar. Importante para HMAC: si re-serializas, los
caracteres pueden cambiar de orden y la firma falla.

**Constant-time compare**: comparación criptográfica que tarda lo mismo
independientemente del input, para evitar timing attacks. Go la provee
con `crypto/hmac.Equal`.

**Timing attack**: ataque donde el atacante deduce información midiendo
cuánto tarda una comparación. HMAC mal implementado puede filtrar bytes
del secret. qrsgen usa `hmac.Equal` para prevenirlo.

**Secret compartido**: string que solo conocen el emisor (downstream) y
el receptor (qrsgen). Debe ser largo (32+ bytes) y aleatorio. Si se
filtra, se rota.

**Inyección por LAN**: un atacante con presencia en el overlay LAN
intenta enviar requests directas al endpoint sin auth. HMAC lo bloquea
si no tiene el secret.
