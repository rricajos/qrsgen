# Por dónde empezar

Tu camino depende de qué quieras hacer:

## Quiero integrar qrsgen en mi sistema

→ Ve directo a [API · Quickstart](../api/quickstart.md). Tienes el
flujo provision → QR → primer mensaje en 20 líneas de curl.

Después, lee:

- [API · Convenciones](../api/conventions.md) para auth y HMAC.
- [API · Mensajes](../api/messages.md) para el detalle del
  `WebhookPayload`.
- [API · Lifecycle webhooks](../api/lifecycle-webhooks.md) para los
  eventos que tu sistema debe recibir.

Si usas n8n o Python, hay recetas listas en
[Integrations](../integrations/).

## Quiero entender cómo funciona internamente

→ [Arquitectura](../architecture/) cubre los flujos, las tablas y la
concurrencia.

Empieza por el [overview](../architecture/index.md), después
[Bootstrap](../architecture/bootstrap.md) para entender el ciclo de
arranque, y luego los flujos
[INCOMING](../architecture/incoming-flow.md) y
[OUTGOING](../architecture/outgoing-flow.md).

## Quiero desplegarlo

→ [Deployment](../deployment/) con la opción que prefieras:

- [Imagen GHCR pre-built](../deployment/images.md) (recomendado).
- [Build local desde el repo](../deployment/images.md).
- [Binario nativo](../deployment/images.md).

Después configura el [stack Swarm](../deployment/swarm.md) con las
[variables de entorno](../deployment/env-vars.md) y opcionalmente
expón la [telemetría pública](../deployment/public-stats.md).

## Quiero operarlo en producción

→ [Operations](../operations/) tiene el runbook completo:

- [Diagnóstico rápido](../operations/diagnostics.md) — health,
  instances, mensajes, ban-risk.
- [Procedimientos comunes](../operations/procedures.md) — re-pareado,
  restart, billing.
- [Troubleshooting](../operations/troubleshooting.md) — errores
  típicos y cómo resolverlos.
- [Alerting](../operations/alerting.md) — reglas Prometheus
  sugeridas.

## Quiero entender el modelo de seguridad

→ [Security](../security/) describe las 7 capas con su modelo de
amenaza, configuración y verificación. Empieza por el
[overview](../security/index.md).
