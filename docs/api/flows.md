# Flujos comunes

Recetas end-to-end combinando varios endpoints.

## Provisión + primer pairing

```
POST /api/instances              { name, events_webhook_url, inbox_id, owner_tag? }
   ↓ event: qr_generated → events_webhook_url
GET  /api/instances/:name/qr     (PNG, mostrar al usuario)
   ↓ usuario escanea
   ↓ event: paired → events_webhook_url
   ↓ event: connected → events_webhook_url
   ↓ ya puedes enviar mensajes
```

Alternativa: `GET /api/instances/:name/wait-ready?timeout=120` bloquea
hasta `ready` sin necesidad de polling.

## Enviar un mensaje

```
POST /api/instances/:name/webhook   { message_type:"outgoing", content, ... }
   → 200 {status:"sent"}         si la instancia está conectada
   → 202 {status:"queued", ...}  si está reconectando (outbox)
```

## Sesión perdida (logged_out)

```
event: logged_out → events_webhook_url
POST /api/instances/:name/refresh-qr
   ↓ event: qr_generated → events_webhook_url
GET  /api/instances/:name/qr     (PNG nuevo)
   ↓ usuario escanea
   ↓ event: paired / connected
```

## Borrado limpio

```
POST   /api/instances/:name/logout    (invalida server-side)
DELETE /api/instances/:name           (borra bridge_instance row + audit)
```

## Billing al cierre de mes

```
GET /api/usage/summary?from=2026-05&to=2026-05
   ↓ iterar rows agrupando por owner_tag → tarifar
```

## Investigar un strike

```
GET /api/audit?instance=whatsapp-main&limit=200
   ↓ buscar entradas anteriores al evento "strike"
GET /api/instances/whatsapp-main/usage?from=...&to=...
   ↓ ver pico de envíos
GET /api/instances/whatsapp-main/ban-risk
   ↓ ver snapshot actual
```
