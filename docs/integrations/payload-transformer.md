# Payload transformer (per-tenant Go templates)

Desde **v0.65.0**, qrsgen permite reescribir el body JSON del POST a
`/messages` del downstream usando un **Go `text/template`** configurado
por tenant. Habilita downstreams cuyo payload no es Chatwoot-shape (n8n
webhook directo, Zendesk, Freshdesk, SaaS propietario, etc.) sin
escribir un adapter Go completo.

## Cuándo usarlo

- El downstream NO es Chatwoot pero acepta un POST con campos JSON
  distintos a los de qrsgen.
- Necesitas inyectar campos extras al payload (tags, metadata).
- Quieres traducir el `message_type` ("incoming"/"outgoing"/"activity")
  a la nomenclatura del downstream.

Si tu downstream encaja en el shape Chatwoot, **no lo uses** — el
default es bueno.

## Cómo configurarlo

El template vive en la columna `payload_template` de la tabla
`bridge_tenant`. Se setea vía la API:

```bash
# Crear o actualizar tenant con un template:
curl -X POST $QRSGEN/api/tenants \
  -H "Authorization: Bearer $QRSGEN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "owner_tag": "client-x",
    "downstream_base_url": "https://hooks.client-x.com",
    "downstream_api_token": "TOKEN",
    "downstream_account_id": 1,
    "downstream_inbox_id": 5,
    "payload_template": "{\"text\":{{printf \"%q\" .Content}},\"channel\":\"whatsapp\",\"direction\":{{printf \"%q\" .MessageType}}}"
  }'

# Actualizar solo el template (PATCH):
curl -X PATCH $QRSGEN/api/tenants/client-x \
  -H "Authorization: Bearer $QRSGEN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"payload_template": "..."}'
```

Para limpiar (volver al shape Chatwoot default), set `payload_template`
a string vacío.

## Variables disponibles

El template recibe un struct con estos campos:

| Variable | Tipo | Descripción |
|---|---|---|
| `{{.Content}}` | `string` | Contenido del mensaje (después de aplicar `QRSGEN_GROUP_PREFIX_SENDER`, mention substitution, etc.) |
| `{{.MessageType}}` | `string` | `"incoming"`, `"outgoing"`, `"activity"` |
| `{{.SourceID}}` | `string` | Idempotency key (WAID o derivado) |
| `{{.ConversationID}}` | `int` | ID de la conversación en el downstream |
| `{{.CreatedAtUnix}}` | `int64` | Timestamp Unix (0 si no es backdated history import) |
| `{{.InReplyTo}}` | `int` | ID del msg al que responde (0 si no aplica) |

## Cómo escribir el template

El output debe ser **JSON sintácticamente válido**. qrsgen lo verifica
antes de POSTear; si parse falla, hace fallback al payload Chatwoot
default + warning log (no rompe el msg).

**Para strings**: usar `{{printf "%q" .Variable}}` — `%q` produce un
string JSON-safe (con quotes + escapes).

**Para ints**: usar `{{.Variable}}` directamente.

## Ejemplos

### n8n webhook directo (shape genérico)

```json
{
  "text": {{printf "%q" .Content}},
  "channel": "whatsapp",
  "direction": {{printf "%q" .MessageType}},
  "thread_id": {{.ConversationID}},
  "external_id": {{printf "%q" .SourceID}}
}
```

### Zendesk ticket comment

```json
{
  "comment": {
    "html_body": {{printf "%q" .Content}},
    "public": true
  },
  "author_id": {{.ConversationID}}
}
```

### Slack incoming-webhook

```json
{
  "text": {{printf "%q" .Content}},
  "username": "qrsgen-bridge",
  "icon_emoji": ":whatsapp:"
}
```

### Custom SaaS con tags

```json
{
  "msg": {{printf "%q" .Content}},
  "tags": ["whatsapp", {{printf "%q" .MessageType}}],
  "meta": {
    "qrsgen_source_id": {{printf "%q" .SourceID}},
    "qrsgen_ts": {{.CreatedAtUnix}}
  }
}
```

## Limitaciones (v0.65.0)

- Solo afecta a `PostMessage` (text messages). Los **attachments**
  (`PostMessageWithAttachment`, multipart/form-data) NO se templatean
  — usan siempre el shape Chatwoot-compatible. Si tu downstream necesita
  un shape distinto para media, abre issue.
- El template se ejecuta **siempre** (no se puede filtrar por tipo de
  mensaje). Si necesitas lógica conditional, úsala dentro del template
  con `{{if eq .MessageType "outgoing"}}...{{end}}`.
- Los demás endpoints (`CreateContact`, `CreateConversation`, etc.)
  siguen usando el shape Chatwoot — para downstreams realmente
  divergentes hay que escribir un adapter completo (interfaz
  `DownstreamAPI` introducida en v0.65.0 lo permite, pero qrsgen
  hoy solo trae el `chatwoot` adapter).

## Fallback graceful

Si el template:

1. **No parsea** al construir el Client → warning log, el Client opera
   en modo default (sin template). El operador ve el warning en logs
   tras el siguiente boot.
2. **Falla execute** runtime (ej. variable inexistente) → warning log
   por cada msg, fallback al shape Chatwoot para ese msg.
3. **Produce JSON inválido** → warning log por cada msg, fallback.

En los 3 casos, el msg llega al downstream — solo en el shape
"equivocado" si el operador no se entera del warning. Vigilar logs
tras configurar template nuevo.
