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
