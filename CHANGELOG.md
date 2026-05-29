# Changelog

Todos los cambios notables se documentan aquí. Sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) y [SemVer](https://semver.org/).

## [Unreleased]

## [0.37.0] - 2026-05-28

Soporte para encuestas (polls) de WhatsApp. Antes los polls se veían
como mensajes vacíos; ahora aparecen con la pregunta y opciones
numeradas para que el agente vea lo que el cliente preguntó.

### Added

- **`formatPollContent`** helper. Extrae el `Name` (pregunta),
  `Options[]` (cada una con `OptionName`) y `SelectableOptionsCount`
  del `PollCreationMessage` y genera body legible:

  ```
  🗳️ **Encuesta:** ¿Día para el meeting?
  1. Lunes
  2. Martes
  3. Miércoles
  _(elige 1 opción)_
  ```

  Multi-select: `_(elige hasta N opciones)_`. Unlimited (`max=0`)
  omite el hint.

- **Polls v1 + v3** soportados. `extractTextContent` chequea ambos
  `GetPollCreationMessage()` y `GetPollCreationMessageV3()`.

### Not propagated

- **`PollUpdateMessage`** (votos individuales) NO se procesa.
  Chatwoot no tiene widget de polls que pueda reflejar votos
  agregados, así que tirarlos al body como mensajes nuevos por
  cada voto sería ruido. v0.37.x podría añadir un modo opt-in si
  hace falta.

### Edge cases

- Poll sin pregunta (`Name=""`) → body vacío (no merece la pena).
- Poll sin options → body vacío.
- Comentario adjunto al poll (raro) no se incluye.

### Tests

- 5 sub-tests en `TestFormatPollContent`: single, multi, unlimited,
  sin nombre, sin opciones.

### Migration notes

- Sin breaking changes. Patrón canónico para detectar polls en
  parsers downstream: `^🗳️ \*\*Encuesta:\*\*`.

## [0.36.0] - 2026-05-28

Soporte para mensajes de ubicación de WhatsApp. Antes se veían vacíos
en el downstream; ahora aparecen con link a Google Maps + opcionalmente
nombre del lugar (POI), dirección y comment del cliente.

### Added

- **`formatLocationContent`** helper en `internal/bridge/incoming.go`.
  Extrae lat/lng del `LocationMessage` y genera un body legible con
  link directo a Google Maps. Incluye:
  - Header `📍 Ubicación compartida` (o `📍 Ubicación en vivo` si IsLive)
  - Nombre del POI en bold (si lo provee el cliente)
  - Dirección textual (si la provee)
  - Link `https://maps.google.com/?q=LAT,LNG` con precisión 6 decimales
  - Comment del cliente en italic (si lo añadió)
- **`extractTextContent` ahora delega en `formatLocationContent`** cuando
  el mensaje tiene `LocationMessage` no-nil — antes caía a "msg sin
  contenido" y se ignoraba.

### Behavior

- **Live locations** (cuando el cliente envía ubicación actualizable
  por tiempo limitado): cada update genera un mensaje nuevo con el
  header "en vivo". qrsgen no aggrega ni edita el msg anterior — la
  conv en Chatwoot recibe N snapshots, uno por update WA.
- **Coordenadas 0,0** se ignoran (location inválida o vacía).
- **Compatibilidad con Google Maps**: el link funciona en cualquier
  cliente (móvil, web, etc.) y ofrece direcciones automáticas.

### Tests

- 5 sub-tests en `TestFormatLocationContent` cubriendo: lat/lng pelado,
  con name+address, live, con comment, coordenadas inválidas (0,0).

### Migration notes

- Sin breaking changes. Mensajes de location que antes se descartaban
  ahora aparecen con contenido. Si tu integración n8n parseaba el
  body raw esperando mensajes vacíos para detectar location, ajusta
  el matching (el patrón `^📍 ` es el indicador canónico).

## [0.35.0] - 2026-05-28

Observability: contador Prometheus unificado para los eventos
real-time del bridge (avatar/reaction/typing/read_receipt). Permite
calcular tasas de éxito, detectar regresiones y alertar sobre fallos
en producción.

### Added

- **`qrsgen_realtime_events_total{feature, result, instance}`** —
  nuevo CounterVec en `internal/metrics/metrics.go`. Labels:
  - `feature`: "avatar" | "reaction" | "typing" | "read_receipt"
  - `result`: "ok" | "no_contact" | "no_conv" | "throttled" |
    "filtered" | "wa_miss" | "wa_error" | "ds_error"
  - `instance`: nombre de la instancia
- Cardinalidad: ~4 features × ~8 results × ~N instancias. Para una
  deployment típica (1-10 instancias) son 32-320 series.

### Wired

- `Incoming.syncAvatar`: incrementa con result=ok/wa_miss/wa_error/
  ds_error/throttled según el path tomado.
- `Incoming.handleReaction`: result=ok/no_contact/no_conv/ds_error.
- `Incoming.HandleChatPresence`: ok/no_contact/no_conv/throttled/ds_error.
- `Incoming.HandleReceipt`: ok/no_contact/no_conv/filtered/ds_error.

### PromQL examples

Tasa de errores de downstream por feature:
```promql
sum by (feature) (rate(qrsgen_realtime_events_total{result="ds_error"}[5m]))
```

% de avatares con foto vs sin foto (privacidad):
```promql
sum(rate(qrsgen_realtime_events_total{feature="avatar",result="ok"}[1h]))
/
sum(rate(qrsgen_realtime_events_total{feature="avatar",result=~"ok|wa_miss"}[1h]))
```

Typing events throttleados (debería ser >50% del total típicamente):
```promql
sum(rate(qrsgen_realtime_events_total{feature="typing",result="throttled"}[5m]))
/
sum(rate(qrsgen_realtime_events_total{feature="typing"}[5m]))
```

### Migration notes

- Sin breaking changes. Las métricas son aditivas; los exporters
  Prometheus existentes las recogen automáticamente.
- Dashboards Grafana nuevos: actualizar para incluir los paneles
  sobre la nueva métrica. Ejemplo en
  `examples/grafana-dashboard/qrsgen-realtime.json` (pendiente).

## [0.34.1] - 2026-05-28

Read receipts incoming: cuando el cliente WhatsApp abre el chat y ve
los mensajes que envió el agente, qrsgen actualiza el
`contact_last_seen_at` de la conv en el downstream. La UI marca los
mensajes con "leído" / doble check azul.

### Added

- **`wameow.ReceiptHandler`** — callback type para `*events.Receipt`.
- **`wameow.Conn.SetReceiptHandler`** + case nuevo para `*events.Receipt`
  en el dispatcher.
- **`manager.Manager.SetReceiptHandler`** — propagación a Conns.
- **`bridge.Incoming.HandleReceipt`** — filtra por kind in
  ("read", "read-self"), encuentra conv, llama
  `UpdateContactLastSeen`.
- **`downstream.Client.UpdateContactLastSeen(convID, ts)`** —
  `POST /api/v1/accounts/X/conversations/Y/update_last_seen` con
  `agent_last_seen_at` y `contact_last_seen_at` = ts del receipt.
- **`QRSGEN_READ_RECEIPTS_SYNC`** (default `true`).

### Behavior

- Solo procesamos `ReceiptTypeRead` y `ReceiptTypeReadSelf`. Los
  otros (`delivered`, `played`, `sender`) se ignoran — son menos
  accionables y aumentarían el ruido al downstream.
- Si el contacto no existe en el downstream o no hay conv abierta,
  el receipt se descarta silenciosamente.
- El timestamp del receipt se pasa al downstream tal cual — la UI
  muestra los msgs como leídos hasta ese momento.

### Migration notes

- Sin breaking changes. `QRSGEN_READ_RECEIPTS_SYNC=false` revierte
  al comportamiento de v0.34.0 (no propagación de receipts).

## [0.34.0] - 2026-05-28

Typing indicators (composing) de WhatsApp se propagan al downstream.
El agente ve "está escribiendo..." en la UI del downstream cuando el
cliente está tipeando del lado WhatsApp.

### Added

- **`wameow.ChatPresenceHandler`** — nuevo callback type. Se dispara
  con `*events.ChatPresence` (composing/paused, text/audio).
- **`wameow.Conn.SetChatPresenceHandler`** + nuevo case para
  `*events.ChatPresence` en el event dispatcher de Conn.
- **`manager.Manager.SetChatPresenceHandler`** — propaga el callback
  a Conns existentes y futuras.
- **`bridge.Incoming.HandleChatPresence`** — orquesta:
  - Find contact + conv (sin crear nada si no existen)
  - Throttle via typingTracker (anti-spam)
  - POST a `toggle_typing_status` del downstream
- **`downstream.Client.SetTypingStatus(convID, typing)`** — implementa
  `POST /api/v1/accounts/X/conversations/Y/toggle_typing_status` con
  body `{"typing_status":"on"|"off"}`.
- **`bridge.typingTracker`** — dedupea calls al downstream. Anti-spam
  con minInterval default 4s. Cambios de estado siempre emiten.
- **`QRSGEN_TYPING_SYNC`** (default `true`) — env var nueva.
- **5 tests** del typingTracker: primer emit, mismo state dentro
  intervalo, cambio de state, mismo state tras intervalo, aislamiento
  per-conv.

### Behavior

- **Throttle de 4s**: typing events de WhatsApp llegan varios por
  segundo durante una sesión de escritura. Solo propagamos al
  downstream cuando el estado cambia (composing↔paused) o si han
  pasado 4s desde la última propagación del mismo estado (refresh).
- **Si el contacto no existe en downstream**, evento descartado.
  No creamos contactos para typing sueltos.
- **Grupos**: cuando hay typing en un grupo, llega un evento por
  participante. El downstream solo soporta un indicator por conv,
  así que se ven todos como "alguien está escribiendo". El campo
  `sender` se loguea para debug pero no se incluye visualmente.

### Migration notes

- Sin breaking changes. Default ON aumenta la frecuencia de calls
  POST al downstream — si tienes rate-limits muy ajustados,
  considera el throttle de 4s y/o desactivar con
  `QRSGEN_TYPING_SYNC=false`.

## [0.33.0] - 2026-05-28

Propaga las reacciones (emojis) que los clientes WhatsApp añaden a
mensajes hacia el downstream como un mensaje incoming con formato
visible. El agente ve en la conv quién reaccionó y con qué emoji,
sin tener que adivinarlo.

### Added

- **Reactions read-side**: cuando un `*events.Message` llega con
  `ReactionMessage` no-nil, qrsgen ya no lo ignora. En su lugar,
  invoca `handleReaction` que:
  - Resuelve sender + conv via el mismo path que mensajes normales
  - Aplica el resolver de nombre (incluyendo `IsContactSaved` de
    v0.32.0): contactos en agenda se muestran sin teléfono,
    desconocidos en grupos se muestran con phone code block
  - Postea al downstream como `message_type: incoming` con
    `source_id: "WAID:reaction:<msg.Info.ID>"`
  - Si el emoji es vacío (reacción retirada), formato
    `"**~Name** _quitó su reacción_"`
- **`QRSGEN_REACTIONS_SYNC`** (default `true`) — env var nueva.
  Setear a `false` ignora todas las reacciones silenciosamente.

### Format examples

```
Contacto en agenda:        **~Jean Paul** reaccionó con 👍
Grupo + desconocido:       **~Richard** `+34604021705` reaccionó con ❤️
Reacción retirada:         **~Jean Paul** _quitó su reacción_
```

### Behavior

- **Reacciones del propio bot (`IsFromMe`) se ignoran**. El agente
  reaccionando desde el downstream no tiene flujo write-back hoy
  (sería v0.34.x outgoing reactions). Por ahora solo read-side.
- **Si el contacto no existe en downstream**, la reacción se
  descarta — no creamos contactos para reacciones sueltas.
  Esperamos al primer mensaje normal del JID para crearlo.
- **El target del mensaje reaccionado** se loguea (`target_msg_id`)
  pero no se incluye visualmente. Chatwoot no tiene UI nativa para
  asociar la reacción al mensaje original; la inferencia visual la
  hace el agente por proximidad en el timeline.

### Migration notes

- Sin breaking changes. Default ON aumenta el volumen de mensajes
  postados al downstream — si tu integración tiene rate-limits
  ajustados o métricas que cuentan mensajes (billing), considéralo.
- `QRSGEN_REACTIONS_SYNC=false` mantiene el comportamiento de
  v0.32.x (reacciones ignoradas).

## [0.32.1] - 2026-05-28

Bugfix del bulk avatar resync endpoint (v0.31.3): la ruta usada en
Chatwoot no existe y devolvía 404, haciendo que el endpoint fallara
con HTTP 500 inmediatamente.

### Fixed

- **`Client.ListContactsByInbox`** ahora usa el endpoint canónico de
  Chatwoot `GET /accounts/{a}/contacts?inbox_id={i}&page={n}` en
  lugar de `GET /accounts/{a}/inboxes/{i}/contacts?page={n}` que NO
  existe en la API. Con esto `POST /api/instances/:name/avatars/resync`
  funciona correctamente — itera todos los contactos del inbox y
  dispara el sync de avatar por cada uno.
- 2 tests nuevos en `internal/downstream/client_test.go`:
  - Verifica que el URL shape es el canónico (`/contacts?inbox_id=X&page=Y`)
  - Verifica que `hasMore=true` cuando la página tiene 15 contactos
    (page_size típico de Chatwoot)

### Impact

- El sync per-message (response a cada mensaje incoming) NO estaba
  afectado — funcionaba correctamente desde v0.31.0. Solo el bulk
  endpoint introducido en v0.31.3 estaba roto.
- Operadores que no usaron el bulk endpoint vieron sus avatares
  poblarse orgánicamente vía el path per-message (con o sin
  `QRSGEN_AVATAR_REFRESH_TTL=24h` activo).
- Tras este fix, el bulk endpoint puede usarse para backfillear
  contactos viejos sin esperar a que vuelvan a escribir.

### Migration notes

- Sin breaking changes. Es un bugfix puro. Upgradear desde v0.31.3
  → v0.32.1 lo arregla automáticamente.
- Operadores que no necesiten el bulk (porque ya están viviendo
  con el sync per-message) pueden seguir en v0.31.x sin afectación
  funcional.

## [0.32.0] - 2026-05-28

Distingue contactos guardados en la libreta del bot vs solo push name.
Los contactos en agenda se postean al downstream con solo el nombre
(el agente ya sabe quién es); los desconocidos siguen con el bloque
de teléfono code para identificarlos.

### Added

- **`WAResolver.IsContactSaved(jid) bool`** — nuevo método de la
  interfaz. Devuelve true si el JID tiene `FullName` o `FirstName`
  en el contact store de whatsmeow (lo que indica que está en la
  libreta del móvil del bot, originada típicamente desde Google
  Contacts → Android sync → WhatsApp app). PushName auto-asignado
  por el propio usuario NO cuenta como "saved".

### Changed

- **`applyGroupSenderPrefix` ahora omite el bloque de teléfono
  cuando `IsContactSaved` es true**. Formato resultante:

  ```
  Contacto en agenda:        **~Jean Paul**
                              <body>

  Solo push name (anónimo):  **~Richard** `+34604021705`
                              <body>
  ```

- Lookup LID → PN: si el sender llega como LID y no es saved/no
  tiene name, se intenta resolver al PN equivalente vía `PNForLID`
  y se re-chequea allí. Maneja correctamente el caso de senders
  hidden que sí están en la libreta del bot via su número.

### Behavior

- **Sin nuevas env vars**. La distinción es automática basada en
  el contact store de whatsmeow. Si tu móvil pareado no tiene
  Google Contacts sync activo, ningún contacto será "saved" y se
  comportará igual que v0.31.x (siempre muestra teléfono).
- **Grupos siempre van con teléfono** porque los grupos no se
  guardan en la libreta como contactos individuales (FullName/
  FirstName están vacíos para JIDs `@g.us`).
- **Push name no rompe nada**: si el contacto solo tiene PushName
  (no está en agenda), se sigue mostrando el formato anterior con
  bloque de teléfono.

### Migration notes

- Sin breaking changes. Es UX puro: el formato cambia solo para
  contactos que el bot tiene en su libreta. Parsers regex deben
  manejar ambos casos:
  - `\*\*~[^*]+\*\*` (saved, solo nombre)
  - `\*\*~[^*]+\*\* \`\+\d+\`` (unsaved, con teléfono)
- **`WAResolver` añade un método** (`IsContactSaved`). Mocks externos
  del interface necesitan implementarlo. Trivial: `return false`
  mantiene el comportamiento v0.31.x (siempre teléfono).

## [0.31.3] - 2026-05-28

Bulk re-sync endpoint para backfillear avatares de contactos viejos
(creados antes de v0.31.0 o que no han recibido mensajes recientes
que disparen el sync vía sync()).

### Added

- **`Client.ListContactsByInbox(ctx, inboxID, page)`** — endpoint
  helper para paginar contactos de un inbox via Chatwoot API
  (`GET /api/v1/accounts/X/inboxes/Y/contacts?page=Z`). Devuelve
  `(contacts, hasMore, error)`. Detecta paginación comparando con
  page_size default de Chatwoot (15).
- **`Incoming.ResyncInstanceAvatars(ctx, instance, r, inboxID)`** —
  itera todas las páginas de contactos del inbox, parsea identifier
  como JID, lanza `syncAvatar` por cada uno bypassando el tracker.
  Devuelve `ResyncResult` con scanned/skipped/queued/pages.
  Cap defensivo de 200 páginas (~3000 contactos).
- **`POST /api/instances/{name}/avatars/resync`** — endpoint REST
  que dispara el bulk. Requiere API token. Resuelve inbox via la
  config de la instancia y delega en `ResyncInstanceAvatars`.

### Use case

Tras adoptar v0.31.0+ por primera vez, los contactos viejos en
Chatwoot tienen letter-avatars. El sync automático solo aplica al
crear contacto o cuando llega un mensaje (sync flow) o cuando WA
emite `events.Picture`. Si tienes contactos inactivos, no se les
sincronizará nunca a menos que les llames manualmente. Este
endpoint permite hacerlo en una sola operación.

```bash
curl -X POST -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  https://qrsgen.example.com/api/instances/CONEXIA4/avatars/resync
```

Respuesta:
```json
{
  "instance": "CONEXIA4",
  "scanned": 234,
  "skipped": 5,
  "queued": 229,
  "pages": 16
}
```

### Migration notes

- Sin breaking changes. La feature es opt-in via llamada explícita
  al endpoint; sin invocarlo, el sistema funciona como en v0.31.2.

## [0.31.2] - 2026-05-28

Real-time avatar refresh: subscribe a `events.Picture` de whatsmeow
y fuerza re-sync inmediato cuando alguien (usuario o grupo) cambia
su foto. No depende del TTL del tracker — el evento es la señal
canónica.

### Added

- **`wameow.PictureHandler`** — nuevo callback type que se inyecta
  en `Conn.SetPictureHandler(h)`. Se dispara con `*events.Picture`
  (JID, pictureID, removed, resolver).
- **`wameow.Conn.handle`** — nuevo case para `*events.Picture`.
- **`manager.Manager.SetPictureHandler(h)`** — propaga el handler a
  cada Conn (existente y futura). Llamar antes de Bootstrap para
  capturar instancias auto-reconnect.
- **`Incoming.HandlePictureChange`** — orquesta la respuesta al
  evento: encuentra el contact en downstream via FindContact, resetea
  el LastID del tracker (forzar re-descarga), spawnea syncAvatar.
  Si el contact no existe, no hace nada (al primer mensaje del JID,
  sync()→CreateContact→maybeAvatarSync hará el sync inicial).

### Behavior

- Cuando un usuario cambia su foto en WhatsApp móvil → evento
  llega a qrsgen → en ~1s su avatar en Chatwoot también está
  actualizado. Sin esperar al siguiente mensaje, sin esperar al TTL.
- Mismo flow para grupos cuando el admin cambia la foto del grupo.
- Si `pictureID` está vacío y `removed=true`, syncAvatar internamente
  cachea el "" — el contact queda con su último avatar conocido en
  Chatwoot (no se elimina automáticamente).
- Sin tracker (`QRSGEN_AVATAR_REFRESH_TTL=0`), el evento sigue
  funcionando — se ignora el tracker pero el resto del flow opera.

### Migration notes

- Sin breaking changes. La feature está ON por default vía el
  wiring en `main.go`. Convs/contactos existentes en Chatwoot SE
  ven beneficiados sin acción operativa.

## [0.31.1] - 2026-05-28

Smart refresh del avatar: detecta cambios de foto en WhatsApp y
re-sincroniza solo lo necesario. Aplica a contactos EXISTENTES, no
solo a los recién creados (modo v0.31.0).

### Added

- **`WAResolver.GetProfilePictureID(ctx, jid) (string, error)`** —
  versión cheap de GetProfilePicture: solo devuelve el ID
  (hash/version) del avatar actual, no descarga la imagen. Permite
  comparar antes de decidir si re-descargar.
- **`internal/bridge/avatar_tracker.go`** — tracker en memoria
  (per-instancia + per-JID) con `ShouldCheck(TTL)`, `LastID`,
  `UpdateID`. ShouldCheck es atómica: bumpea timestamp en `true`
  para que múltiples goroutines concurrentes (burst de mensajes)
  no spawnen el mismo sync varias veces.
- **`Incoming.maybeAvatarSync`** — wrapper que gate-keepea el spawn
  de la goroutine via el tracker.
- **`QRSGEN_AVATAR_REFRESH_TTL`** (default `24h`) — env var nueva.
  `0` desactiva el refresh (modo v0.31.0: sync solo al crear).
- **7 tests** del avatar_tracker: primer chequeo, dentro/fuera de
  TTL, UpdateID, aislamiento per-JID, aislamiento per-instancia,
  concurrencia (burst de 20 goroutines, solo una ve `true`).

### Changed

- **`Incoming.syncAvatar` refactorizado** a flow smart:
  1. Get current ID (cheap metadata, no descarga).
  2. Si == lastKnownID → skip descarga.
  3. Si == "" → sin foto, cachear y exit.
  4. Si distinto → download + upload + update tracker.
  
  Antes (v0.31.0) siempre descargaba la imagen completa.
  
- **Aplica el sync tanto a contactos creados como existentes** (el
  callsite en sync() ya no está dentro del `if contact == nil`). El
  tracker decide si toca según TTL.

### Migration notes

- Sin breaking changes. Default ON, TTL 24h. Si quieres mantener
  el comportamiento exacto de v0.31.0 (solo al crear contact, sin
  refresh), setea `QRSGEN_AVATAR_REFRESH_TTL=0`.
- **`WAResolver` añade un método** (`GetProfilePictureID`). Mocks
  externos necesitan implementarlo.

## [0.31.0] - 2026-05-28

Sincroniza la foto de perfil de WhatsApp al avatar del contacto en
el downstream. Aplica tanto a contactos 1-on-1 (foto del usuario)
como a grupos (foto del grupo). Reemplaza los letter-avatars
autogenerados por Chatwoot por las fotos reales.

### Added

- **`WAResolver.GetProfilePicture(ctx, jid) ([]byte, string, error)`** —
  nuevo método de la interfaz. Hace `client.GetProfilePictureInfo` +
  HTTP GET a la URL devuelta. Devuelve `([], "", nil)` cuando el JID
  no tiene foto configurada (estado válido, no es error).
- **`Client.UploadContactAvatar(ctx, contactID, data, mime)`** —
  PUT multipart a `/api/v1/accounts/X/contacts/Y` con el avatar.
- **`Incoming.syncAvatar(ds, r, contactID, jid)`** — orquesta el sync
  fire-and-forget. Spawn en goroutine tras `CreateContact` exitoso.
- **`QRSGEN_AVATAR_SYNC`** (default `true`) — env var nueva.
  Setear a `false` desactiva la feature; los contactos siguen creándose
  pero sin avatar (Chatwoot pinta su letter-avatar default).

### Behavior

- **Solo en contact-creation**. Si el contacto ya existía en downstream,
  qrsgen NO refresca el avatar — un cambio de foto en WhatsApp tras la
  creación no se propaga. Refresh periódico queda para v0.31.1.
- **Fire-and-forget**. Errores de WA (foto privada, account restringida)
  o downstream (upload rechazado) loguean warning pero no bloquean el
  flujo del mensaje. El contact se crea aunque el avatar falle.
- **Timeouts**: 10s para fetch desde WA, 30s overall para todo el sync.
- **No bloquea el msg processing**. La goroutine corre paralela al
  envío del mensaje a downstream.

### Migration notes

- Sin breaking changes. La feature está ON por default — espera ver
  fotos reales en convs nuevas tras deploy. Convs/contactos existentes
  no se actualizan (no hay bulk sync; v0.31.2 si se quiere).
- **`WAResolver` añade un método**. Mocks externos del interface
  necesitan implementarlo. Trivial: return `nil, "", nil`.

## [0.30.2] - 2026-05-28

UX tweak: em-space (U+2003) entre nombre y code block del teléfono.

### Changed

- **`applyGroupSenderPrefix`** ahora intercala un em-space (U+2003,
  un carácter Unicode "wide space") entre el bold del nombre y el
  inline code del teléfono. Razón: en v0.30.1 quedaban tocándose,
  porque Chatwoot colapsa espacios normales a uno solo en render.
  El em-space NO se colapsa (es un codepoint distinto al `\x20`)
  y ocupa ~4x más, dando la respiración que faltaba.

### Migration notes

- Cambio cosmético. Si tu parser regex matchea exactamente
  ` ` entre el `**` y el `` ` ``, ahora hay un ` ` allí.

## [0.30.1] - 2026-05-28

UX tweak: teléfono en inline code block en lugar de italic + separador.

### Changed

- **`applyGroupSenderPrefix` ahora usa `**~<Name>** \`+digits\``**
  con el teléfono envuelto en backticks (inline code) en lugar de
  underscores (italic). Chatwoot renderiza inline code con fondo
  distintivo y fuente monospace, lo que separa visualmente el
  teléfono del nombre sin necesitar el dot `·` que añadimos en
  v0.29.7. Se ve más limpio y profesional.
- **Caso degenerate phone-only** ahora también va en code:
  `` `+digits`: `` en lugar de `_+digits_:`.

### Migration notes

- Cambio puramente cosmético. Parsers regex actualizar al nuevo
  patrón `\*\*~[^*]+\*\* \`\+\d+\`` si dependían del italic.

## [0.30.0] - 2026-05-28

Suprime el header de remitente en mensajes consecutivos del mismo
participante dentro de un grupo. Replica la convención visual de
WhatsApp (header en el primer msg del burst, nada en los siguientes).

### Added

- **`internal/bridge/group_tracker.go`** — tracker per-instancia +
  per-grupo del último sender visto. API: `RecordAndCheck(instance,
  chatJID, senderJID) bool`. Returns true si toca emitir header
  (sender distinto al previo, o TTL expirado, o no había registro).
  Estado en memoria — un restart de qrsgen reinicia los burst counters.
- **`QRSGEN_GROUP_HEADER_TTL`** (default `10m`) — env var nueva.
  Setear a `0` desactiva la feature (header siempre).
- **6 tests** del tracker: primer msg, burst del mismo sender, cambio
  de sender, TTL expirado, aislamiento per-grupo, aislamiento
  per-instancia.

### Changed

- **`Handle` en `incoming.go` ahora consulta el tracker** antes de
  aplicar el prefix de grupo. Mensajes `fromMe` (del agente) se
  registran como `_bot` para que el siguiente mensaje real reciba
  header correctamente tras una intervención del agente.

## [0.29.7] - 2026-05-28

UX tweak final del prefijo de grupo: middle dot como separador
visible (markdown colapsa N espacios a 1) + teléfono compacto sin
espacio entre CC y national.

### Changed

- **`applyGroupSenderPrefix` ahora usa `**~<Name>** · _+<digits>_`**
  con middle dot (·, U+00B7) entre name y teléfono. Razón: Chatwoot
  colapsa los espacios consecutivos a uno solo en render. Dot
  explícito siempre se ve.
- **`formatE164` simplificado al mínimo**: `"34604021705" → "+34604021705"`.
  El espacio entre CC y national también era ruido cuando el teléfono
  está adyacente al nombre — `+` ya marca el inicio del número.

### Removed

- **`detectCountryCode` + tablas `ccLen1/2/3`** — dead code tras
  simplificar formatE164. ~30 LoC menos.

## [0.29.6] - 2026-05-28

UX micro-tweak: 3 espacios entre nombre y teléfono en lugar de 2.

### Changed

- **`applyGroupSenderPrefix` ahora usa TRES espacios** entre el bold
  del nombre y el italic del teléfono:
  `**~<Name>**   _+CC ..._`. Razón: con 2 espacios el espacio entre
  ambos era demasiado tight; 3 marca mejor la separación visual.

## [0.29.5] - 2026-05-28

UX tweak: tilde delante del nombre, dentro del bold — matchea la
convención visual que WhatsApp usa para indicar remitente.

### Changed

- **`applyGroupSenderPrefix` ahora genera `**~<Name>**  _+CC ..._`**
  (tilde como primer char dentro del bold) en lugar de
  `**<Name>**  _+CC ..._`. Razón: el tilde delante es la pista visual
  que WhatsApp usa nativa para "esto es el sender del grupo", y
  reproducirlo hace que el chat lea más como WhatsApp y menos como
  un text dump genérico.
- Mismo `~` aplica al caso degenerate name-only: `**~<Name>**:`.

### Migration notes

- Cambio puramente cosmético. Parsers regex actualizar a
  `\*\*~[^*]+\*\*` si dependían del bold-name-pattern.

## [0.29.4] - 2026-05-28

UX tweak: el teléfono ahora solo separa el country code del national
number con un espacio; el national queda compacto.

### Changed

- **`formatE164` simplificado**: `"34604021705" → "+34 604021705"` en
  lugar de `"+34 604 02 17 05"`. Razón: el agrupamiento intra-national
  era ruido. Lo único que aporta lectura es el `+CC` separado del
  resto.
- **Tabla de CCs reconocidos sigue igual** (NANP, Europa, Latam, Asia
  operacional, Portugal, Israel, Emiratos). CCs no listados se dejan
  compactos con `+` delante (sin separación).

### Removed

- **`groupNationalNumber` y `groupByThree`** — funciones internas no
  longer needed con el nuevo formato. Eliminadas para reducir
  superficie.

### Migration notes

- Cambio puramente cosmético. Si un parser regex usaba el patrón
  `\+\d+( \d+){3,4}` (CC + 3-4 grupos), ahora debe ajustarse a
  `\+\d+ \d+` (CC + un solo grupo).

## [0.29.3] - 2026-05-28

UX tweak adicional del prefijo de grupo: nombre en **bold** para que
salte visualmente.

### Changed

- **`applyGroupSenderPrefix` ahora genera `**<Name>**  _+CC ..._`**
  (nombre en bold). Chatwoot renderiza `**...**` como negrita.
  Razón: en una conv con 3-5 senders el ojo necesita escanear quién
  habla; nombre en bold + teléfono italic da la jerarquía visual
  correcta sin sumar líneas.
- Mismo bold aplica al caso degenerate name-only: `**<Name>**:`.

### Migration notes

- Cambio puramente cosmético, sin nuevas env vars.
- Parsers regex actualizar a `\*\*[^*]+\*\*\s+_[^_]+_` si dependían
  del formato anterior.

## [0.29.2] - 2026-05-28

UX tweak del prefijo de grupo: quita los paréntesis del teléfono y
añade doble espacio de separación.

### Changed

- **`applyGroupSenderPrefix` ahora genera `<Name>  _+CC NNN NN NN NN_`**
  (dos espacios entre name y teléfono, teléfono italic sin paréntesis)
  en lugar de `<Name> _(+CC ...)_`. Razón: los paréntesis añadían
  ruido visual y el doble espacio marca mejor la separación entre
  foreground (nombre) y background (teléfono).
- **Degradación phone-only** ahora también va italic:
  `_+CC NNN ..._:\n<body>` en lugar de `+CC NNN ...:`. Consistencia
  con el caso con nombre.

### Migration notes

- **Sin nuevas env vars**, sin cambios de comportamiento del routing.
- Cambio cosmético: parsers que matcheaban `_\(\+[\d ]+\)_` necesitan
  actualizar al patrón `_\+[\d ]+_` (sin paréntesis). El doble espacio
  entre nombre y teléfono también puede afectar regex.

## [0.29.1] - 2026-05-28

UX patch del prefijo de grupo: nombre primero, teléfono en "segundo
plano" (italic + paréntesis), y teléfono formateado E.164 con
espaciado por país. La readabilidad mejora notablemente cuando
varios participantes intervienen en la misma conversación.

### Changed

- **`applyGroupSenderPrefix` ahora genera `<Name> _(+CC NNN NN NN NN)_`**
  en lugar de `+<phone> - <Name>:`. Chatwoot renderiza el `_..._` como
  italic, así que el teléfono queda visualmente subordinado al nombre.
  Razón: con 3+ senders distintos en una conv del grupo, el "+phone -"
  inicial le robaba prioridad al contenido del mensaje.
- **Teléfono formateado E.164 por país**. España (`+34`) usa 3-2-2-2
  (e.g. `+34 604 02 17 05`, matcheando el estilo de Evolution); resto
  de países agrupa por 3 desde la izquierda (`+33 612 345 678`). CCs
  reconocidos: NANP (+1), Europa común (+30..+49), Latam (+51..+58),
  Asia operacional, Portugal (+351), Israel (+972), Emiratos (+971).
  Para CCs no listados, deja el número compacto con `+` delante.

### Added

- **`formatE164`, `detectCountryCode`, `groupNationalNumber`,
  `groupByThree`** — helpers en `internal/bridge/incoming.go`. 12 tests
  cubriendo Spain mobile/landline, France, Germany, UK, US, Portugal,
  Italy, Mexico, CCs desconocidos, y casos degenerados.

### Migration notes

- **Sin nuevas env vars**. `QRSGEN_GROUP_PREFIX_SENDER=false` sigue
  desactivando el prefix entero como antes.
- **Cambio de formato observable** en Chatwoot/downstream para
  integraciones que parseaban el body raw esperando `+phone - Name:`.
  Si tienes un parser regex de n8n que dependa del formato viejo,
  actualízalo al nuevo (`<Name> _\(\+[\d ]+\)_`) o desactiva el prefix.

## [0.29.0] - 2026-05-28

Conversaciones de WhatsApp en grupo se reflejan correctamente en
downstream: el nombre del grupo va al título de la conv, y cada
mensaje incoming lleva prefijo identificando al participante.

### Added

- **`wameow.WAResolver.GroupSubject(jid)`** — nuevo método de la
  interfaz que resuelve el subject (nombre visible) de un grupo
  via `client.GetGroupInfo()`. Implementación con cache TTL de 10
  min para positivos / 1 min para negativos: GetGroupInfo es un
  round-trip al server WA y no queremos hacerlo por cada mensaje.
- **`QRSGEN_GROUP_PREFIX_SENDER`** (default `true`) — env var nueva
  que controla si qrsgen prefija el body de los mensajes incoming
  de grupos con la identidad del remitente. Sin él, en una misma
  conv del downstream múltiples participantes son indistinguibles.
  Formato: `+<phone> - <name>:\n<body>` cuando hay teléfono y push
  name; degrada a uno solo si falta el otro. Si el participante
  no es identificable (LID sin mapping ni push name), el body se
  postea sin prefijo en lugar de basura.
- **Tests nuevos** en `internal/bridge/incoming_test.go` cubriendo
  los 6 cases de `applyGroupSenderPrefix` (PN, LID resolvable, LID
  no resolvable, body vacío, sin identificación posible) +
  `GroupSubject` en el `fakeResolver`.

### Changed

- **`internal/bridge/incoming.go` Handle ahora distingue grupos**.
  Para `chat.Server == g.us` consulta `GroupSubject(chat)` primero
  (en lugar de `ContactName`, que solo cubre contactos individuales
  porque `Store.Contacts` no indexa grupos). Si el subject no
  resuelve, NO cae a `PushName` del participante (eso pondría el
  nombre del primer remitente como título del grupo entero).

### Migration notes

- **Sin breaking changes de API**. La nueva env var tiene default
  `true` — si tu integración n8n parseaba el body raw sin esperar
  el prefijo `+34… - Name:\n`, ponla a `false` o ajusta tu parser.
- **`WAResolver` añade un método nuevo** (`GroupSubject`). Si tienes
  un mock externo del interfaz, necesitas añadirlo. Implementación
  trivial que devuelva `("", false)` mantiene compat.

## [0.28.5] - 2026-05-27

Public error codes: contrato estable para que integradores pattern-
matcheen errores programáticamente sin parsear strings.

### Added

- **`internal/errcode/` package** con códigos string públicos del
  patrón `QRSGEN_<CATEGORY>_<REASON>`. Inicial set: `SpamguardBlocked`,
  `HMACMismatch`, `PayloadInvalid`, `QueueFull`, `InstanceNotFound`,
  `TenantNotFound`, `Internal`. `HumanText(code)` devuelve descripción
  humana en español para mostrar en UI.
- **Doc nuevo** `docs/api/error-codes.md` con la tabla completa +
  ejemplos de uso (Python, n8n).

### Changed

- **Respuestas de error ahora incluyen `error_code` + `error`** además
  de los campos contextuales. Ejemplo para spamguard 422:
  ```json
  {
    "error_code": "QRSGEN_SPAMGUARD_BLOCKED",
    "error": "Bloqueado por QRsGEN — duplicado...",
    "status": "blocked"
  }
  ```
  Backward compatible: el HTTP status sigue siendo el mismo, y los
  campos antiguos (`status`, `reason`) se mantienen donde aplicaba.

## [0.28.4] - 2026-05-27

Spamguard block ahora se refleja visualmente en el chat del cliente:
mensaje en rojo en lugar de verde.

### Changed

- **`POST /api/instances/:name/webhook` devuelve HTTP 422** cuando
  spamguard bloquea un outgoing duplicado. Chatwoot (y cualquier
  downstream que respete el contrato `api_channel`) marca entonces
  el mensaje como `failed` (icono rojo) en lugar de `sent` (verde).
  El agente sabe al instante que su mensaje no se entregó. Antes
  qrsgen devolvía 200 silenciosamente y el block solo se notificaba
  vía evento lifecycle externo.
- Nuevo sentinel error público `bridge.ErrSpamguardBlocked` para que
  callers puedan distinguir el block del resto de errores.

## [0.28.3] - 2026-05-27

UX del `spam_blocked` event: contexto para que el integrador pueda
linkear al mensaje y avisar en la conversación del cliente.

### Added

- **`spam_blocked` lifecycle event ahora incluye `msg_id`, `conv_id`
  y `remote_jid`** en los extras del payload, además de `count` y
  `preview` que ya estaban. Permite que el integrador (n8n / Omnia)
  construya un link directo al mensaje bloqueado (`/conversations/{conv_id}`)
  y poste una nota interna en la conv del cliente avisando que el
  outgoing no se entregó. Antes el agente veía su mensaje como `sent`
  en Chatwoot sin saber que qrsgen lo había bloqueado.

## [0.28.2] - 2026-05-27

Tres pulidos observables: una race real arreglada + métrica de versión
+ UX del key cifrado.

### Fixed

- **Race "Error al enviar" en body de `backend_started`** tras deploy.
  Síntoma observable: pill verde OK + body en rojo justo después. Causa:
  el broadcast se disparaba antes de que el HTTP server del nuevo
  container estuviera accept-ready, así que el dispatch de vuelta de
  Chatwoot al webhook hit connection-refused. Fix: el broadcast ahora
  hace polling al `/api/health` propio (timeout 5s) antes de emitir,
  garantizando que estamos aceptando POSTs.

### Added

- **`qrsgen_version_info{version="X.Y.Z"} 1`** — nueva métrica
  Prometheus (info-style gauge fijo a 1) para que dashboards Grafana
  hagan join contra otras series y muestren la versión activa de
  qrsgen. Estándar de ops desde Prometheus 2.x.

### Changed

- **`OUTBOX_ENCRYPTION_KEY`** ahora acepta también base64 URL-safe (con
  o sin padding), no solo el estándar. Soluciona friction cuando la
  key viene de un secret manager (Vault, GitHub Actions) que normaliza
  a URL-safe. Tests añadidos para las 4 variantes.
- **Sample defaults** del repo (`docker-compose.yml` + `.env.example`)
  bumped de `0.19.1` (estancado meses) a `0.28.1`. Sin impacto en
  binarios — solo template files para nuevos integradores.

## [0.28.1] - 2026-05-27

Cleanup pass post-marathon: cabos sueltos de v0.27 + v0.28, sin
nuevas features. Incluye un nuevo campo `version` en lifecycle events
(útil para integradores que quieran mostrar el tag del binario al
usuario, e.g. "QRsGEN v0.28.1 operativo").

### Added

- **`version` en el payload de todos los lifecycle events**:
  emitidos vía `emitLifecycleWebhook` y `emitCustomWebhook`. Inyectado
  por `mgr.SetVersion(version)` desde main. El integrador puede
  renderizar el tag en sus mensajes (e.g. el bot Omnia ahora muestra
  "QRsGEN vX.X.X" en los pills de backend_started / backend_restarting).

### Changed

- **Outbox schema**: `payload` (JSONB) ahora NULLable. La migración v0.27
  insertaba `'null'::jsonb` como placeholder cuando había cifrado; ahora
  va NULL real. Migración idempotente (ALTER COLUMN DROP NOT NULL).
- **`tenant.Resolver.Patch`** usa `strings.Join` en vez del helper local
  `joinComma` que reinventaba la rueda. Sin cambio funcional.

### Fixed

- **`docs/deployment/env-vars.md`**: faltaba `OUTBOX_ENCRYPTION_KEY`
  (introducido en v0.27.0). Default de `QRSGEN_VERSION` actualizado
  (era `0.23.0-rc1` desde hace meses).
- **`docs/security/outbox-encryption.md`**: la nota sobre KEK/DEKs
  decía "considerado para v0.28+" pero v0.28 ya salió sin eso;
  cambiado a "futuras versiones".

## [0.28.0] - 2026-05-27

Spamguard persistence: cierra la known limitation de v0.21.0 sobre
contadores in-memory que se perdían en restarts.

### Added

- **`SpamguardTracker` persistido en DB** (vía `SetPool` + `Warmup`).
  Nuevas tablas `bridge_spamguard_recent` (last-2 hashes per
  instance+jid_user) y `bridge_spamguard_counter` (counter acumulado
  per instance). El bloqueo dup sobrevive a restarts: un agente que
  intente enviar el mismo mensaje justo después de un deploy sigue
  siendo bloqueado.
- **Cleanup cron** (cada 30 min) que purga `bridge_spamguard_recent`
  con `updated_at > 1h`. La ventana de relevancia del spamguard es
  corta — más viejo que 1h sin actividad ya no es spam realista.

### Changed

- `SpamguardTracker.CheckAndRecord` ahora hace best-effort UPSERT
  tras cada decisión (in-memory cambia primero; DB después). Hot path
  tolera fallos DB sin bloquear el flow.

### Known limitations resueltas

- ✅ v0.21.0: "Spamguard counter in-memory: se resetea en cada
  restart" — hecho en v0.28.0.

## [0.27.0] - 2026-05-27

Outbox encryption at-rest: cierra una known limitation pendiente desde
v0.23.0. Opt-in vía env, backward compatible.

### Added

- **AES-256-GCM encryption** opcional para payloads en
  `bridge_outgoing_queue`. Activar con `OUTBOX_ENCRYPTION_KEY` (32
  bytes en base64). Si vacío → no cifra (compat). Las filas cifradas
  llevan `nonce IS NOT NULL`; las legacy `nonce IS NULL` se entregan
  en claro (backward compat durante migración).
- Nuevo helper `internal/outbox/crypto.go` con `sealPayload` /
  `openPayload` + `DecodeEncryptionKey` que valida tamaño.
- Schema migration idempotente: añade `payload_enc BYTEA` y `nonce
  BYTEA` a `bridge_outgoing_queue`.
- Doc `docs/security/outbox-encryption.md` con setup, rotación y qué
  vector cubre (DBA compromise / dump de Postgres).

### Changed

- `outbox.Outbox` añade `SetEncryptionKey([]byte)` setter. Si la key
  está set, `Enqueue` cifra y persiste en `payload_enc + nonce`;
  `DrainOnce` / `ExpireOnce` descifran al leer.

### Known limitations (v0.27.0)

- Rotación segura requiere drenar el outbox antes de cambiar la key.
  Rotación dual-key sin pausa queda para futuras versiones.
- Cifrado per-tenant (KEK + DEKs) queda para v0.28+.

## [0.26.0] - 2026-05-27

Tenant config completeness: `PATCH` parcial + HMAC per-tenant para
aislar credenciales del webhook entrante entre clientes en multi-
downstream.

### Added

- **`PATCH /api/tenants/:owner_tag`**: update parcial. Solo modifica
  los campos del body — el resto se preserva. Útil para rotar
  `webhook_hmac_secret` sin reenviar el `downstream_api_token`. Audit
  log registra `tenant.patch` con `metadata.fields` (nombres, no
  valores). Mismas keys whitelisteadas que en PUT.
- **`bridge_tenant.webhook_hmac_secret`** (nueva columna, migración
  idempotente). HMAC secret per-tenant write-only — nunca se devuelve
  en GET (igual que `downstream_api_token`).
- **HMAC verify per-tenant**: el middleware del webhook entrante
  resuelve el secret efectivo de la instancia: si su `owner_tag` tiene
  tenant con `webhook_hmac_secret` set → usa ese; sino → fallback al
  `WEBHOOK_HMAC_SECRET` global del env; sino → bypass (compat).

### Changed

- `PUT /api/tenants/:owner_tag` ahora acepta `webhook_hmac_secret`.
  Semántica de **replace**: campos no enviados se vacían (incluido el
  secret). Para preservar selectivamente, usa PATCH.
- `tenant.Resolver` añade `Patch(ctx, ownerTag, fields)` con
  whitelist de campos. Get/List/Warmup/Set actualizados para leer y
  escribir el nuevo campo (`COALESCE(webhook_hmac_secret, '')`).

## [0.25.0] - 2026-05-27

Observabilidad per-tenant: cuando un proceso qrsgen sirve varios
clientes vía `owner_tag`, ahora puedes separar métricas y entradas de
audit por tenant sin tener que cruzar contra DB.

### Added

- **Label `owner_tag`** en los counters Prometheus per-instance:
  `qrsgen_messages_total`, `qrsgen_spamguard_blocks_total`,
  `qrsgen_lifecycle_events_total`, `qrsgen_message_dispatch_errors_total`.
  Vacío para instancias sin tenant configurado. Las queries existentes
  sin filtrar por `owner_tag` siguen funcionando (Prometheus agrega).
- **`Router.OwnerTagFor(ctx, instance)`** nuevo método en la interfaz —
  `*Client` devuelve `""` (single-downstream), `*Registry` resuelve
  desde el cache TTL existente. Permite a callsites de métricas
  etiquetar sin nuevos lookups DB.
- **`GET /api/audit?owner_tag=tenant-acme`** — filtro nuevo en el
  endpoint de audit. Subquery sobre `bridge_instance.owner_tag`. Si se
  combina con `?instance=X` el AND es lógico. Sin filtro = todo.
  Permite que un admin de tenant solo vea sus entradas sin acceso al
  resto del audit log.

### Changed

- `audit.Logger.Query` mantiene firma original; nueva `QueryFiltered`
  acepta el segundo filtro opcional. `Query` delega en `QueryFiltered`
  con `ownerTag=""` para backward compat.
- Manager expone `SetOwnerTagResolver(r)` para inyectar el `Registry`
  (o cualquier impl de la interface). Sin él, el label sale como `""`.

## [0.24.2] - 2026-05-27

Bug fix de "🎉 espurio" + robustez en `backend_started` + cache 30s
del endpoint público + timestamps en tenant API.

### Fixed

- **`connected` espurio tras EventConnected duplicado de whatsmeow**:
  el dispatcher ahora trackea `connectedEmitted` per-instance. Mientras
  la sesión esté viva, EventConnected adicionales (re-handshake silent,
  session renewal sin Disconnect intermedio) **no re-emiten** el evento
  `connected` (que en n8n se renderiza como "Conexión establecida 🎉").
  El flag se limpia en `disconnect` / `logged_out`, dejando que la
  próxima sesión emita normalmente. Caso reportado: SAT-MARC mostrando
  🎉 a las 5:17 AM sin desconexión previa visible.

### Changed

- **`BroadcastBackendStarted` ahora espera a `state=ready`** (timeout
  20s) por instancia antes de reportar `connected: true`. Sustituye al
  snapshot fijo de `IsConnected()` a los 8s post-boot, que daba falsos
  negativos cuando WhatsApp negociaba handshake lento. El pre-sleep de
  8s en `cmd/server` queda eliminado (la espera vive en el broadcast).
- **`/api/public/stats` cacheado in-memory 30s**: la landing hace
  polling cada 10s; sin cache hacíamos 5 SELECTs por request. Ahorra
  ~95% de hits a DB sin sacrificar frescura.
- **`tenant.Resolver.{List,Get,Warmup,Set}` devuelven `created_at` y
  `updated_at`**: visibles vía `/api/tenants*` para UIs de admin. El
  `PUT` los recibe del `RETURNING` del upsert y los persiste en cache.

## [0.24.1] - 2026-05-27

Fix de `version` hardcodeada y simetría con multi-downstream en el
endpoint público.

### Added

- **`tenants_total`** en `/api/public/stats` — cuenta filas en
  `bridge_tenant`. Completa la familia `instances_total` /
  `installations_total` / `tenants_total`.

### Fixed

- **`/api/health` y `/api/public/stats` ahora reportan la versión real
  de cada release**. Antes devolvían `"0.23.0"` hardcoded sin importar
  el tag. Ahora la inyectamos en build via
  `-X main.version={{.Version}}` (GoReleaser). Builds locales reportan
  `"dev"`.

## [0.24.0] - 2026-05-27

Multi-downstream real: un solo proceso puede servir varios clientes con
config downstream propia por `owner_tag`, manteniendo backward compat
total con el fallback `DOWNSTREAM_*` del env.

### Added

- **Multi-downstream real** (`internal/tenant` + `internal/downstream/registry.go`):
  un proceso qrsgen puede servir varios downstreams distintos, enrutados
  por `bridge_instance.owner_tag` vía la nueva tabla `bridge_tenant`
  (owner_tag PK + downstream URL/token/account/inbox). Cache in-memory per
  tenant con invalidación en cada upsert/delete; instance→owner_tag con
  TTL 30s. Si una instancia no tiene `owner_tag`, o no hay tenant para él,
  cae al fallback global (`DOWNSTREAM_*` del env) — totalmente backward
  compatible.
- **Endpoints `/api/tenants/*`**: `GET` (list/detail sin tokens),
  `PUT /:owner_tag` (upsert con `downstream_api_token` solo de escritura),
  `DELETE /:owner_tag` (instancias caen al fallback global). Cada cambio
  invalida el cache del `*Client` por tenant.

### Changed

- `bridge.Incoming` y `bridge.Outgoing` dependen del nuevo interface
  `downstream.Router` en lugar de `*downstream.Client`. `*Client` lo
  implementa devolviéndose a sí mismo, así el callsite single-downstream
  no necesita ningún cambio.
- `resolveInbox` (incoming flow) ahora prioriza
  `bridge_tenant.downstream_inbox_id` antes que el per-instance y el env.

## [0.23.0] - 2026-05-26

Robustez producción + capa de monetización ligera + cero pérdida en
restarts + telemetría pública opt-in + 64 sub-páginas docs.

### Added (post rc1)

- **Lifecycle webhook retry exponencial** para eventos críticos
  (`strike`, `ban_risk`, `outgoing_expired`, `logged_out`,
  `spam_blocked`, `backend_restarting`). 3 reintentos async con
  backoff 5s → 30s → 5min. Métrica
  `qrsgen_lifecycle_webhook_retries_total{event,outcome}` para
  alerting (`outcome=exhausted` → el downstream lleva ≥5min caído).
- **Health endpoint enriquecido**: `/api/health` ahora hace DB ping
  (timeout 2s), reporta `outbox_pending`, `uptime_seconds`,
  `checks.db.{ok, latency_ms}`. Devuelve `503` con
  `status: "degraded"` si DB no responde → Docker HEALTHCHECK falla
  → Swarm restart automático.
- **`installations_total`** en `/api/public/stats` (simetría con
  `instances_total`). Cuenta DISTINCT instances en el audit log
  → sobrevive a DELETE.
- **`instance.paired` registrado en audit log**: ahora cada pairing
  exitoso queda como entrada en `bridge_audit_log`. La métrica
  `qrs_scanned_total` del endpoint público lo cuenta.
- **Cards live en la landing** (`docs/home/status.md`): 6 cards
  conectadas a `/api/public/stats` con polling 10s y toggle
  on/off persistido en localStorage. Fetch con AbortController
  (timeout 3s) para no bloquear UI si el endpoint público está
  caído.
- **Documentación masivamente ampliada**:
  - 64 sub-páginas en sidebar Material (estructura por dominio).
  - Sección **Migrations** con 7 páginas (Evolution / wajs / Baileys
    / SaaS overview / Whapi.cloud / MaytAPI / Salir de qrsgen).
    Schemas de origen detallados en cada una.
  - Sección **Integrations** con n8n + Python (cliente httpx
    + FastAPI receiver).
  - Glosario al final de cada sub-página (>50 términos técnicos
    explicados consistentemente).
- **`tools/migrate/`**: `bulk-provision.py`, `validate.py`,
  `export-config.py` (Python httpx) para automatizar migraciones
  desde plataformas existentes.

### Added (en rc1, inalterado)

- **Outbox persistido** (`internal/outbox` + tabla `bridge_outgoing_queue`).
  El endpoint `POST /api/instances/:name/webhook` ahora encola el payload
  cuando la instancia no está conectada y devuelve `202 {status:"queued"}`.
  Un drainer reentrega cada 5s; mensajes sin entregar a los 5 min expiran
  y emiten el evento lifecycle `outgoing_expired`. Per-instance backlog
  hard-cap (200) + retry budget (5 attempts) + audit hooks.

### Added

- **BanWatcher** (`internal/banwatch` + `GET /api/instances/:name/ban-risk`):
  detector proactivo con tres señales (velocity / diversity / delivery
  ratio) sobre ventanas rolling, score 0-1 + nivel ok|low|moderate|high.
  Emite el evento lifecycle `ban_risk` en rising edge para que el
  integrador reduzca el ritmo.
- **Usage tracking persistido** (`internal/usage` + `bridge_usage_daily`):
  contadores diarios in/out + spamguard + lifecycle, flush a Postgres
  cada 60s. Endpoints `/api/instances/:name/usage` (por instancia),
  `/api/usage` (todas) y `/api/usage/summary` (agregado mensual por
  `owner_tag` — el campo principal para billing).
- **Audit log inmutable** (`internal/audit` + `bridge_audit_log`). Tabla
  append-only con triggers plpgsql que rechazan `UPDATE` y `DELETE`.
  Hooks en provision / patch / delete / boot / outbox.{enqueue,expire,failed}.
  Endpoint `GET /api/audit?instance=&limit=`.
- **`owner_tag` en `bridge_instance`**: string libre que el integrador
  usa para correlacionar instancias con su modelo de tenants. qrsgen no
  lo interpreta. Aceptado en `POST /api/instances` y `PATCH …`, devuelto
  en `GET …`, agrupado en `/api/usage/summary`.
- **HMAC opcional** del webhook entrante (`WEBHOOK_HMAC_SECRET`). Cuando
  está set, exige `X-Qrsgen-Signature: sha256=<hex>` calculado como
  HMAC-SHA256(secret, raw body). Mismatches devuelven 401. Cuando está
  vacío, comportamiento previo (backward compat).
- **HEALTHCHECK** en `Dockerfile` y `Dockerfile.release` vía flag
  `-healthcheck` del propio binario (distroless friendly, no necesita
  curl). Interval 30s, timeout 5s, start-period 20s.
- **Backups Postgres** automatizados: script `ops/backup/postgres-backup.sh`
  + systemd timer (`qrsgen-postgres-backup.timer`) — daily 03:00, retention
  7 daily + 4 weekly, restore runbook en `ops/backup/README.md`.
- **Read-only rootfs** del container + tmpfs `/tmp` 64 MB en compose. El
  binario no escribe a disco; cualquier mutación del fs es signal de
  compromiso.
- **Lifecycle suavizado** para evitar paneles ruidosos:
  - 60s de silencio antes de emitir `unreachable` (blip silencioso si
    reconecta en ese rango).
  - 5s de estabilidad antes de emitir `reconnected`.
- Tests: `internal/config` (100%), `internal/downstream` (79%),
  `internal/banwatch` (95%), `internal/usage`, `internal/audit`
  (integration gated por `INTEGRATION_PG_DSN`). Tests HMAC dedicados.
- Documentación: API completa para todos los endpoints nuevos,
  arquitectura reescrita reflejando outbox / banwatch / audit / usage,
  sección hardening en security.md.

### Changed

- Migración a APIs no-deprecadas de whatsmeow: `DownloadAny` ahora rutea
  por `client.Download` con `DownloadableMessage`; `simpleTextMessage`
  devuelve `*waE2E.Message` (drop `binary/proto`). `SA1019` re-activado en
  golangci-lint.
- `errorlint` reactivado: 5 comparaciones `err == sentinel` migradas a
  `errors.Is`.
- Naming genérico en docs y ejemplos: `SAT-XXX` → `whatsapp-main`,
  `SAT-ALBERT` → `whatsapp-sales`, `<INSTANCE_NAME>` como placeholder.
- `update_config.order: stop-first` en compose (previene race de dos
  containers compitiendo por el mismo JID WhatsApp durante deploy — a
  cambio de ~15s downtime; el outbox lo cubre).

### Security

- HMAC verify opcional del webhook entrante.
- Read-only rootfs + tmpfs `/tmp` 64 MB.
- Audit log inmutable a nivel DB.
- Dependencias actualizadas: `github.com/jackc/pgx/v5` 5.7.1→5.9.2 (cierra
  GHSA-9jj7-4m8r-rfcm critical + GHSA-j88v-2chj-qfwx low),
  `filippo.io/edwards25519` 1.1.0→1.1.1 (GHSA-fw7p-63qq-7hpr),
  `github.com/caarlos0/env/v11` 11.2.2→11.4.1, Echo v4 (CVE
  GO-2025-3553 en `golang-jwt/jwt v3` transitivo cerrado).
- Workflows actualizados: `actions/setup-go@v6`, `docker/login-action@v4`,
  `goreleaser/goreleaser-action@v7`, golang docker 1.25 → 1.26.

### Known limitations

- Outbox payloads sin cifrado en disco — para multi-tenant serio
  se debería cifrar por tenant.
- Single downstream por proceso aún — usar `owner_tag` + un proceso
  por downstream como workaround.
- BanWatcher per-process (no comparte estado entre réplicas — qrsgen
  corre `replicas: 1` por diseño así que no es problema en producción
  típica).

## [0.21.0] - 2026-05-22

Primera release pública.

### Added

- Multi-instance orchestrator: un binario gestiona N sesiones WhatsApp.
- Lifecycle events: `qr_generated`, `paired`, `connected`, `reconnected`, `unreachable`, `disconnected`, `logged_out`, `strike`, `spam_blocked`, `backend_restarting`, `backend_started`.
- Bridge incoming/outgoing con formato `Channel::Api`-compatible.
- Spamguard: tracker in-memory de últimos 2 mensajes por (instance, jid) — bloquea duplicados back-to-back.
- LID/PN twin dedup con ventana configurable (default 10s).
- Bearer auth (`QRSGEN_API_TOKEN`) protege endpoints administrativos. `/api/instances/:name/webhook` exento.
- Egress firewall script + watcher systemd: allowlist Meta CIDRs + LAN, DROP el resto.
- Métricas Prometheus en `/metrics`: messages_total, spamguard_blocks_total, lifecycle_events_total, message_dispatch_errors_total, active_instances, total_instances.
- Healthcheck `/api/health` (sin auth) para liveness/readiness probes.
- Idempotencia de outgoing por `chatwoot_msg_id` (vía `Deduper.SeenIncomingMsg`).
- Stack Docker Compose portable con env vars + ejemplo `.env.example`.
- Documentación: architecture, api, deployment, security, operations, n8n-example.
- 12 unit tests en `internal/bridge` (spamguard tracker, normalize, hashContent).

### Security

- Auth Bearer en API.
- Egress filtering vía iptables.
- Distroless container image.

### Known limitations

- Multi-tenant aún no soportado: un proceso qrsgen sirve un solo downstream.
- Spamguard counter in-memory: se resetea en cada restart.
- LID twin del cliente: dedup limpia downstream pero el destinatario sigue recibiendo 2 msgs si WhatsApp hace dispatch dual.

[Unreleased]: https://github.com/rricajos/qrsgen/compare/v0.28.5...HEAD
[0.28.5]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.5
[0.28.4]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.4
[0.28.3]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.3
[0.28.2]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.2
[0.28.1]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.1
[0.28.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.0
[0.27.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.27.0
[0.26.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.26.0
[0.25.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.25.0
[0.24.2]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.2
[0.24.1]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.1
[0.24.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.0
[0.23.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.23.0
[0.21.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.21.0
