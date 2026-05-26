# Características destacadas

## Multi-instancia real

Un binario gestiona N números independientes, cada uno con su WebSocket
contra Meta y su estado aislado. Sin proceso separado por instancia.

## Outbox persistido

Cola en Postgres con TTL de 5 minutos. Cuando una instancia está
disconnected y llega un outgoing, qrsgen lo encola y lo entrega al
volver. **Cero pérdida en restarts cortos.**

## BanWatcher proactivo

Tres señales con thresholds configurables:

- **Velocity**: mensajes por minuto.
- **Diversity**: JIDs únicos contactados por ventana.
- **Delivery ratio**: éxitos / intentos.

Score 0-1 + level cualitativo. Emite el evento `ban_risk` cuando cruza
umbrales, para que tu sistema reduzca ritmo antes de que WhatsApp
sancione.

## Audit log inmutable

Tabla `bridge_audit_log` con triggers PL/pgSQL que rechazan UPDATE y
DELETE. Cualquier operación queda registrada con timestamp y metadata
JSONB. **Tamper-evident a nivel DB.**

## Usage tracking + facturación

Counters diarios persistidos en `bridge_usage_daily`. Endpoint
`/api/usage/summary` agrega por `(owner_tag, mes)` listo para billing
multi-tenant ligero. qrsgen no decide pricing — solo expone los hechos.

## HMAC opcional del webhook

`WEBHOOK_HMAC_SECRET` activa firma HMAC-SHA256 obligatoria en el
endpoint `/webhook`. Previene inyecciones desde dentro del overlay
LAN.

## Read-only rootfs

El container corre con filesystem read-only + tmpfs en `/tmp`. Imagen
distroless sin shell ni package manager. Un atacante con RCE no puede
instalar herramientas, persistir implantes ni escalar a root.

## Backups Postgres automatizados

Systemd timer diario con retención 7 días / 4 semanas. Runbook de
restore incluido. Off-site backup configurable con un `ExecStartPost=`.

## 12 lifecycle events

Conexión, desconexión, ban risk, outbox expirations... cada uno se
emite como webhook HTTP a la URL que configures por instancia. Catálogo
completo en [Lifecycle webhooks](../api/lifecycle-webhooks.md).
