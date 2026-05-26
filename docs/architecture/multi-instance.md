# Multi-instance routing

## Incoming

```
1. whatsmeow sabe la instancia (por el WebSocket que recibió el evento).
2. mgr.InboxIDFor("whatsapp-main") → query DB → inbox_id = N.
3. POST al downstream con ese inbox_id en el payload.
```

## Outgoing

```
1. URL del webhook contiene /api/instances/whatsapp-main/webhook
   → instancia parseada por Echo (`:name` param).
2. Si IsConnected(whatsapp-main) → Conn.SendText.
   Si no → outbox.Enqueue.
```

## Aislamiento entre instancias

Multi-instance funciona en el mismo proceso qrsgen. Cada instancia tiene:

- Su **WebSocket independiente** contra Meta.
- Su **bucket spamguard separado** (history last-2 propio).
- Su **buffer banwatch propio** (velocity/diversity/delivery por
  instancia).
- Su **fila en `bridge_instance`** con su config (`inbox_id`,
  `events_webhook_url`, `owner_tag`, `spamguard_enabled`, etc.).

Solo se comparte:

- El pool `pgxpool.Pool` (Postgres connections).
- Las métricas Prometheus (labels distinguen por `instance`).
- El audit log (todas escriben a la misma tabla).
- El usage tracker (granularidad `(instance, day)` lo separa).

## owner_tag para multi-tenant ligero

El campo `bridge_instance.owner_tag` (string libre) permite que el
integrador correlacione instancias con su modelo de tenants:

```
tenant-acme    → whatsapp-main, whatsapp-sales
tenant-globex  → whatsapp-support
```

El reporte `GET /api/usage/summary` agrupa naturalmente por
`(owner_tag, mes)`, listo para facturar.

## Multi-downstream NO soportado (aún)

`DOWNSTREAM_BASE_URL` y `DOWNSTREAM_API_TOKEN` son **globales por
proceso**. Para servir varios downstreams desde un solo qrsgen habría que
enrutar por `owner_tag` y mantener un mapa de clientes HTTP. Pendiente.

Workaround actual: un proceso qrsgen por downstream, todos apuntando al
mismo Postgres (los nombres de instancia separan los namespaces).
