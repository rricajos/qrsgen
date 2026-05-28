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
        │ si grupo: applyGroupSenderPrefix(body, msg, resolver)
        │           ├─ IsContactSaved(sender)? ──► sí: prefix = "**~<name>**"
        │           └─                             no: prefix = "**~<name>** `+<E164>`"
        │ construye payload {content, attachments, source_id: WAID:..., ...}
        │ POST al endpoint downstream
        ▼
Tu sistema recibe el POST y procesa
        │
        └─ usage.IncIn(instance)
        └─ metric qrsgen_messages_total{direction="in"}++
        └─ maybeAvatarSync(jid)  ──► goroutine (fire-and-forget)
                                       │
                                       ▼
                              GetProfilePictureID (cheap)
                              ¿LastID != currentID?  ──► no: skip
                                       │ sí
                                       ▼
                              GetProfilePicture (descarga)
                              UploadContactAvatar(downstream)
                              tracker.UpdateID
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

## Prefijo de grupo (saved/unsaved branching)

Para mensajes cuyo `chat.Server == g.us` (grupos), antes del POST al
downstream qrsgen llama `applyGroupSenderPrefix(body, msg, resolver)`.
Esta función decide el formato del prefix según si el sender está
guardado en la libreta del bot owner (vía `WAResolver.IsContactSaved`,
que consulta `client.Store.Contacts.GetContact` de whatsmeow):

- **Saved + name disponible**: `**~<name>**\n<body>` — el agente ya
  sabe quién es; omitimos el bloque de teléfono.
- **No saved + name + phone**: `` **~<name>** `+<E164>`\n<body> `` —
  push name + teléfono code block para identificar al desconocido.
- **Solo name** (sin phone): `**~<name>**:\n<body>`.
- **Solo phone** (sin name): `` `+<E164>`:\n<body> ``.

Si el sender llega como LID y la primera consulta no es saved o no
tiene nombre, qrsgen intenta resolver el LID a PN vía `PNForLID` y
re-pregunta `IsContactSaved`/`ContactName` sobre el PN. Cubre
contactos guardados por número que llegan anonimizados en grupos.
Detalles en
[Formato del prefijo de grupo](../integrations/group-sender-format.md).

## Side effect: avatar sync

Tras el POST al downstream, `sync()` llama también a `maybeAvatarSync`
(desde v0.31.1). Esta función consulta el tracker in-memory
(`avatar_tracker.go`); si el TTL ha vencido para `(instance, jid)`,
spawnea una goroutine que sincroniza el avatar de WhatsApp al
downstream. Es fire-and-forget — errores se loguean como `warn` pero
nunca bloquean el flujo del mensaje. Detalle completo en
[Avatar sync](../integrations/avatar-sync.md).

Adicionalmente, qrsgen subscribe `events.Picture` de whatsmeow (desde
v0.31.2). Cuando el usuario cambia su foto, dispara
`HandlePictureChange` que resetea el tracker y fuerza un sync
inmediato sin esperar al siguiente mensaje.

## Glosario

**Incoming**: mensaje que viene del cliente WhatsApp hacia tu sistema
(opuesto a "outgoing", que va del sistema al cliente).

**events.Message**: evento emitido por la librería whatsmeow cuando
llega un mensaje por el WebSocket. Contiene el contenido, sender,
timestamp, etc.

**fromMe**: campo en `events.Message` que indica si el mensaje fue
enviado por el propio número conectado (típicamente desde otro device
Multi-Device del usuario). qrsgen lo detecta para evitar dobles
entregas.

**Idempotencia incoming**: técnica donde qrsgen detecta y descarta
mensajes duplicados via `bridge_dedup`. Clave compuesta por
`(instance, jid_user, content_hash)`.

**Channel::Api-compatible**: formato JSON estándar que muchos sistemas
de ticketing usan para mensajes (originalmente Chatwoot). qrsgen genera
este formato por defecto en sus POSTs al downstream.

**WAID** (WhatsApp ID): identificador único que WhatsApp asigna a cada
mensaje. qrsgen lo guarda en `source_id` del mensaje sincronizado al
downstream con prefijo `WAID:` para evitar reprocesarlo como outgoing.

**Inbox ID**: identificador numérico de "buzón" en el sistema
downstream. qrsgen lo propaga al POSTear incoming para que el downstream
sepa en qué conversación encolar el mensaje.

**Routing al downstream**: qrsgen no decide qué hacer con cada mensaje;
solo lo entrega al downstream que el integrador haya configurado
(Chatwoot, n8n proxy, app custom).

**Avatar sync (side-effect)**: spawn fire-and-forget que descarga la
foto de perfil del JID en WhatsApp y la sube al downstream como
avatar del contacto. Corre en paralelo al POST del mensaje. Gated
por un tracker in-memory que usa TTL + comparación de `info.ID` para
minimizar tráfico.

**Prefijo de grupo adaptativo**: rama de decisión en
`applyGroupSenderPrefix` (desde v0.32.0) que omite el bloque de
teléfono cuando el sender está guardado en la libreta del bot owner.
Detectado vía `IsContactSaved` contra el contact store de whatsmeow.
