# Groups API

Endpoints REST para gestionar grupos WhatsApp. Subset operativo
introducido en **v0.48.0**.

Todos los endpoints están bajo `/api/instances/:name/groups/...` y
protegidos por `QRSGEN_API_TOKEN` (header `Authorization: Bearer ...`).

Para uso en producción y recetas más completas ver
[`docs/integrations/group-admin.md`](../integrations/group-admin.md).

## `GET /api/instances/:name/groups/:jid`

Devuelve la info actual del grupo. Round-trip a los servers WA.

**Path params**:
- `name` (string) — instance name
- `jid` (string) — group JID (formato `<id>@g.us`)

**Response 200**:
```json
{
  "jid": "120363111222333444@g.us",
  "subject": "IA LA CASA",
  "topic": "Coordinación del proyecto",
  "is_locked": false,
  "is_announce": false,
  "is_ephemeral": false,
  "participants": [
    {
      "jid": "34604021705@s.whatsapp.net",
      "phone_number": "+34604021705",
      "is_admin": true,
      "is_super_admin": true
    }
  ]
}
```

**Errors**:
| Code | Body | Caso |
|---|---|---|
| 400 | `{"error":"invalid jid: ..."}` | JID no parseable |
| 400 | `{"error":"jid must be a group (@g.us)"}` | JID válido pero no de grupo |
| 404 | `{"error":"instance not found"}` | instance no existe |
| 500 | `{"error":"get group info: ..."}` | WA error (grupo no existe / bot sin permisos) |

## `POST /api/instances/:name/groups/:jid/name`

Cambia el subject (nombre visible) del grupo. Requiere bot admin.

**Body** (JSON):
```json
{"name": "Nuevo Nombre"}
```

**Response 200**:
```json
{"jid": "120363111222333444@g.us", "name": "Nuevo Nombre"}
```

**Errors**:
| Code | Body | Caso |
|---|---|---|
| 400 | `{"error":"invalid group jid"}` | JID no parseable o no `@g.us` |
| 400 | `{"error":"name required"}` | body sin `name` o `name=""` |
| 404 | `{"error":"instance not found"}` | |
| 500 | `{"error":"set group name: 403 forbidden"}` | bot no es admin |

**Side effect**: con `QRSGEN_GROUP_EVENTS_ENABLED=true`, qrsgen
postea un activity msg en la conv del grupo:
```
📝 **Bot Name** cambió el nombre del grupo a _Nuevo Nombre_
```

## `POST /api/instances/:name/groups/:jid/participants`

Añadir, expulsar, promover o degradar miembros. Requiere bot admin.

**Body** (JSON):
```json
{
  "action": "add",
  "jids": ["34611111111@s.whatsapp.net", "34622222222@s.whatsapp.net"]
}
```

**`action`** ∈ `{add, remove, promote, demote}`.

**Response 200**:
```json
{
  "jid": "120363111222333444@g.us",
  "action": "add",
  "count": 2
}
```

**Errors**:
| Code | Body | Caso |
|---|---|---|
| 400 | `{"error":"invalid group jid"}` | |
| 400 | `{"error":"invalid body"}` | JSON malformado |
| 400 | `{"error":"jids required"}` | array vacío |
| 400 | `{"error":"invalid participant jid: ..."}` | algún JID no parseable |
| 400 | `{"error":"invalid action ..."}` | action no soportado |
| 404 | `{"error":"instance not found"}` | |
| 500 | `{"error":"update participants: ..."}` | bot no admin / WA error |

**Side effects** (con events enabled):

| action | activity msg |
|---|---|
| `add` | `➕ **Bot** añadió a NombreDelAñadido` |
| `remove` | `➖ NombreDelExpulsado salió/fueron expulsados del grupo` |
| `promote` | `⭐ **Bot** promovió a admin: NombrePromovido` |
| `demote` | `⭐ **Bot** quitó admin a: NombreDegradado` |

## Requisitos generales

### Bot debe ser admin

Para cualquier operación de escritura el bot DEBE ser admin del
grupo. WhatsApp rechaza con 403 si no — qrsgen devuelve 500 con el
mensaje del peer.

Workaround: el owner del grupo añade el número del bot como admin
desde su phone antes de empezar a gestionar vía API.

### Bot debe estar conectado

Si la instancia está `disconnected` el endpoint devuelve 404 o 500
según el caso. Verificar con `GET /api/instances` antes de operar.

## Endpoints planeados (post v0.48.0)

- `POST /groups/:jid/topic` — cambiar descripción
- `POST /groups/:jid/locked` — restringir edición a admins
- `POST /groups/:jid/announce` — restringir envío de msgs a admins
- `POST /groups/:jid/ephemeral` — mensajes temporales
- `POST /groups` — crear grupo nuevo
- `DELETE /groups/:jid` — bot abandona el grupo

Estos vendrán en una minor posterior con scope completo.
