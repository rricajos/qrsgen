# Ejemplo de integración con n8n

[n8n](https://n8n.io/) es una herramienta open-source de automatización
de workflows. Es una buena referencia para integrar qrsgen porque su
modelo (HTTP webhooks + nodos HTTP request) mapea uno a uno con la API
qrsgen.

> Este documento es un ejemplo. qrsgen es agnóstico — cualquier sistema
> que hable HTTP (Zapier, Make, Temporal, app custom, scripts shell)
> sirve igualmente.

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
  "name": "whatsapp-main",
  "events_webhook_url": "https://workflows.example.com/webhook/qrsgen-events",
  "inbox_id": 90,
  "owner_tag": "tenant-acme"
}
```

`owner_tag` es opcional pero útil: lo recibes de vuelta en `/api/usage/summary`
para facturación.

Tras la respuesta, qrsgen empieza a emitir `qr_generated` cada ~20s en el
`events_webhook_url`.

### 2) Recibir lifecycle events

Webhook node escuchando en `/webhook/qrsgen-events`. El body que llega
sigue siempre este esquema:

```json
{
  "instance": "whatsapp-main",
  "event": "qr_generated",
  "occurred_at": "2026-05-26T08:15:00Z",
  "jid": "",
  "last_qr_msg_id": 12345
}
```

Algunos eventos llevan campos extra (`extras` en la tabla del README).
Ejemplo de `ban_risk`:

```json
{
  "instance": "whatsapp-main",
  "event": "ban_risk",
  "occurred_at": "2026-05-26T08:30:00Z",
  "jid": "34650367855@s.whatsapp.net",
  "alert": "high_velocity",
  "score": 0.78,
  "level": "high",
  "velocity": 45,
  "diversity": 12,
  "delivery_ratio": 0.94
}
```

A partir de aquí, el workflow decide. Switch node por `event`:

| Event | Acción típica |
|---|---|
| `qr_generated` | Fetch del PNG desde `/api/instances/.../qr` y postearlo donde el agente lo escanee. |
| `paired` / `connected` | Notificar que el técnico está operativo. |
| `reconnected` | Si quieres, mostrar pill verde "vuelve la conexión". |
| `unreachable` | Pill amarilla "buscando conexión". |
| `disconnected` / `logged_out` | Pill roja + trigger del flow de re-pareado. |
| `strike` | **Alerta inmediata** Slack/Telegram/SMS — riesgo de ban. |
| `spam_blocked` | Log + métrica. |
| `ban_risk` | Slack al supervisor: "pausa envíos masivos". |
| `outgoing_expired` | Re-encolar en tu sistema o avisar al agente que ese mensaje no llegó. |
| `backend_restarting` / `backend_started` | Pill informativa. |

### 3) Enviar mensaje saliente

Cuando el operador escribe algo en tu UI, n8n hace:

```
POST http://qrsgen:3100/api/instances/whatsapp-main/webhook
Content-Type: application/json
X-Qrsgen-Signature: sha256=<hex>   (si WEBHOOK_HMAC_SECRET activo)
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

El endpoint `/webhook` está exento del Bearer token (es el entrypoint del
downstream). Para autenticación dedicada, activa
`WEBHOOK_HMAC_SECRET` (ver [Security capa 2](security/layer-2-hmac.md)).

#### Manejar el `202 queued`

Si la instancia está disconnected (restart, blip, sesión perdida) qrsgen
devuelve `202` y encola el mensaje:

```json
{ "status": "queued", "queue_id": 7421, "expires_at": "2026-05-26T09:13:42Z" }
```

En n8n, configura el nodo HTTP Request como **"Continue on Fail"** desactivado
y trata cualquier `2xx` como éxito. Después, ramifica por
`{{$json.status}}`:

- `sent` → marca el mensaje como entregado en tu UI.
- `queued` → marca como "en cola" + opcionalmente programa un follow-up
  que revise el estado tras 1-2 min, o simplemente espera al evento
  `outgoing_expired` (que llega si no se entregó antes del TTL).

