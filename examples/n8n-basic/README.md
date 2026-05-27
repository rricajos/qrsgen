# n8n basic integration

Tres workflows JSON listos para importar en cualquier instancia n8n.
Demuestran el flujo lifecycle más típico — recibir eventos qrsgen
(qr_generated, connected, logged_out…) y postearlos a Chatwoot u otro
sistema downstream.

## Importar

En n8n: **Workflows → Import from File** → seleccionar uno de los
`.workflow.json` de esta carpeta.

Tras importar, los nodos `httpRequest` que apuntan a Chatwoot
necesitarán credenciales propias (HTTP Header Auth con la API key del
downstream). El nodo Webhook expone un path que tendrás que copiar a
`events_webhook_url` cuando provisionas instancias en qrsgen.

## Workflows

### `qrsgen_lifecycle_notifier.workflow.json`

Webhook listener que recibe eventos lifecycle de qrsgen y los traduce
a mensajes en una conversación del downstream (Chatwoot, p.ej.).
Mapea cada evento (`qr_generated`, `connected`, `unreachable`,
`logged_out`, etc.) a un emoji + texto descriptivo.

**Punto de entrada**: `POST {n8n}/webhook/qrsgen-events`

Configurar como `events_webhook_url` al crear instancia:

```bash
curl -X POST $QRSGEN_URL/api/instances \
  -H "Authorization: Bearer $QRSGEN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"whatsapp-main","events_webhook_url":"https://n8n.example.com/webhook/qrsgen-events"}'
```

### `qrsgen_qrping.workflow.json`

Health-check periódico que llama a `GET /api/health` y alerta al
canal de soporte si la respuesta no es 200 OK. Cron 1m.

### `qrsgen_qrv2.workflow.json`

Bot comando `/qrv2 <instance_name>` — desde una conversación del
downstream, un agente escribe `/qrv2 SAT-MARC` y el bot regenera el
QR llamando a `POST /api/instances/SAT-MARC/refresh-qr`.

## Estos workflows son genéricos

No dependen de "Omnia" ni de ningún bot concreto. Son la versión
sample para integradores. Si quieres una UX más pulida (status
detallado, comando `qr` natural, métricas conversacionales) escribe
tu propio set encima — qrsgen solo emite los eventos.

## Alternativa sin n8n

Puedes consumir los webhooks lifecycle desde cualquier endpoint HTTP.
Ver:

- [`../python/`](../python/) — receiver Python en FastAPI (~80 líneas).
- [`../node/`](../node/) — receiver Node en HTTP nativo (~30 líneas).
