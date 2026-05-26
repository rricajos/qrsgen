# Flujo OUTGOING (tu sistema → cliente WhatsApp)

```
Tu sistema decide enviar un msg al cliente
        │
        ▼
POST http://qrsgen:3100/api/instances/<INSTANCE_NAME>/webhook
   Headers: Content-Type: application/json
            X-Qrsgen-Signature: sha256=<hex>   (opcional, si WEBHOOK_HMAC_SECRET set)
   Body: {
     "event": "message_created",
     "message_type": "outgoing",
     "content": "Hola...",
     ...
   }
        │
        ▼
api.POST("/instances/:name/webhook"):
        │ leer raw body
        │ HMAC middleware (si secret configurado)
        │ ¿mgr.IsConnected(instance)?
        │
        │     SI            NO
        │     │              │
        │     ▼              ▼
        │  HandleFor    outbox.Enqueue → 202 {status:"queued", queue_id, expires_at}
        │     │
        │     ▼
        │  bridge/outgoing.go HandleFor():
        │    - skip si message_type != "outgoing"
        │    - skip si private=true
        │    - skip si source_id startswith "WAID:" (eco)
        │    - skip si remoteJid startswith "qrsgen-qr-" (ops contact)
        │    - dedup por msg_id
        │    - spamguard.CheckAndRecord → si dup: emit "spam_blocked"
        │    - banwatch.Record(success|failure)
        │    - sender.SendText/SendMedia → whatsmeow → Meta
        │    - PATCH source_id="WAID:..." en downstream
        │     │
        │     ▼
        │  200 {status:"sent"}
        ▼
Cliente WhatsApp recibe el msg
```

## Outbox: el cor de "cero pérdida en restarts"

Cuando `mgr.IsConnected(instance) == false` (restart, blip, sesión
perdida), qrsgen **persiste el payload crudo** en `bridge_outgoing_queue`
con `expires_at = NOW() + TTL (default 5 min)` y devuelve
`202 {status: "queued", queue_id: N, expires_at}`.

Dos goroutines lo manejan:

- **drainer** (cada 5s): selecciona pending para instancias que
  `IsConnected()` ahora y las entrega via `bridge.Outgoing.HandleForRaw`.
  Cada fail incrementa `attempts`; cuando llega a 5 → status `failed` +
  audit entry.
- **expirer** (cada 30s): marca como `expired` las pending pasadas su
  `expires_at` y emite el evento lifecycle `outgoing_expired` con un
  preview del contenido.

## Safety nets

Para evitar enviar mensajes erróneos a WhatsApp, `HandleFor` aplica
filtros que hacen no-op:

- `message_type != "outgoing"`.
- `private == true` (nota privada, no se envía a WhatsApp).
- `source_id` empieza con `"WAID:"` (eco del propio mensaje).
- `remoteJid` empieza con `"qrsgen-qr-"` (contacto sintético del panel
  ops).
- Spamguard activado + contenido duplicado → emit `spam_blocked` + skip.

## Per-instance MaxQueueDepth

`outbox.MaxQueueDepth` (default 200) limita el backlog por instancia.
Cuando se alcanza, nuevos POSTs devuelven `503` en lugar de encolar más.
Evita runaway buffering cuando una instancia está permanentemente muerta.
