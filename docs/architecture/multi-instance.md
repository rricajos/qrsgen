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

## Glosario

**Multi-instance**: arquitectura donde un solo proceso gestiona varias
sesiones WhatsApp independientes. Cada instancia = un número.

**Instance name**: identificador único de una instancia dentro del
proceso. Aparece en URLs (`/api/instances/<name>/...`) y en logs.

**Routing**: proceso de determinar a qué instancia va una request o un
evento. Por nombre de URL (outgoing) o por sesión WhatsApp (incoming).

**Aislamiento entre instancias**: cada instancia tiene su propio
WebSocket, sus propios buffers y su propia config en DB. No comparten
estado en memoria (solo el pool Postgres compartido).

**Namespace** (en este contexto): conjunto de nombres dentro del cual
los identificadores son únicos. Cada proceso qrsgen tiene su propio
namespace de nombres de instancia.

**owner_tag**: string libre del integrador para correlacionar instancias
con su modelo de tenants. qrsgen lo lee solo en el agregado de
facturación.

**Multi-tenant ligero**: arquitectura donde un proceso sirve varios
clientes identificándolos solo por etiqueta (`owner_tag`), sin
aislamiento real. Bueno cuando el integrador es de confianza.

**Multi-tenant fuerte**: arquitectura donde cada cliente tiene
aislamiento real (DB separada, proceso separado, secrets separados).
qrsgen no lo soporta nativamente — workaround: un proceso por cliente.

**Multi-downstream**: capacidad de servir varios destinos downstream
distintos desde un solo proceso qrsgen. NO soportado actualmente —
`DOWNSTREAM_BASE_URL` y `DOWNSTREAM_API_TOKEN` son globales.
