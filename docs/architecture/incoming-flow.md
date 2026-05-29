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
        │                                  aplica name resolver (LID/PN)
        │                                  body = "**<tilde><name>** reaccionó con <emoji>"
        │                                          (tilde = "~" si !IsContactSaved, "" si saved — v0.39.5)
        │                                          (en grupo: header en code block,
        │                                           "`+<E164> · <tilde><name>` reaccionó con <emoji>" — v0.39.6;
        │                                           o "_quitó su reacción_" si text=="")
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
        │           saved = resolver.IsContactSaved(jid)   (v0.39.5)
        │             ├─ si LID y !saved: PNForLID → re-check IsContactSaved con PN
        │           tilde = "" si saved, "~" si !saved      (v0.39.5)
        │           prefix = "`+<E164> · <tilde><name>`"   (v0.39.6)
        │           (v0.39.4: header completo envuelto en inline code block
        │            con backticks; teléfono SIEMPRE presente.
        │            v0.39.5: el tilde se prepende SOLO si el contacto no está
        │            guardado — replica la convención de la UI de WhatsApp.
        │            v0.39.6: el orden pasa a phone-first, separador middle
        │            dot " · " (U+00B7), nombre al final. Se eliminan los **
        │            de bold y los tabs porque Chatwoot no procesa bold dentro
        │            de inline code y colapsa los tabs a un único espacio —
        │            la heurística runes/tabs de v0.39.3 deja de aplicar)
        │ construye payload {content, attachments, source_id: WAID:..., ...}
        │ POST al endpoint downstream
        ▼
Tu sistema recibe el POST y procesa
        │
        └─ usage.IncIn(instance)
        └─ metric qrsgen_messages_total{direction="in"}++
        └─ waidTracker.RecordIncoming(convID, WAID, ts)  (v0.39.0)
              ──► luego drenado por conversation_updated
                  para disparar MarkRead → WA (doble check azul)
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

## Prefijo de grupo (v0.39.6: code block + phone-first + middle dot + tilde solo si no saved)

Para mensajes cuyo `chat.Server == g.us` (grupos), antes del POST al
downstream qrsgen llama `applyGroupSenderPrefix(body, msg, resolver)`.
Desde **v0.39.6** la función construye el header como
`` `+<E164> · <tilde><name>` ``: teléfono primero, middle dot `·`
(U+00B7) con espacios a ambos lados como separador, y nombre al final.
Mantiene la lógica de `IsContactSaved` para el tilde introducida en
v0.39.5 — solo cambia el ensamblado del string.

```
saved = resolver.IsContactSaved(jid)
if jid es LID y !saved:
    pn = resolver.PNForLID(jid)
    if pn != "": saved = resolver.IsContactSaved(pn)  ← LID/PN fallback
tilde = "~" if !saved else ""
prefix = "`" + "+" + e164 + " · " + tilde + name + "`"
```

- **Name + phone, saved** (sin tilde):
  `` `+<E164> · <name>`\n<body> ``
- **Name + phone, no saved** (con tilde):
  `` `+<E164> · ~<name>`\n<body> ``

  Toda la línea de header va dentro de un par de backticks (inline
  code block, v0.39.4). El separador es **middle dot `·` (U+00B7)**
  con un espacio a cada lado. Chatwoot lo preserva literal dentro del
  code block, a diferencia de los tabs `\t` (que colapsa a un único
  espacio) y de los `**bold**` (que no procesa dentro de inline code y
  quedan como asteriscos literales). El orden phone-first aprovecha
  la longitud predecible del E.164 para dar columna estable; el
  nombre, de largo variable, ocupa la cola y no descuadra nada.

- **Solo name** (sin phone): `**<tilde><name>**:\n<body>` —
  degenerado, sin code block.
- **Solo phone** (sin name): `+<E164>:\n<body>` — degenerado, sin
  backticks (desde v0.39.2).