Si quieres tratar 202 como error, configura el nodo con `"Response → Error
on response"` y revisa el `status` en el catch.

### 4) Recibir mensajes entrantes

Configurable vía `DOWNSTREAM_BASE_URL` + `DOWNSTREAM_API_TOKEN` (env del
stack). qrsgen postea los `incoming` a
`<DOWNSTREAM_BASE_URL>/api/v1/accounts/<ACCOUNT_ID>/conversations/.../messages`
con un formato JSON estándar (Channel::Api-compatible). Si tu downstream
no usa ese formato/path, puedes apuntar esos vars a un webhook n8n (o de
cualquier otro proxy) que reciba el POST y lo reformatee a tu necesidad.

---

## Workflows útiles

### QR pruning automático

Cron diario que limpia instancias `qr_pending` que llevan >24h sin
escanear:

```javascript
// Code node en n8n
const tok = $env.QRSGEN_API_TOKEN;
const r = await this.helpers.request({
  url: 'http://qrsgen:3100/api/instances',
  headers: { Authorization: 'Bearer ' + tok },
  json: true,
});
const stale = [];
for (const i of r) {
  if (i.state !== 'qr_pending') continue;
  // Necesitamos el created_at — lo sacamos del GET rico
  const detail = await this.helpers.request({
    url: 'http://qrsgen:3100/api/instances/' + i.name,
    headers: { Authorization: 'Bearer ' + tok },
    json: true,
  });
  const age = (Date.now() - new Date(detail.created_at).getTime()) / 3600_000;
  if (age > 24) stale.push(i.name);
}
return stale.map(name => ({ json: { name } }));
```

Después, un HTTP Request node con `DELETE /api/instances/{{$json.name}}`.

### Reporte de facturación mensual

Cron mensual el día 1 a las 03:00 UTC:

```
GET http://qrsgen:3100/api/usage/summary?from={{lastMonth}}&to={{lastMonth}}
Headers:
  Authorization: Bearer ${QRSGEN_API_TOKEN}
```

Después, un Code node que mapea `owner_tag` a tu tenant y aplica el
pricing. Genera un PDF / CSV / mail.

### Strike alert

Webhook que recibe `event=strike` → Slack/Telegram/email:

```
IF $json.event === 'strike':
  POST a Slack con texto:
  ":rotating_light: STRIKE en *{{$json.instance}}* (JID {{$json.jid}}) — acción inmediata"
```

### Ban-risk monitor

Webhook que recibe `event=ban_risk` con `level=high` → pausa workflows
masivos:

```
IF $json.event === 'ban_risk' AND $json.level === 'high':
  - POST a Slack alertando
  - PATCH workflows automáticos a "inactive" en n8n API
```

### Health monitor

Cron cada 5 min:

```
GET http://qrsgen:3100/api/health
```

Si falla 3 veces consecutivas, alerta. Sencillo y vale por una página de
status real.

### Audit dashboard

Cron cada hora, descarga últimas 100 entradas del audit log y las indexa
en tu DB:

```
GET http://qrsgen:3100/api/audit?limit=100
Headers:
  Authorization: Bearer ${QRSGEN_API_TOKEN}
```

Filtra por `action` (`instance.create`, `outbox.expire`, etc.) para
detectar patrones (ej: muchos `outbox.expire` en una instancia → algo
no anda bien).

---

## Tips n8n específicos

- **Credentials**: usa `httpHeaderAuth` para guardar el Bearer token
  encriptado. Nunca hardcode en JSON exportable.
- **Webhook node respuesta inmediata**: si tu webhook receptor tarda
  >10s en responder, qrsgen no reintenta (no hay retry queue del
  lifecycle). Mejor responder 200 rápidamente y procesar async.
- **Switch node por `event`**: estructura limpia para ramificar lifecycle.
  Cada rama un sub-workflow.
- **Workflow tags**: etiqueta los workflows de qrsgen con un tag común
  (`qrsgen`) para que sea fácil pausar todos a la vez en una alerta de
  `ban_risk`.
