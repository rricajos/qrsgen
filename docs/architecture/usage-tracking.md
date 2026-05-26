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
