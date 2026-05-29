# Flujo INCOMING (cliente WhatsApp → tu sistema)

```
Cliente WhatsApp envía msg/typing/read al número conectado
        │
        ▼
Meta enruta al WebSocket activo del JID destino
        │
        ▼
WebSocket que qrsgen mantiene abierto desde bootstrap
        │ whatsmeow emite events.Message
        │                       events.ChatPresence ──► handleChatPresence
        │                       events.Receipt      ──► handleReceipt
        ▼
wameow.handle() dispatcher (case por tipo de evento)
        │
        ▼ events.Message
bridge/incoming.go:
        │ resuelve LID↔PN si aplica (Multi-Device)
        │ si msg.GetReactionMessage() != nil ──► handleReaction
        │                                          │
        │                                          ▼
        │                                  resuelve sender + conv
        │                                  aplica name resolver
        │                                    (IsContactSaved + LID/PN)
        │                                  body = "**~<name>** reaccionó con <emoji>"
        │                                          (o "_quitó su reacción_")
        │                                  POST incoming con
        │                                    source_id="WAID:reaction:<ID>"
        │                                          │
        │                                          ▼
        │                                        (fin)
        │ si fromMe=true: dedup.ShouldDrop() para evitar twin
        │ body = extractTextContent(msg)
        │        ├─ Conversation / ExtendedTextMessage ──► texto plano
        │        ├─ LocationMessage    ──► formatLocationContent (v0.36.0)
        │        │                        "📍 Ubicación compartida\n**<POI>**\n
        │        │                         <dir>\nhttps://maps.google.com/?q=<lat>,<lng>"
        │        ├─ PollCreationMessage(V3) ──► formatPollContent (v0.37.0)
        │        │                              "🗳️ **Encuesta:** <q>\n1. ...\n
        │        │                               _(elige N opción/es)_"
        │        └─ (nada) ──► ""  → mensaje descartado si tampoco hay media
        │ media = extractMedia(msg)
        │        ├─ AudioMessage + PTT ──► filename="voice-note.ogg" (v0.38.0)
        │        ├─ AudioMessage       ──► sanitizeMime + default audio/ogg
        │        └─ StickerMessage     ──► default image/webp si mime vacío
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

events.ChatPresence (composing/paused) — v0.34.0
        │
        ▼
manager.SetChatPresenceHandler → bridge.Incoming.HandleChatPresence
        │ FindContact + FindConversation (no crea)
        │ typingTracker.ShouldEmit(convID, state)
        │     ├─ cambio de estado ──► true (emite)
        │     ├─ mismo estado, < minInterval ──► false (throttle)
        │     └─ mismo estado, ≥ minInterval ──► true
        │ si true: Client.SetTypingStatus(convID, typing)
        │          POST /conversations/Y/toggle_typing_status
        ▼
        (fin)

events.Receipt (kind=read | read-self) — v0.34.1
        │
        ▼
manager.SetReceiptHandler → bridge.Incoming.HandleReceipt
        │ filtra Type in ("read","read-self"); resto: return
        │ FindContact + FindConversation (no crea)
        │ Client.UpdateContactLastSeen(convID, ts)
        │ POST /conversations/Y/update_last_seen
        │     body {agent_last_seen_at, contact_last_seen_at}=ts
        ▼
        (fin)
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

## Reacciones (handleReaction)

Desde v0.33.0, `bridge.Incoming.Handle` chequea si el payload entrante
es una reacción **antes** del path normal de texto/media:

```go
if msg.Message.GetReactionMessage() != nil {
    return i.handleReaction(ctx, instance, msg)
}
```

Antes de v0.33.0 estos eventos caían en el path "sin texto ni media" y
se descartaban. `handleReaction`:

- Resuelve sender + conversación por el mismo camino que un mensaje
  normal (LID↔PN, contact lookup en downstream).
- Aplica el name resolver de `applyGroupSenderPrefix` incluyendo
  `IsContactSaved` (v0.32.0) para decidir si incluye teléfono.
- Construye el body con uno de tres formatos:
  - `**~<name>** reaccionó con <emoji>` (saved)
  - `` **~<name>** `+<E164>` reaccionó con <emoji> `` (no saved en grupo)
  - `**~<name>** _quitó su reacción_` (text="" → retracted)
- POSTea con `message_type: "incoming"` y
  `source_id: "WAID:reaction:<msg.Info.ID>"` — namespace separado del
  mensaje target para evitar colisión en el dedup del downstream.

Casos de descarte: `IsFromMe=true` (reacción propia desde otro
device), contacto no existe en downstream (no se crea por una
reacción suelta), `QRSGEN_REACTIONS_SYNC=false`. Detalles en
[Sincronización de reacciones](../integrations/reactions-sync.md).

## Content extraction (location, polls, media polish)

Desde v0.36.0 / v0.37.0 / v0.38.0, `extractTextContent` y
`extractMedia` en `internal/bridge/incoming.go` cubren tipos de payload
que antes caían en el path "sin contenido" y se descartaban
silenciosamente:

- **`LocationMessage`** (v0.36.0): `formatLocationContent(loc)` produce
  un body multilínea con header `📍 Ubicación compartida` (o
  `Ubicación en vivo` si `IsLive=true`), nombre del POI en bold,
  dirección, link `https://maps.google.com/?q=<lat>,<lng>` con `%.6f`
  de precisión, y comentario del sender en italic. Coords `0,0`
  descartan (devuelve `""`). Live locations: cada snapshot llega como
  mensaje independiente; no hay agregación.