Si el sender llega como LID, qrsgen sigue resolviendo a PN vía
`PNForLID` para obtener `ContactName` y phone presentables, y desde
v0.39.5 además re-chequea `IsContactSaved` con el PN resuelto para
acertar el bit del tilde para contactos guardados que mandaron por su
LID anonimizado.

**Evolución del check `IsContactSaved` en este path**:

- v0.32.0–v0.39.3: condicionaba si **se omitía el teléfono** (saved →
  sin phone).
- v0.39.4: no se consultaba; el prefijo era idéntico para saved y
  unsaved, siempre con `~` y siempre con phone.
- v0.39.5: vuelve a consultarse, pero condiciona solo el **tilde
  `~`** del nombre. El teléfono se sigue incluyendo siempre.
- **v0.39.6**: misma semántica que v0.39.5; solo cambia el orden y
  los separadores del header (`+phone · <~?>name`) y desaparecen los
  `**` y los tabs.

Detalles e histórico en
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
- Aplica el name resolver compartido con `applyGroupSenderPrefix`.
  Desde v0.39.4 el teléfono se incluye siempre en grupos; desde v0.39.5
  el tilde `~` se prepende al nombre solo si el contacto no está
  guardado (vía `IsContactSaved`).
- Construye el body con uno de los formatos (siendo `<tilde>` = `"~"`
  si !saved, `""` si saved — v0.39.5):
  - 1:1 (no grupo): `**<tilde><name>** reaccionó con <emoji>`
  - Grupo (v0.39.6): `` `+<E164> · <tilde><name>` reaccionó con <emoji> ``
    (header completo en code block; phone-first, separador middle dot
    `·` (U+00B7) con espacios; sin `**bold**` ni tabs)
  - Retracted (text=""): `**<tilde><name>** _quitó su reacción_`
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

## Mark-as-read outgoing (v0.39.0) — cierra el ciclo bidireccional

Desde v0.39.0 el flujo incoming registra cada mensaje en un tracker
in-memory tras `PostMessage` exitoso para permitir que el path
**outgoing** (webhook `conversation_updated` desde Chatwoot) propague
`MarkRead` a WhatsApp cuando el agente lee. Es el inverso del
v0.34.1 (read receipts incoming).

```
sync() incoming, tras PostMessage exitoso:
        │
        └─ waidTracker.RecordIncoming(convID, msg.Info.ID, msg.Info.Timestamp)
                cap 50 entradas/conv (FIFO al desbordar)

────────────────────────────────────────────────────────────────────

POST /api/instances/{name}/webhook (Chatwoot envía conversation_updated)
        │
        ▼
bridge.Outgoing.HandleWebhook:
        │ event == "conversation_updated"?
        │ payload.Conversation.AgentLastSeenAt > 0?
        │     (si no, ignorar — Chatwoot emite el evento por muchos
        │      motivos: cambio de etiqueta, asignación, status...)
        ▼
waids := waidTracker.DrainBefore(convID, agent_last_seen_at)
        │ devuelve los WAIDs con ts ≤ cutoff (idempotente: la siguiente
        │ llamada con mismo cutoff devuelve [])
        ▼
ReadMarker.MarkRead(ctx, chat, sender, waids, agent_last_seen_at)
        │ wameow.Conn.MarkRead → client.MarkRead() de whatsmeow
        ▼
Cliente WA ve doble check azul sobre los mensajes incluidos
```

**Componentes nuevos** (v0.39.0):

- `wameow.Conn.MarkRead(ctx, chat, sender, messageIDs, ts)` — wrapper
  fino sobre `client.MarkRead()` de whatsmeow. Idempotente.
- `bridge.waidTracker` — tracker in-memory per-conv. Métodos
  `RecordIncoming` (llamado por incoming `sync()`) y `DrainBefore`
  (llamado por outgoing `HandleWebhook`).
- `bridge.ReadMarker` — interfaz pequeña con un solo método; desacopla
  `Outgoing` del cliente WA real.
