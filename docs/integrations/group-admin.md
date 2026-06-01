# Group admin

Gestión de grupos WhatsApp desde la API HTTP — rename, miembros,
configuración. Disponible desde **v0.48.0**.

## Cuándo usarlo

- **Test E2E de v0.47.0**: dispara un cambio desde la API → llega
  `*events.GroupInfo` → qrsgen postea activity msg en la conv del
  grupo en Chatwoot. Loop verificable sin tocar el phone.
- **Producción**: un agente desde un panel custom (o n8n) gestiona
  grupos sin abrir WhatsApp. Útil para soporte que kickea miembros
  problemáticos o renombra grupos por sector.

## Endpoints v0.48.0 (mínimos)

Todos protegidos por `QRSGEN_API_TOKEN` (mismo middleware que el resto
de `/api/instances/*`).

### `GET /api/instances/:n/groups/:jid`

Info del grupo (subject, topic, settings, participantes con roles).
Round-trip al server WhatsApp.

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/instances/ATC/groups/120363111222333444@g.us"
```

Response 200:
```json
{
  "jid": "120363111222333444@g.us",
  "subject": "IA LA CASA",
  "topic": "Grupo de coordinación del proyecto",
  "is_locked": false,
  "is_announce": false,
  "is_ephemeral": false,
  "participants": [
    {
      "jid": "34604021705@s.whatsapp.net",
      "phone_number": "+34604021705",
      "is_admin": true,
      "is_super_admin": true
    },
    {
      "jid": "1234567890@lid",
      "phone_number": "+34611111111",
      "is_admin": false,
      "is_super_admin": false
    }
  ]
}
```

Si el participante es un LID, qrsgen resuelve `phone_number` cuando
puede (vía `PNForLID`). Si no hay resolución conocida, queda vacío.

### `POST /api/instances/:n/groups/:jid/name`

Renombra el grupo (cambia el subject visible). Requiere que el bot
sea admin del grupo.

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "IA LA CASA — Renovado"}' \
  "$BASE/api/instances/ATC/groups/120363111222333444@g.us/name"
```

Response 200:
```json
{"jid": "120363111222333444@g.us", "name": "IA LA CASA — Renovado"}
```

Errores:
- `400` body inválido o `name` vacío
- `500` WA rechaza (típicamente bot no es admin): error del peer en
  el campo `error`

Side effect (con `QRSGEN_GROUP_EVENTS_ENABLED=true`): qrsgen postea
un activity msg en la conv del grupo:
```
📝 **Bot Name** cambió el nombre del grupo a _IA LA CASA — Renovado_
```

### `POST /api/instances/:n/groups/:jid/participants`

Añadir, expulsar, promover o degradar miembros. Body:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"action": "add", "jids": ["34611111111@s.whatsapp.net"]}' \
  "$BASE/api/instances/ATC/groups/120363111222333444@g.us/participants"
```

Campos:
- `action` (requerido): `add` | `remove` | `promote` | `demote`
- `jids` (requerido): array de JIDs a procesar

Response 200:
```json
{
  "jid": "120363111222333444@g.us",
  "action": "add",
  "count": 1
}
```

Errores:
- `400` action no válido, jids vacío, jid malformado
- `500` WA rechaza (bot no es admin, número bloqueó al bot, etc.)

Side effects (con `GROUP_EVENTS_ENABLED=true`):
- `add` → `➕ **Bot** añadió a NombreDelAñadido`
- `remove` → `➖ NombreDelExpulsado salió/fueron expulsados del grupo`
- `promote` → `⭐ **Bot** promovió a admin: NombrePromovido`
- `demote` → `⭐ **Bot** quitó admin a: NombreDegradado`

## Requisitos

### Bot debe ser admin

Para cualquier operación de escritura (`name`, `participants`) el
bot debe ser admin del grupo. WhatsApp rechaza con `403 forbidden`
si no — qrsgen devuelve 500 con el mensaje del peer:

```json
{"error": "set group name: 403 forbidden"}
```

**Workaround**: añadir el número del bot como admin desde el phone
del owner del grupo antes de gestionar via API.

### `events.GroupInfo` activado para activity msgs

Sin `QRSGEN_GROUP_EVENTS_ENABLED=true`, las operaciones funcionan
pero no se reflejan como activity msgs en Chatwoot. El cambio sí
se aplica en WhatsApp, simplemente el agente no lo ve en la conv.

## Limitaciones v0.48.0 (planeadas para v0.50.0)

- **No expuesto**: `topic` (descripción), `locked`, `announce`,
  `ephemeral`, `create` (crear grupo nuevo), `leave` (bot abandona).

## Receta n8n

Workflow para promover automáticamente a un agente cuando llega un
mensaje de "/promoteme" en un grupo:

```
[Webhook qrsgen incoming]
   ↓ filter: content == "/promoteme"
[Function: extract sender JID + chat JID]
   ↓
[HTTP Request: POST /groups/{chat}/participants
   { "action": "promote", "jids": [{sender}] }]
   ↓
[HTTP Request: POST /chatwoot/messages
   { "content": "✅ Promovido a admin del grupo" }]
```

## Testing local

Sin un grupo real puedes verificar el plumbing:

```bash
# JID inválido → 400 (validación)
curl -H "Auth: Bearer $TOKEN" \
  "$BASE/api/instances/ATC/groups/noesunjid"
# → {"error":"invalid jid: ..."}

# JID válido pero no-grupo → 400
curl -H "Auth: Bearer $TOKEN" \
  "$BASE/api/instances/ATC/groups/34604021705@s.whatsapp.net"
# → {"error":"jid must be a group (@g.us)"}

# JID grupo no existente → 500 (peer WA rechaza)
curl -H "Auth: Bearer $TOKEN" \
  "$BASE/api/instances/ATC/groups/999999999999@g.us"
# → {"error":"get group info: info query returned status 400: bad-request"}
```

## Glosario

**Admin**: rol en un grupo WhatsApp con permisos de gestión (renombrar,
añadir/expulsar miembros, cambiar settings). Hay 2 niveles: `admin` y
`super_admin` (creador del grupo, no se le puede quitar admin).

**LID participants**: usuarios en el grupo que aparecen como
`<id>@lid` en lugar de `<phone>@s.whatsapp.net`. qrsgen resuelve a PN
vía whatsmeow's `PNForLID` cuando puede.

**ParticipantChange**: tipo de operación. WA usa el string raw
(`add` / `remove` / `promote` / `demote`); qrsgen valida y reenvía.
