# Ejemplo de integración con n8n

[n8n](https://n8n.io/) es una herramienta open-source de automatización de workflows. Es una buena referencia para integrar qrsgen porque su modelo (HTTP webhooks + nodos HTTP request) mapea uno a uno con la API qrsgen.

> Este documento es un ejemplo. qrsgen es agnóstico — cualquier sistema que hable HTTP (Zapier, Make, Temporal, una app custom, scripts shell) sirve igualmente.

## Receta básica

### 1) Provisionar una nueva instancia

n8n HTTP Request node:

```
POST http://qrsgen:3100/api/instances
Headers:
  Authorization: Bearer ${QRSGEN_API_TOKEN}
  Content-Type: application/json
Body:
{
  "name": "TECNICO-1",
  "events_webhook_url": "https://workflows.example.com/webhook/qrsgen-events",
  "inbox_id": 90
}
```

Tras la respuesta, qrsgen empieza a emitir `qr_generated` cada ~20s en el `events_webhook_url`.

### 2) Recibir lifecycle events

Webhook node escuchando en `/webhook/qrsgen-events`. El body que llega tiene la forma:

```json
{
  "instance": "TECNICO-1",
  "event": "qr_generated",
  "jid": "",
  "occurred_at": "2026-05-22T08:15:00Z",
  "last_qr_msg_id": 12345
}
```

A partir de aquí, tu workflow decide:
- Si `event === "qr_generated"` → fetch del PNG desde `/api/instances/TECNICO-1/qr` + postear donde te interese.
- Si `event === "connected"` → notificar que el técnico está operativo.
- Si `event === "strike"` → alerta inmediata (riesgo de ban).
- etc.

### 3) Enviar mensaje saliente

Cuando el operador escribe algo en tu UI, n8n hace:

```
POST http://qrsgen:3100/api/instances/TECNICO-1/webhook
Content-Type: application/json
Body:
{
  "event": "message_created",
  "message_type": "outgoing",
  "content": "Hola, ¿cómo te atendieron?",
  "conversation": {
    "id": 100,
    "meta": {
      "sender": {
        "identifier": "34600000000@s.whatsapp.net"
      }
    }
  },
  "id": 12345,
  "private": false
}
```

El endpoint `/webhook` está exento del Bearer token (es el entrypoint de tu downstream).

### 4) Recibir mensajes entrantes

Configurable vía `DOWNSTREAM_BASE_URL` + `DOWNSTREAM_API_TOKEN` (env del stack). qrsgen postea los `incoming` a `<DOWNSTREAM_BASE_URL>/api/v1/accounts/<ACCOUNT_ID>/conversations/.../messages` con un formato JSON estándar. Si tu downstream no usa ese formato/path, puedes apuntar esos vars a un webhook n8n (o de cualquier otro proxy) que reciba el POST y lo reformatee a tu necesidad.

## Ideas de workflows útiles

- **QR pruning**: cron diario que llame `/api/instances` y borre las que llevan >24h en `state="qr_pending"`.
- **Strike alert**: webhook `event=strike` → POST a Slack/Telegram/email.
- **Usage tracking**: webhook `event=connected` → contar QRs activos en tu DB para facturación.
- **Health monitor**: cron cada 5 min → `/api/health` → si falla 3 veces, alerta.
