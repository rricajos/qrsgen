# Códigos de error

qrsgen devuelve **códigos de error estables** en el body de las
respuestas 4xx/5xx (desde v0.28.5). El integrador puede pattern-matchear
contra `error_code` sin parsear strings.

Patrón: `QRSGEN_<CATEGORY>_<REASON>` (uppercase, snake_case).
**Los códigos existentes nunca se renombran** — contrato público. Solo
se añaden nuevos en versiones futuras.

## Tabla

| Código | HTTP | Significado |
|---|---|---|
| `QRSGEN_SPAMGUARD_BLOCKED` | `422` | Outgoing duplicado bloqueado por spamguard. Chatwoot marca el mensaje como `failed` (icono rojo). |
| `QRSGEN_HMAC_MISMATCH` | `401` | Header `X-Qrsgen-Signature` ausente o inválido. |
| `QRSGEN_PAYLOAD_INVALID` | `400` | JSON malformado o campos requeridos faltantes. |
| `QRSGEN_QUEUE_FULL` | `503` | Outbox per-instance al límite (`MaxQueueDepth`, default 200). |
| `QRSGEN_INSTANCE_NOT_FOUND` | `404` | La instancia referenciada en la URL no existe. |
| `QRSGEN_TENANT_NOT_FOUND` | `404` | El `owner_tag` no tiene config en `bridge_tenant`. |
| `QRSGEN_INTERNAL` | `500` | Fallo inesperado (bug qrsgen o dep externa caída). Revisar logs. |

## Formato de respuesta

```json
{
  "error_code": "QRSGEN_SPAMGUARD_BLOCKED",
  "error": "Bloqueado por QRsGEN — duplicado de uno de los 2 últimos mensajes al mismo contacto. No se entregó al cliente.",
  "status": "blocked"
}
```

- `error_code`: stable machine-readable string.
- `error`: descripción humana en español (default qrsgen ships en
  español; el integrador puede localizar usando `error_code`).
- Campos extra opcionales según contexto (e.g. `status` en spamguard).

## Ejemplo de uso (Python)

```python
import httpx

resp = httpx.post(f"{QRSGEN_URL}/api/instances/X/webhook", json=payload)
if resp.status_code == 422:
    body = resp.json()
    if body.get("error_code") == "QRSGEN_SPAMGUARD_BLOCKED":
        # Lógica específica: marcar conversación, notificar agente, etc.
        ...
```

## Ejemplo de uso (n8n)

En un nodo HTTP Request con "Continue on Fail", el siguiente nodo
puede leer `$json.error_code` y branchear:

```
$('http-request').item.json.error_code === 'QRSGEN_SPAMGUARD_BLOCKED'
```

## Compatibilidad

- Códigos existentes desde v0.28.5 son **estables**. Nunca se
  renombrarán.
- Códigos nuevos en versiones futuras se añadirán a esta tabla con
  nota `Desde vX.Y.Z`.
- Si un integrador match-ea contra un código que no existe en la
  versión actual, simplemente no entrará al branch — comportamiento
  forward-compatible.
