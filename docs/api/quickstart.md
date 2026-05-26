# Quickstart

End-to-end desde cero hasta el primer mensaje enviado:

```bash
TOK="$QRSGEN_API_TOKEN"
BASE="http://qrsgen:3100"

# 1. Provisionar una instancia.
curl -sS -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -X POST "$BASE/api/instances" \
  -d '{"name":"whatsapp-main","events_webhook_url":"https://wf.example.com/qrsgen-events","inbox_id":90,"owner_tag":"tenant-acme"}'

# 2. Esperar a que aparezca el QR (long-poll).
curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/whatsapp-main/wait-ready?timeout=120"

# 3. Mientras el state == qr_pending, descargar el PNG y mostrarlo al usuario.
curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/whatsapp-main/qr" -o /tmp/qr.png

# 4. Cuando el usuario escanea, el state pasa a "ready" automáticamente.
#    Verificar:
curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/whatsapp-main" | jq '.state'

# 5. Enviar un mensaje (formato Channel::Api-compatible).
curl -sS -H 'Content-Type: application/json' \
  -X POST "$BASE/api/instances/whatsapp-main/webhook" \
  -d '{
    "event":"message_created","message_type":"outgoing","content":"Hola",
    "conversation":{"id":1,"meta":{"sender":{"identifier":"34600000000@s.whatsapp.net"}}},
    "id":42,"private":false
  }'
```

Si `whatsapp-main` está conectada → `200 {"status":"sent"}`.
Si está reconectando → `202 {"status":"queued","queue_id":N,"expires_at":"..."}` y
el outbox la entrega cuando vuelva.

## Próximos pasos

- Lee [Convenciones](conventions.md) para entender autenticación y HMAC.
- Mira [Mensajes](messages.md) para el detalle del `WebhookPayload`.
- Configura tu orquestador para escuchar [Lifecycle webhooks](lifecycle-webhooks.md).

## Glosario

**Provisionar**: crear una instancia nueva en qrsgen. Una instancia
representa un número de WhatsApp gestionado por el bridge.

**Instancia**: una sesión WhatsApp dentro del proceso qrsgen. Identificada
por un `name` único (e.g. `whatsapp-main`).

**Pairing**: proceso en el que el usuario escanea el QR con la app
oficial de WhatsApp y vincula su número a qrsgen. Solo hay que hacerlo
la primera vez (la sesión se persiste).

**Long-poll**: técnica donde una llamada HTTP mantiene la conexión
abierta hasta que ocurre un evento o expira un timeout. `wait-ready`
funciona así: bloquea hasta que la instancia llega a `ready`.

**State machine**: una instancia transita por estados bien definidos
(`qr_pending` → `paired` → `ready` → `disconnected`). El campo `state`
del endpoint informa en qué estado está ahora mismo.

**Channel::Api-compatible**: formato JSON estándar que muchos sistemas
de ticketing (Chatwoot, etc.) usan para mensajes. qrsgen entiende y
genera este formato por defecto.

**JID** (JabberID): identificador del destinatario en WhatsApp. Tiene
formato `<número>@s.whatsapp.net` o `<id>@lid` (cuentas anónimas
Multi-Device). qrsgen lo necesita en `conversation.meta.sender.identifier`.

**Outbox 202 queued**: cuando la instancia está reconectando, qrsgen
encola el mensaje y devuelve `202` en lugar de error. El mensaje se
entrega cuando la sesión vuelve (TTL 5 min).
