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

## Glosario

**Flujo end-to-end**: secuencia completa de llamadas API necesarias
para completar un caso de uso (provision, send, recovery, billing,
etc.).

**Provisión + pairing**: secuencia inicial donde se crea una instancia
y el usuario escanea el QR para vincular su número WhatsApp. Solo se
hace la primera vez.

**Recovery flow** (sesión perdida): secuencia para regenerar el pairing
cuando WhatsApp invalidó la sesión server-side (`logged_out`). Requiere
nuevo escaneo del usuario.

**Borrado limpio**: secuencia `logout` + `DELETE`. El `logout` invalida
la sesión en Meta servers; el `DELETE` borra la fila de
`bridge_instance`. El audit log conserva la traza.

**Billing al cierre de mes**: query típica para facturación —
`/api/usage/summary` con el mes actual, agrupado por `owner_tag`.

**Forensics**: investigación post-incidente combinando `/api/audit`
(quién hizo qué), `/api/usage` (volumen de actividad) y `/api/ban-risk`
(estado actual de las señales).
