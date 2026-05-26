# Convenciones

## Base URL

`http://qrsgen:3100` desde dentro de la overlay docker. Aliases válidos:
`qrsgen` o `bridge_bridge`.

## Autenticación

Casi todos los endpoints requieren `Authorization: Bearer <QRSGEN_API_TOKEN>`.
Exentos:

| Endpoint | Por qué |
|---|---|
| `GET /api/health` | Liveness / readiness probes. |
| `POST /api/instances/:name/webhook` | El downstream rara vez manda headers arbitrarios; usa HMAC en otro header si necesitas autenticación (ver abajo). |
| `GET /metrics` | Prometheus scrape. |
| `GET /static/*` | Assets públicos. |

Si `QRSGEN_API_TOKEN` está vacío en env, todos los endpoints quedan abiertos
(modo dev — un WARNING aparece al boot).

## HMAC del webhook entrante (opcional)

Cuando `WEBHOOK_HMAC_SECRET` está set en env,
`POST /api/instances/:name/webhook` exige
`X-Qrsgen-Signature: sha256=<hex>` donde
`<hex> = HMAC-SHA256(secret, raw_body)`. Mismatches devuelven `401`. Si la
env var está vacía, el endpoint queda abierto en LAN.

## Headers comunes

- `Authorization: Bearer <token>` — auth (ver arriba).
- `Content-Type: application/json` — requests con body JSON.
- `X-Qrsgen-Signature: sha256=<hex>` — sólo en webhook si HMAC activo.
- `X-Migration-Id: <id>` — opcional, se propaga a logs y response para
  correlacionar trazas entre tu orquestador y qrsgen.

## Códigos de error genéricos

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

Cada endpoint puede añadir códigos específicos — se anotan en su página.