- **`PollCreationMessage` / `PollCreationMessageV3`** (v0.37.0):
  `formatPollContent(poll)` produce `🗳️ **Encuesta:** <pregunta>`,
  opciones numeradas 1-based, y hint `_(elige 1 opción)_` /
  `_(elige hasta N opciones)_` según `SelectableOptionsCount` (sin hint
  para `max=0`). `PollUpdateMessage` (votos) **no** se procesa —
  Chatwoot no tiene UI de polls que pueda reflejarlos.
- **Media polish** (v0.38.0): voice notes (PTT=true) usan filename
  `voice-note.ogg` en lugar de `audio.opus`; `sanitizeMime` quita el
  parámetro de codec del `Content-Type` (`audio/ogg; codecs=opus` →
  `audio/ogg`); stickers con mime vacío reciben default `image/webp`.
  **Sin transcodificación** — los bytes son los mismos que llegan por
  WA. Solo cambia el `Content-Type` y filename anunciados al
  downstream para maximizar compatibilidad con reproductores HTML5.

Las tres versiones no introducen env vars nuevas — aplican siempre que
el payload llegue por el WebSocket. Detalles en
[Soporte de contenido de mensajes](../integrations/message-content.md).

## Typing (handleChatPresence)

Desde v0.34.0, `wameow.Conn` expone `SetChatPresenceHandler` y el
dispatcher tiene un case nuevo para `*events.ChatPresence`. El handler
se propaga vía `manager.SetChatPresenceHandler` a todas las `Conn`
(actuales + futuras), mismo patrón que `SetPictureHandler` del avatar
sync.

`bridge.Incoming.HandleChatPresence`:

- Resuelve contacto + conversación con `FindContact`/`FindConversation`.
  **No crea** — si no existen, descarta el evento (el primer mensaje
  "real" abrirá la ruta).
- Consulta `typingTracker.ShouldEmit(convID, state)` para decidir si
  POSTea. La política: cambios de estado siempre emiten; mismo estado
  dentro de `minInterval` (default 4s) se throttlea.
- Si debe emitir, llama `Client.SetTypingStatus(convID, typing bool)`
  que hace `POST /api/v1/accounts/X/conversations/Y/toggle_typing_status`
  con body `{"typing_status":"on"}` o `{"typing_status":"off"}`.

Master switch `QRSGEN_TYPING_SYNC` (default `true`). El tracker es
in-memory; restart pierde el throttle state y en el worst case se hace
una llamada HTTP extra por conversación activa. Detalles en
[Typing indicators y read receipts](../integrations/presence-and-receipts.md).

## Read receipts (handleReceipt)

Desde v0.34.1, `wameow.Conn` expone `SetReceiptHandler` y el dispatcher
tiene un case nuevo para `*events.Receipt`. Propagación idéntica al
patrón de typing.

`bridge.Incoming.HandleReceipt`:

- Filtra por `Type`: solo `read` y `read-self` continúan; resto
  (`delivered`, `played`, `sender`) se ignoran porque son menos
  accionables para el agente.
- Resuelve contacto + conversación. Si no existen, descarta.
- Llama `Client.UpdateContactLastSeen(convID, ts)` que hace
  `POST /api/v1/accounts/X/conversations/Y/update_last_seen` con body
  `{"agent_last_seen_at": <ts>, "contact_last_seen_at": <ts>}`. Ambos
  campos llevan el mismo valor: el `receipt.Timestamp` (Unix epoch
  segundos) del momento en que WhatsApp registró el receipt — no
  cuando qrsgen lo recibió.

El resultado en el downstream: la conversación se marca como vista por
el contacto en `ts`, y los mensajes outgoing previos del agente se
renderizan como leídos (equivalente al doble check azul en Chatwoot).

Master switch `QRSGEN_READ_RECEIPTS_SYNC` (default `true`). Sin retry:
el siguiente `read` corregirá el `last_seen_at` si el POST falla.
Detalles en
[Typing indicators y read receipts](../integrations/presence-and-receipts.md).

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

**`handleReaction`**: handler en `bridge.Incoming` (desde v0.33.0) que
intercepta `ReactionMessage` antes del path normal de texto/media y
los propaga al downstream como mensaje incoming con
`source_id: "WAID:reaction:<ID>"`.

**`WAID:reaction:<ID>` namespace**: prefijo para el `source_id` de
reacciones. Garantiza unicidad respecto al mensaje target (que usa
`WAID:<ID>`) y evita que el dedup del downstream las confunda como
duplicados.

**`handleChatPresence`**: handler en `bridge.Incoming` (desde v0.34.0)
que procesa `*events.ChatPresence` (`composing`/`paused`) y los propaga
al downstream vía `Client.SetTypingStatus`. Throttled por el
`typingTracker`.

**`handleReceipt`**: handler en `bridge.Incoming` (desde v0.34.1) que
procesa `*events.Receipt` filtrando por `Type in ("read","read-self")`
y los propaga al downstream vía `Client.UpdateContactLastSeen`.

**`typingTracker`**: estructura in-memory per-conversación que decide
si un evento `ChatPresence` debe emitir o silenciarse. Cambios de
estado siempre emiten; mismo estado dentro de `minInterval` (default
4s) NO emite. Reset on restart.

**`contact_last_seen_at`**: campo de la conversación en el modelo de
Chatwoot que registra cuándo el contacto vio la conversación por
última vez. qrsgen lo actualiza con el `receipt.Timestamp` de los
read receipts entrantes.
