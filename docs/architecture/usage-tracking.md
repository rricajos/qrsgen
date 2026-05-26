# Usage tracking + monetización

`internal/usage` incrementa contadores in-memory en cada send/receive y
flush-ea a `bridge_usage_daily` cada 60s con UPSERT. Si Postgres está
temporalmente caído, los deltas se preservan para el siguiente tick.

## Contadores

Por `(instance, day)`:

- `messages_in` — incrementado en `incoming.Handle` tras POST exitoso al
  downstream.
- `messages_out` — incrementado en `outgoing.HandleFor` tras
  SendText/SendMedia OK.
- `spamguard_blocks` — incrementado cuando el spamguard descarta un
  duplicado.
- `lifecycle_events` — incrementado cada vez que se POSTea un lifecycle
  webhook con éxito.

## Endpoints

- `GET /api/instances/:name/usage` — días para una instancia.
- `GET /api/usage` — días para todas (dashboard).
- `GET /api/usage/summary` — agregado mensual por `(owner_tag, mes)`.

Esta última es la query típica de billing: el integrador mapea
`owner_tag` a su tenant y suma los counters que tarifique.

## qrsgen NO toma decisiones de pricing

El sistema solo expone hechos. Quién, qué tarifa, cuánto se multiplica,
qué se descuenta — eso es responsabilidad del integrador. Esta separación
permite que qrsgen sirva múltiples modelos de negocio (per-instance,
per-message, per-conversación, suscripción) sin saber nada sobre ellos.

## Resilience

`usage.Tracker.Flush()` es best-effort: si falla, **re-buffera los
deltas** y reintenta en el siguiente tick. No se pierden cuentas por
flapping de Postgres ni por restarts (se hace un flush final en
shutdown).

## Multi-tenant ligero

Combinado con el campo `owner_tag` en `bridge_instance`, el usage summary
soporta **multi-tenant ligero**: un solo proceso qrsgen sirviendo varios
clientes, cada uno identificado por su `owner_tag`. El reporte mensual
agrupa naturalmente y permite billing por cliente sin más infraestructura.

## Glosario

**Usage tracking**: registro de qué hizo cada instancia en una unidad de
tiempo (en qrsgen, día UTC). Datos: mensajes in/out, spam blocks,
lifecycle events.

**Flush**: operación que toma los contadores en memoria y los persiste
a DB. qrsgen lo hace cada 60s + un flush final al shutdown.

**UPSERT**: operación SQL que es INSERT si la fila no existe, UPDATE si
ya existe. Postgres lo implementa con `INSERT ... ON CONFLICT DO UPDATE`.
qrsgen lo usa para mantener `bridge_usage_daily` actualizado idempotente.

**Idempotente** (en el contexto de flush): repetir un flush con los
mismos counters produce el mismo resultado en DB. Importante para
recovery sin doble-conteo.

**owner_tag**: string libre del integrador para mapear instancias a
tenants. qrsgen lo expone en `/api/usage/summary` agrupado para
facturación, pero NO toma decisiones de pricing.

**Multi-tenant ligero**: arquitectura donde un solo proceso sirve a
varios clientes (tenants), identificándolos solo por una etiqueta. Sin
aislamiento real de datos — depende de la confianza en el integrador.
Distinto de multi-tenant fuerte (DB separada por tenant, etc.).

**Billing**: facturación. qrsgen genera datos (contadores agregados);
quién factura qué y cuánto es decisión del integrador.

**Best-effort flush**: si el flush falla (DB caída transitoriamente),
qrsgen no aborta — re-buffera los deltas para reintento en el próximo
tick. Cero pérdida en flapping.

**Day UTC**: día calendario en zona UTC. qrsgen usa UTC para agregar
counters porque evita ambigüedades con cambios de hora y horarios
locales del operador.
