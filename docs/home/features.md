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

## Glosario

**Outbox**: cola persistida en Postgres donde van los mensajes outgoing
cuando la instancia está temporalmente desconectada. Se reentregan al
volver, con TTL de 5 minutos.

**TTL** (Time To Live): tiempo máximo que un mensaje puede esperar en
la outbox antes de expirar.

**BanWatcher**: módulo interno que analiza el ritmo de envíos para
detectar patrones que WhatsApp suele penalizar.

**Velocity**: mensajes outgoing por unidad de tiempo. Una de las tres
señales del BanWatcher.

**Diversity**: número de JIDs únicos contactados por ventana de
tiempo. Otra señal del BanWatcher.

**Delivery ratio**: fracción de envíos exitosos sobre intentos
totales. Tercera señal del BanWatcher.

**Audit log**: tabla `bridge_audit_log` con triggers DB que rechazan
UPDATE/DELETE — registro inmutable de operaciones.

**Tamper-evident**: propiedad donde cualquier modificación al log es
detectable o imposible. qrsgen lo garantiza a nivel DB.

**Usage tracking**: contadores diarios de mensajes y eventos por
instancia, persistidos en Postgres para reporting/facturación.

**owner_tag**: string libre para mapear instancias a tenants
(clientes). qrsgen lo expone en agregados de billing pero no lo
interpreta.

**Multi-tenant ligero**: arquitectura donde un solo proceso sirve a
varios clientes identificándolos solo por etiqueta.

**HMAC** (Hash-based Message Authentication Code): firma criptográfica
que demuestra que un mensaje viene de quien dice y no ha sido
modificado.

**Distroless**: imagen Docker mínima sin shell ni package manager.
Reduce la superficie de ataque ante RCE.

**Read-only rootfs**: filesystem del container marcado como solo
lectura. Cualquier intento de escribir falla — buena señal de
compromiso si ocurre.

**Lifecycle event**: notificación HTTP que qrsgen POSTea cuando ocurre
algo relevante en una instancia (conexión, QR, ban risk, etc.).
