# Tenants (multi-downstream)

Endpoints para mapear `owner_tag` → config downstream propia
(`downstream_base_url`, `downstream_api_token`, `downstream_account_id`,
`downstream_inbox_id`). Un mismo proceso qrsgen puede servir varios
downstreams distintos enrutando por estos mapeos.

Las instancias **sin `owner_tag`**, o cuyo `owner_tag` no está mapeado
aquí, caen al fallback global definido por las variables de entorno
`DOWNSTREAM_*`. Eso hace que estos endpoints sean **opcionales**: si tu
deploy es single-tenant no necesitas tocar `bridge_tenant` en absoluto.

Ver [`architecture/multi-instance.md`](../architecture/multi-instance.md)
para el modelo conceptual.

## `GET /api/tenants`

Lista todos los tenants configurados. **El campo `downstream_api_token`
nunca se devuelve** — solo se escribe.

**Response 200:**
```json
[
  {
    "owner_tag": "tenant-acme",
    "downstream_base_url": "https://acme.chatwoot.io",
    "downstream_account_id": 7,
    "downstream_inbox_id": 12,
    "created_at": "2026-05-27T08:21:47.581Z",
    "updated_at": "2026-05-27T09:14:02.118Z"
  },
  {
    "owner_tag": "tenant-globex",
    "downstream_base_url": "https://globex.example",
    "downstream_account_id": 1,
    "downstream_inbox_id": 3,
    "created_at": "2026-05-22T10:11:30.000Z",
    "updated_at": "2026-05-22T10:11:30.000Z"
  }
]
```

Los campos `created_at` y `updated_at` se incluyen desde **v0.24.2**.

## `GET /api/tenants/:owner_tag`

Detalle de un tenant. Mismo contrato que `GET /api/tenants` (sin token,
con timestamps).

**Códigos posibles:** `200`, `404`, `500`.

## `PUT /api/tenants/:owner_tag`

Upsert. Crea si no existe, actualiza in-place si existe. Tras un PUT
exitoso, qrsgen **invalida el `*Client` cacheado** de ese `owner_tag`,
por lo que la próxima request usará la nueva config sin restart.

**Request:**
```json
{
  "downstream_base_url": "https://acme.chatwoot.io",
  "downstream_api_token": "secret-token-here",
  "downstream_account_id": 7,
  "downstream_inbox_id": 12
}
```

| Campo | Tipo | Requerido | Descripción |
|---|---|---|---|
| `downstream_base_url` | string | ✓ | URL base del downstream (Chatwoot, Omnia, etc.). |
| `downstream_api_token` | string | ✓ | API token. Solo de escritura — nunca se devuelve. |
| `downstream_account_id` | int | – | Default `1`. Cuenta dentro del downstream. |
| `downstream_inbox_id` | int | – | Default `0`. Si > 0, se prioriza sobre `bridge_instance.inbox_id` y sobre el env `DOWNSTREAM_INBOX_ID`. |

**Response 200:**
```json
{"message": "tenant saved", "owner_tag": "tenant-acme"}
```

**Códigos posibles:** `200`, `400` (validation), `500`.

**Audit log**: cada PUT registra una entrada
`action="tenant.upsert"` con los campos no-secretos (URL/account/inbox).

## `DELETE /api/tenants/:owner_tag`

Elimina el mapeo. Las instancias que tenían ese `owner_tag` siguen
funcionando — **caen al fallback global** (`DOWNSTREAM_*` env vars) en
el próximo mensaje. No se pierden mensajes ni se cierran sesiones
WhatsApp; solo cambia el destino downstream.

**Response 200:**
```json
{"message": "tenant deleted", "owner_tag": "tenant-acme"}
```

**Códigos posibles:** `200`, `404`, `500`.

**Audit log**: cada DELETE registra `action="tenant.delete"`.

## Ejemplo: provisioning un nuevo cliente

```bash
# 1. Crear la config del downstream del cliente
curl -X PUT https://qrsgen.example/api/tenants/tenant-acme \
  -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "downstream_base_url":"https://acme.chatwoot.io",
    "downstream_api_token":"cw_acme_xxx",
    "downstream_account_id":7,
    "downstream_inbox_id":12
  }'

# 2. Crear la(s) instancia(s) marcadas con ese owner_tag
curl -X POST https://qrsgen.example/api/instances \
  -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"acme-main","owner_tag":"tenant-acme"}'

# 3. El cliente escanea el QR. Los mensajes entrantes ya van al
#    downstream de acme — qrsgen resuelve el route por mensaje.
```

## Glosario

**Tenant**: cliente final del integrador en el modelo SaaS. En qrsgen
se representa por un `owner_tag` (string libre) que enlaza instancias
con su config downstream.

**`owner_tag`**: etiqueta string que el integrador asigna a una instancia
para correlacionarla con su modelo de tenants. Se almacena en
`bridge_instance.owner_tag` y se usa para routing downstream.

**Fallback global**: las variables de entorno `DOWNSTREAM_BASE_URL`,
`DOWNSTREAM_API_TOKEN`, `DOWNSTREAM_ACCOUNT_ID`, `DOWNSTREAM_INBOX_ID`
configuran el destino por defecto. Se usa cuando una instancia no tiene
`owner_tag`, o cuando su `owner_tag` no está en `bridge_tenant`.

**Cache invalidation**: tras un PUT/DELETE de tenant, qrsgen evict el
`*Client` HTTP cacheado para ese `owner_tag`, garantizando que la
próxima request use la config nueva sin restart.

**Audit log**: cada operación de tenant queda registrada en
`bridge_audit_log` (append-only). El token jamás aparece en el audit.