- `bridge.Outgoing.EnableMarkAsRead(waids, marker)` — wire en
  `main.go`.
- `bridge.Incoming.EnableMarkAsRead()` devuelve `*waidTracker`; el
  mismo tracker se inyecta en `Outgoing.EnableMarkAsRead` para que
  ambas direcciones compartan estado.
- `WebhookPayload.Conversation.AgentLastSeenAt` /
  `ContactLastSeenAt` — campos nuevos parseados del webhook.
- `senderAdapter.MarkRead` en `main.go` puentea hacia `wameow.Conn`.

**Configuración REQUERIDA en Chatwoot**: el webhook (mismo endpoint
que ya se usa para `message_created`) debe suscribir adicionalmente
`conversation_updated`. Sin esa config el feature no rompe, solo no
opera (qrsgen nunca recibe la señal de cuándo el agente leyó).

Master switch `QRSGEN_MARK_AS_READ_OUTGOING` (default `true`). Sin
retry: tracker in-memory + idempotencia natural cubren el caso
fallido. Restart pierde el historial — mensajes leídos durante
downtime no se marcan retroactivamente en WA (cosmético). Detalles en
[Mark-as-read outgoing (v0.39.0)](../integrations/presence-and-receipts.md#mark-as-read-outgoing-v0390).

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

**Prefijo de grupo**: header que `applyGroupSenderPrefix` antepone
al body para mensajes de grupo. Desde v0.32.0 hasta v0.39.3 fue
adaptativo (omitía el teléfono para senders guardados, vía
`IsContactSaved`). En **v0.39.4** se unificó: header en inline code
block con backticks y teléfono siempre presente, sin consultar
`IsContactSaved`. Desde **v0.39.5** `applyGroupSenderPrefix` vuelve a
consultar `IsContactSaved` pero **solo para decidir si prepende el
tilde `~`** al nombre: contactos saved (FullName/FirstName) van sin
tilde, contactos solo PushName van con `~`. El teléfono sigue
incluyéndose siempre (no se revierte el cambio de v0.39.4). Desde
**v0.39.6** el header se reordena a `` `+<E164> · <~?>name` ``
(phone-first, middle dot `·` (U+00B7) como separador) y se eliminan
los marcadores `**bold**` y los tabs `\t` porque Chatwoot no procesa
bold dentro de inline code y colapsa los tabs a un único espacio
dentro del code block — la heurística de tab count variable de
v0.39.3 deja de tener efecto y se retira del path.

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

**`agent_last_seen_at`**: campo de la conversación en el modelo de
Chatwoot que registra cuándo el agente vio la conversación por última
vez. Llega en el webhook `conversation_updated` y desde v0.39.0 qrsgen
lo usa como cutoff para drenar el `waidTracker` y disparar `MarkRead`
hacia WhatsApp.

**`waidTracker`** (v0.39.0): tracker in-memory per-conversación que
guarda los WAIDs de mensajes incoming junto con su timestamp para
permitir `MarkRead` outgoing cuando el agente lee. Métodos
`RecordIncoming(convID, waid, ts)` (llamado en `sync()` incoming tras
`PostMessage`) y `DrainBefore(convID, cutoffTS)` (llamado en
`HandleWebhook` outgoing al recibir `conversation_updated`). Cap por
defecto de 50 entradas/conv (FIFO). Reset on restart.

**`ReadMarker`** (v0.39.0): interfaz minimal (un solo método
`MarkRead(ctx, chat, sender, messageIDs, ts)`) que desacopla
`bridge.Outgoing` del cliente WA concreto. Implementada por
`wameow.Conn.MarkRead`.

**`conversation_updated`**: webhook emitido por Chatwoot cuando algo
cambia en una conversación. Desde v0.39.0 qrsgen lo procesa para
detectar `agent_last_seen_at` y disparar `MarkRead` outgoing. Requiere
suscripción explícita en la configuración del webhook (no viene por
defecto).
