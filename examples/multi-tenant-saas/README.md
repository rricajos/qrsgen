# Multi-tenant SaaS recipe

Receta end-to-end para usar qrsgen como backend de un SaaS WhatsApp
con varios clientes finales (tenants), cada uno con su downstream
propio (Chatwoot privado, app propietaria, etc.).

Combina los endpoints multi-tenant introducidos en v0.24.0–v0.26.0:

- `bridge_tenant` table (config downstream por `owner_tag`).
- `/api/tenants/*` CRUD (PUT/PATCH/DELETE).
- `bridge_instance.owner_tag` enlaza cada instancia a un tenant.
- `webhook_hmac_secret` per-tenant para aislar credenciales.
- Prometheus labels `owner_tag` para split per-cliente en Grafana.
- `/api/audit?owner_tag=` para que un admin de tenant solo vea lo suyo.

## Arquitectura objetivo

```
Cliente A (acme)                Cliente B (globex)
 │                               │
 │ tenant_id=tenant-acme         │ tenant_id=tenant-globex
 │ downstream=acme.chatwoot.io   │ downstream=globex.example.com
 │ hmac=acme-secret              │ hmac=globex-secret
 ▼                               ▼
┌─────────────────────────────────────────┐
│            qrsgen (un proceso)          │
│  bridge_tenant ─ tenant-acme    │       │
│                 ─ tenant-globex │       │
│  bridge_instance                │       │
│    acme-main      → tenant-acme │       │
│    acme-sales     → tenant-acme │       │
│    globex-support → tenant-globex│      │
└─────────────────────────────────────────┘
```

Un solo binario qrsgen sirve a ambos clientes, enrutando cada
mensaje al downstream correcto.

## Setup paso a paso

### 1. Provisionar tenants

`tenants.json` (un objeto por cliente):

```json
[
  {
    "owner_tag": "tenant-acme",
    "downstream_base_url": "https://acme.chatwoot.io",
    "downstream_api_token": "cw_acme_xxx",
    "downstream_account_id": 1,
    "downstream_inbox_id": 12,
    "webhook_hmac_secret": "ACME_HMAC_SECRET_32B_RANDOM"
  },
  {
    "owner_tag": "tenant-globex",
    "downstream_base_url": "https://globex.example",
    "downstream_api_token": "cw_globex_yyy",
    "downstream_account_id": 1,
    "downstream_inbox_id": 3,
    "webhook_hmac_secret": "GLOBEX_HMAC_SECRET_32B_RANDOM"
  }
]
```

Aplicar:

```bash
QRSGEN_URL="https://qrsgen.example.com"
TOKEN="$QRSGEN_API_TOKEN"

jq -c '.[]' tenants.json | while read -r t; do
  TAG=$(echo "$t" | jq -r .owner_tag)
  curl -sf -X PUT "$QRSGEN_URL/api/tenants/$TAG" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "$t" | jq .
done
```

### 2. Provisionar instancias (cada una con su owner_tag)

`instances.json`:

```json
[
  {"name": "acme-main",      "owner_tag": "tenant-acme",   "events_webhook_url": "https://orchestrator.example.com/webhook/qrsgen-events"},
  {"name": "acme-sales",     "owner_tag": "tenant-acme",   "events_webhook_url": "https://orchestrator.example.com/webhook/qrsgen-events"},
  {"name": "globex-support", "owner_tag": "tenant-globex", "events_webhook_url": "https://orchestrator.example.com/webhook/qrsgen-events"}
]
```

Aplicar con el helper del repo (recomendado — idempotente, multi-thread):

```bash
python3 ../../tools/migrate/bulk-provision.py \
  --url "$QRSGEN_URL" --token "$TOKEN" --plan instances.json
```

O manualmente:

```bash
jq -c '.[]' instances.json | while read -r i; do
  curl -sf -X POST "$QRSGEN_URL/api/instances" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" -d "$i"
done
```

### 3. Verificar el routing

```bash
# Cada instancia debe reportar su owner_tag correcto.
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$QRSGEN_URL/api/instances/acme-main" | jq .owner_tag
# → "tenant-acme"
```

### 4. Pairing (cada cliente escanea su QR)

WhatsApp obliga a re-pairing por device — no es automatizable. Cada
cliente abre el QR de SUS instancias y lo escanea con su móvil.

Para el flow recomendado en chat downstream, ver:
[../n8n-basic/README.md](../n8n-basic/README.md).

### 5. Observabilidad per-tenant

Métricas Prometheus llevan `owner_tag` automáticamente:

```promql
# Mensajes salientes en última hora por tenant
sum by (owner_tag) (increase(qrsgen_messages_total{direction="out"}[1h]))

# Errores por kind, separado por tenant
sum by (owner_tag, kind) (rate(qrsgen_message_dispatch_errors_total[5m]))
```

Importar [`../grafana-dashboard/dashboard.json`](../grafana-dashboard/)
para verlo gráfico.

Audit por tenant:

```bash
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$QRSGEN_URL/api/audit?owner_tag=tenant-acme&limit=50" | jq .
```

### 6. Billing

`/api/usage/summary` ya agrupa por `owner_tag` desde v0.23.0:

```bash
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$QRSGEN_URL/api/usage/summary?month=2026-05" | jq .
```

Output:

```json
[
  {"owner_tag": "tenant-acme",   "messages_in": 12453, "messages_out": 8901,  "lifecycle_events": 47},
  {"owner_tag": "tenant-globex", "messages_in": 4521,  "messages_out": 3120,  "lifecycle_events": 22}
]
```

Cada fila es directamente facturable.

## Rotación de credenciales

Si necesitas rotar el HMAC secret de un tenant sin downtime, usa
PATCH (PUT borraría otros campos si no los reenvías):

```bash
curl -sf -X PATCH "$QRSGEN_URL/api/tenants/tenant-acme" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"webhook_hmac_secret":"NEW_ROTATED_SECRET"}'
```

qrsgen invalida el cache `*Client` inmediatamente.

## Aislamiento que esto NO da

- **DB separada por tenant**: todas las filas viven en las mismas
  tablas. Un DBA comprometido puede leer todos los datos.
- **Proceso separado**: un crash del proceso afecta a todos los
  tenants. Para isolation real, un proceso qrsgen por tenant es lo
  recomendado.

Lo que SÍ da:
- Routing aislado por mensaje (cada uno va a su downstream).
- HMAC verify aislado (un secret comprometido no afecta a otros).
- Métricas + audit + billing separados por etiqueta.

Para multi-tenant "serio" con isolation real, el roadmap apunta a
outbox encryption per-tenant (KEK/DEK) + DB schemas separados — ver
[security/pending.md](../../docs/security/pending.md).
