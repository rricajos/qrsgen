# Integrations

qrsgen es agnóstico del stack que lo consuma: cualquier sistema que
hable HTTP puede integrarse. Esta sección recopila recetas concretas
para los integradores más comunes.

## Páginas

- **[n8n](n8n.md)** — workflow open-source no-code. Es la integración de
  referencia porque su modelo (HTTP webhooks + nodos request) mapea
  uno a uno con la API qrsgen.
- **[Python (httpx + FastAPI)](python.md)** — cliente para integrarlo en
  scripts o servicios propios. Incluye listener de webhooks para recibir
  lifecycle events.

## Patrones comunes a todas las integraciones

Independientemente del orquestador, hay tres flujos que siempre
implementarás:

1. **Provision** — `POST /api/instances` con `events_webhook_url`
   apuntando a tu sistema. Tras esto qrsgen empieza a emitir
   `qr_generated`.
2. **Receive lifecycle events** — tu sistema expone un webhook
   receptor que ramifica por `event` (ver
   [lifecycle webhooks](../api/lifecycle-webhooks.md)).
3. **Send outgoing** — `POST /api/instances/:name/webhook` con
   `message_type: outgoing` + `content` + `conversation.meta.sender.identifier`.
   Respuesta 200 (enviado) o 202 (encolado en outbox).

Cualquier orquestador (Zapier, Make, Temporal, app custom, scripts
Bash...) sirve igual — solo necesitas:

- Capacidad de hacer requests HTTP.
- Endpoint receptor para los webhooks de qrsgen.
- Algún sitio donde guardar el `QRSGEN_API_TOKEN` (Bearer auth).

## Glosario

**Integración**: software que conecta qrsgen con tu modelo de negocio.
Puede ser un orquestador (n8n, Zapier), una app web propia, un script,
o un servicio dedicado.

**Webhook receptor**: endpoint HTTP de tu sistema que escucha los
eventos lifecycle de qrsgen. Va en el campo `events_webhook_url` de la
instancia.

**Idempotente**: que repetir la operación produce el mismo efecto que
hacerla una vez. Importante en tu webhook receptor — qrsgen puede emitir
el mismo lifecycle event 2 veces en casos extremos.

**Bearer token**: credencial de auth para `/api/*` (no `/webhook`).
Guárdalo en el mecanismo de credenciales de tu orquestador, no
hardcodeado.
