# Flujo INCOMING (cliente WhatsApp → tu sistema)

```
Cliente WhatsApp envía msg al número conectado
        │
        ▼
Meta enruta al WebSocket activo del JID destino
        │
        ▼
WebSocket que qrsgen mantiene abierto desde bootstrap
        │ whatsmeow emite events.Message
        ▼
wameow.handle() → callback onMessage
        │
        ▼
bridge/incoming.go:
        │ resuelve LID↔PN si aplica (Multi-Device)
        │ si fromMe=true: dedup.ShouldDrop() para evitar twin
        │ construye payload {content, attachments, source_id: WAID:..., ...}
        │ POST al endpoint downstream
        ▼
Tu sistema recibe el POST y procesa
        │
        └─ usage.IncIn(instance)
        └─ metric qrsgen_messages_total{direction="in"}++
```

## LID/PN twin dedup

WhatsApp Multi-Device puede entregar el mismo mensaje desde un cliente
tanto via su JID PN (número) como su LID (identificador anónimo).
qrsgen detecta y descarta el twin via `bridge_dedup` con clave
`(instance, jid_user, content_hash)` y ventana configurable
(`DEDUP_WINDOW_MS`, default 10s).

## Routing al downstream

El POST se hace contra `DOWNSTREAM_BASE_URL/api/v1/accounts/<ACCOUNT_ID>/conversations/...`.
El path es Channel::Api-compatible, pero el endpoint cliente HTTP es
genérico — puedes apuntar `DOWNSTREAM_BASE_URL` a un proxy/webhook que
reformatee a otro shape si tu downstream no usa Channel::Api.

`inbox_id` se obtiene de `bridge_instance.inbox_id` para esa instancia;
si está `NULL` o `0`, cae al `DOWNSTREAM_INBOX_ID` global.
