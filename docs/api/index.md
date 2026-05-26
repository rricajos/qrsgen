# API HTTP — Visión general

qrsgen expone una API REST en `:3100` sobre la overlay LAN (alias `qrsgen`
o `bridge_bridge`). No tiene DNS público — todo acceso pasa por
containers del mismo overlay.

## Navegación

- [Quickstart](quickstart.md) — provision → QR → primer mensaje, end-to-end.
- [Convenciones](conventions.md) — autenticación, HMAC, headers, códigos de error.
- [Instancias](instances.md) — `/api/instances/*` CRUD.
- [QR y ciclo de vida](qr-lifecycle.md) — `/qr`, `/wait-ready`, `/refresh-qr`, `/restart`, `/logout`.
- [Mensajes](messages.md) — `POST /api/instances/:name/webhook` y el schema `WebhookPayload`.
- [Observabilidad](observability.md) — `/usage`, `/outbox`, `/ban-risk`, `/audit`, `/metrics`, `/health`.
- [Lifecycle webhooks](lifecycle-webhooks.md) — esquema de los POSTs que qrsgen envía a `events_webhook_url`.
- [Flujos comunes](flows.md) — recetas end-to-end (provision, send, recovery, billing, strike).

## Glosario

**API REST**: estilo de API donde cada recurso se identifica con una URL
y se opera con verbos HTTP (`GET`, `POST`, `PATCH`, `DELETE`). qrsgen
expone su funcionalidad así.

**Overlay LAN**: red privada virtual que Docker Swarm crea entre los
hosts de un cluster. Los containers de un mismo overlay se ven entre sí
por nombre (`qrsgen:3100`) sin pasar por internet.

**Alias**: nombre alternativo que un container tiene dentro del overlay.
qrsgen responde tanto como `qrsgen` como `bridge_bridge` (compat con
workflows antiguos).

**Endpoint**: URL concreta de la API que sirve una operación. Por
ejemplo, `POST /api/instances` es el endpoint para crear una instancia
nueva.

**Bearer token**: tipo de credencial HTTP que se envía en el header
`Authorization: Bearer <token>`. qrsgen lo usa para proteger la API
contra accesos no autorizados desde dentro del overlay.

**Webhook**: URL receptora HTTP donde un sistema envía notificaciones de
forma asíncrona. qrsgen tiene un endpoint webhook (`POST /webhook`) y
también POSTea webhooks a tu sistema (lifecycle events).
