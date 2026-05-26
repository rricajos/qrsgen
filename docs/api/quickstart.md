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
