# Características destacadas

## Multi-instancia real

Un binario gestiona N números independientes, cada uno con su WebSocket
contra Meta y su estado aislado. Sin proceso separado por instancia.

## Outbox persistido

Cola en Postgres con TTL de 5 minutos. Cuando una instancia está
disconnected y llega un outgoing, qrsgen lo encola y lo entrega al
volver. **Cero pérdida en restarts cortos.**

## BanWatcher proactivo

Tres señales con thresholds configurables:

- **Velocity**: mensajes por minuto.
- **Diversity**: JIDs únicos contactados por ventana.
- **Delivery ratio**: éxitos / intentos.

Score 0-1 + level cualitativo. Emite el evento `ban_risk` cuando cruza
umbrales, para que tu sistema reduzca ritmo antes de que WhatsApp
sancione.

## Audit log inmutable

Tabla `bridge_audit_log` con triggers PL/pgSQL que rechazan UPDATE y
DELETE. Cualquier operación queda registrada con timestamp y metadata
JSONB. **Tamper-evident a nivel DB.**

## Usage tracking + facturación

Counters diarios persistidos en `bridge_usage_daily`. Endpoint
`/api/usage/summary` agrega por `(owner_tag, mes)` listo para billing
multi-tenant ligero. qrsgen no decide pricing — solo expone los hechos.

## Sincronización de avatares WhatsApp

qrsgen descarga la foto de perfil de cada contacto/grupo WhatsApp y la
sube al downstream como avatar. Tres capas de sincronización:

- **Al crear contacto** (v0.31.0) — primera foto disponible.
- **Refresh con TTL** (v0.31.1) — tracker en memoria que compara
  `info.ID` (cheap metadata, no descarga) cada `QRSGEN_AVATAR_REFRESH_TTL`
  y solo descarga si cambió.
- **Tiempo real** (v0.31.2) — subscribe a `events.Picture` de whatsmeow:
  cuando el usuario cambia su foto en WhatsApp móvil, el avatar
  downstream se actualiza en ~1s sin esperar al siguiente mensaje.

Más un endpoint `POST /api/instances/:name/avatars/resync` (v0.31.3) para
**backfill** de contactos viejos (creados antes de v0.31.x o inactivos).
Detalles completos en [Avatar sync](../integrations/avatar-sync.md).

## Formato adaptativo del prefijo de grupo

Desde v0.32.0, el prefijo que qrsgen antepone al body de mensajes de
grupo se adapta según si el remitente está guardado en la libreta del
número conectado:

- **Contacto en agenda** (FullName o FirstName en whatsmeow): solo nombre.
  ```
  **~Jean Paul**
  hola buenas
  ```
- **Solo push name** (no guardado): nombre + código de teléfono para que
  el agente pueda identificar al desconocido.
  ```
  **~Richard** `+34604021705`
  hola buenas
  ```

Sin env vars: el comportamiento depende del estado del contact store
de whatsmeow, que se nutre de la cadena Google Contacts → libreta del
móvil → app WA → whatsmeow. Cubre el caso LID-anonymizado-en-grupos
resolviendo a PN antes de decidir. Detalles en
[Formato del prefijo de grupo](../integrations/group-sender-format.md).

## Sincronización de reacciones WhatsApp

Desde v0.33.0, cuando un usuario reacciona a un mensaje en WhatsApp
(long-press → emoji), qrsgen propaga la reacción al downstream como
un nuevo mensaje incoming. Antes de esta versión los eventos
`ReactionMessage` se descartaban silenciosamente.

- **Contacto en agenda**:
  ```
  **~Jean Paul** reaccionó con 👍
  ```
- **Sender no guardado en grupo**:
  ```
  **~Richard** `+34604021705` reaccionó con ❤️
  ```
- **Reacción retirada**:
  ```
  **~Jean Paul** _quitó su reacción_
  ```

Mismo path platform-agnostic (`downstream.Router.PostMessage`), mismo
name resolver con `IsContactSaved` que el prefijo de grupo. Master
switch via `QRSGEN_REACTIONS_SYNC` (default `true`). Las reacciones
del propio bot owner (`IsFromMe=true`) se ignoran. Detalles en
[Sincronización de reacciones](../integrations/reactions-sync.md).

## Typing indicators, read receipts y mark-as-read bidireccional

Desde **v0.34.0**, **v0.34.1** y **v0.39.0**, qrsgen propaga señales
de interacción en tiempo real en ambas direcciones:

- **Typing indicators (v0.34.0)** — cuando el cliente WhatsApp empieza
  a escribir (`composing`) o se detiene (`paused`), qrsgen llama
  `POST /toggle_typing_status` y el agente ve "está escribiendo..." en
  el panel del downstream. Throttle in-memory per-conversación: cambios
  de estado siempre emiten, mismo estado dentro de 4s se silencia para
  no inundar al downstream con keystrokes repetidos. Master switch via
  `QRSGEN_TYPING_SYNC` (default `true`).
- **Read receipts incoming (v0.34.1)** — cuando el cliente abre el chat
  y lee los mensajes del agente, qrsgen llama `POST /update_last_seen`
  con `agent_last_seen_at` y `contact_last_seen_at` ambos igual al
  timestamp del receipt. La UI marca los mensajes del agente como
  leídos (equivalente al doble tick azul). Solo se propagan `read` y
  `read-self`; `delivered`, `played`, `sender` se ignoran por menos
  accionables. Master switch via `QRSGEN_READ_RECEIPTS_SYNC` (default
  `true`).
- **Mark-as-read outgoing (v0.39.0)** — cierra el ciclo bidireccional:
  cuando el agente abre la conversación en Chatwoot y la marca como
  leída, qrsgen propaga `MarkRead` a WhatsApp para que el cliente
  remoto vea el doble check azul sobre sus mensajes incoming. qrsgen
  registra cada WAID en un tracker in-memory (`waidTracker`) tras un
  `PostMessage` exitoso, y al recibir el webhook `conversation_updated`
  drena los WAIDs `≤ agent_last_seen_at` y llama `client.MarkRead()` de
  whatsmeow. **Requiere suscribir el evento `conversation_updated`** en
  el webhook de Chatwoot (sin esa config el feature no rompe, solo no
  marca). Master switch via `QRSGEN_MARK_AS_READ_OUTGOING` (default
  `true`).

Las tres son fire-and-forget. Desde v0.39.0 el `MarkRead` outgoing es la
única excepción a la convención "read-only sobre WhatsApp"; typing y
edición de perfil siguen siendo read-only. Limitaciones: grupos solo
muestran un indicador agregado ("alguien está escribiendo..."); privacy
settings del sender pueden ocultar receipts (cobertura parcial); el
`waidTracker` es in-memory y se pierde en restart, así que mensajes
leídos durante downtime no se marcan retroactivamente en WA. Detalles
en [Presencia y read receipts](../integrations/presence-and-receipts.md).

## Soporte de contenido de mensajes (location, polls, media polish)

Desde **v0.36.0**, **v0.37.0** y **v0.38.0**, qrsgen extiende
`extractTextContent` / `extractMedia` para serializar tipos de payload
WhatsApp que antes caían en el path "sin contenido" y se descartaban.

- **Location messages (v0.36.0)** — cuando el cliente comparte una
  ubicación, qrsgen renderiza un body multilínea con header
  `📍 Ubicación compartida` (o `Ubicación en vivo` si `IsLive`), nombre
  del POI en bold si WA lo provee, dirección, link a Google Maps con
  `%.6f` de precisión y comentario del sender en italic. Coordenadas
  `0,0` se descartan como inválidas. Live locations llegan como
  múltiples snapshots independientes — qrsgen no agrega.
- **Polls (v0.37.0)** — `PollCreationMessage` (v1) y
  `PollCreationMessageV3` se renderizan como `🗳️ **Encuesta:** <pregunta>`
  + lista numerada de opciones + hint `_(elige 1 opción)_` /
  `_(elige hasta N opciones)_` según `SelectableOptionsCount`. Sin
  hint para `max=0` (unlimited). Los votos posteriores
  (`PollUpdateMessage`) NO se propagan: Chatwoot no tiene widget
  nativo de polls que pueda reflejarlos correctamente.
- **Media polish (v0.38.0)** — sin transcodificar contenido, mejora la
  compatibilidad con reproductores HTML5: voice notes (PTT=true) usan
  filename `voice-note.ogg` en lugar de `audio.opus`, el mime se sanea
  con `sanitizeMime` (`audio/ogg; codecs=opus` → `audio/ogg`), y los
  stickers con mime vacío reciben default `image/webp`. **No** incluye
  conversión WebP→PNG ni Opus→AAC (requiere ffmpeg en container, fuera
  de scope).

Las tres comparten propiedad: **no hay env var nueva**. Aplican siempre
que el payload llegue por el WebSocket. Detalles completos en
[Soporte de contenido de mensajes](../integrations/message-content.md).

## Observabilidad de features real-time

Desde **v0.35.0**, las cuatro features real-time (avatar sync,
reacciones, typing, read receipts) emiten al counter Prometheus
unificado `qrsgen_realtime_events_total{feature,result,instance}`.
Permite calcular en Grafana tasas de éxito (`ok`), cobertura
(`ok` vs `wa_miss`), efectividad del throttle (`throttled / total`)
y errores downstream (`ds_error`) por feature sin parsear logs.
Cardinalidad ~32–320 series para despliegues típicos. Catálogo
completo, queries y alerting sugerido en
[Observabilidad — `qrsgen_realtime_events_total`](../api/observability.md#qrsgen_realtime_events_total-v0350).

## HMAC opcional del webhook

`WEBHOOK_HMAC_SECRET` activa firma HMAC-SHA256 obligatoria en el
endpoint `/webhook`. Previene inyecciones desde dentro del overlay
LAN.

## Read-only rootfs

El container corre con filesystem read-only + tmpfs en `/tmp`. Imagen
distroless sin shell ni package manager. Un atacante con RCE no puede
instalar herramientas, persistir implantes ni escalar a root.

## Backups Postgres automatizados

Systemd timer diario con retención 7 días / 4 semanas. Runbook de
restore incluido. Off-site backup configurable con un `ExecStartPost=`.

## 12 lifecycle events

Conexión, desconexión, ban risk, outbox expirations... cada uno se
emite como webhook HTTP a la URL que configures por instancia. Catálogo
completo en [Lifecycle webhooks](../api/lifecycle-webhooks.md).

## Glosario

**Outbox**: cola persistida en Postgres donde van los mensajes outgoing
cuando la instancia está temporalmente desconectada. Se reentregan al
volver, con TTL de 5 minutos.

**TTL** (Time To Live): tiempo máximo que un mensaje puede esperar en
la outbox antes de expirar.

**BanWatcher**: módulo interno que analiza el ritmo de envíos para
detectar patrones que WhatsApp suele penalizar.

**Velocity**: mensajes outgoing por unidad de tiempo. Una de las tres
señales del BanWatcher.

**Diversity**: número de JIDs únicos contactados por ventana de
tiempo. Otra señal del BanWatcher.

**Delivery ratio**: fracción de envíos exitosos sobre intentos
totales. Tercera señal del BanWatcher.

**Audit log**: tabla `bridge_audit_log` con triggers DB que rechazan
UPDATE/DELETE — registro inmutable de operaciones.

**Tamper-evident**: propiedad donde cualquier modificación al log es
detectable o imposible. qrsgen lo garantiza a nivel DB.

**Usage tracking**: contadores diarios de mensajes y eventos por
instancia, persistidos en Postgres para reporting/facturación.

**owner_tag**: string libre para mapear instancias a tenants
(clientes). qrsgen lo expone en agregados de billing pero no lo
interpreta.

**Multi-tenant ligero**: arquitectura donde un solo proceso sirve a
varios clientes identificándolos solo por etiqueta.

**HMAC** (Hash-based Message Authentication Code): firma criptográfica
que demuestra que un mensaje viene de quien dice y no ha sido
modificado.

**Distroless**: imagen Docker mínima sin shell ni package manager.
Reduce la superficie de ataque ante RCE.

**Read-only rootfs**: filesystem del container marcado como solo
lectura. Cualquier intento de escribir falla — buena señal de
compromiso si ocurre.

**Lifecycle event**: notificación HTTP que qrsgen POSTea cuando ocurre
algo relevante en una instancia (conexión, QR, ban risk, etc.).

**Avatar sync**: descarga de la foto de perfil de WhatsApp y subida al
downstream como avatar del contacto. Read-only sobre WhatsApp:
qrsgen nunca escribe en el perfil del usuario.

**Letter-avatar**: avatar por defecto que algunos downstreams (ej.
Chatwoot) generan con las iniciales del nombre cuando no hay imagen
configurada. El avatar sync los reemplaza por las fotos reales de WA.

**Prefijo de grupo**: línea que qrsgen antepone al body de cada
mensaje de grupo identificando al sender (nombre + opcionalmente
teléfono). Permite al agente que lee la conversación saber quién
escribió cada mensaje sin abrir el subhilo del grupo.

**Contacto saved (libreta)**: JID que el dueño del número conectado
tiene en su libreta del móvil, con `FullName` o `FirstName` propagado
hasta el contact store de whatsmeow. Determina si qrsgen omite el
bloque de teléfono en el prefijo de grupo.

**PushName**: nombre que el propio sender configura en su WhatsApp.
qrsgen lo usa como fallback de display pero NO lo cuenta como
"guardado" — viene del sender, no de la decisión del bot owner.

**Reacción (WhatsApp)**: emoji que un usuario añade a un mensaje
existente mediante long-press → tap en el emoji. WhatsApp lo entrega
como un `ReactionMessage` que apunta al `msg.Info.ID` del mensaje
target. qrsgen lo sincroniza al downstream como un nuevo mensaje
incoming desde v0.33.0.

**`ReactionMessage`**: tipo de payload en `events.Message` cuando el
sender reaccionó en vez de enviar texto/media. Tiene `Text` (emoji o
`""` si retiró la reacción) y referencia al mensaje target.

**Reacciones sync**: propagación de reacciones WhatsApp al downstream.
Read-only sobre WhatsApp (qrsgen no envía reacciones de vuelta).
Controlada por `QRSGEN_REACTIONS_SYNC`.

**Typing indicator (sync)**: propagación de los eventos
`*events.ChatPresence` (`composing`/`paused`) de WhatsApp al downstream
vía `POST /toggle_typing_status`. Permite al agente ver "está
escribiendo..." mientras el cliente WA tipea. Throttled in-memory con
`minInterval=4s` por conversación. Controlado por `QRSGEN_TYPING_SYNC`.

**Read receipt (sync)**: propagación de los `*events.Receipt` con
`Type in ("read","read-self")` de WhatsApp al downstream vía
`POST /update_last_seen`. Actualiza `contact_last_seen_at` con el
timestamp del receipt, marcando los mensajes outgoing previos del
agente como leídos en la UI. Controlado por
`QRSGEN_READ_RECEIPTS_SYNC`.

**`*events.ChatPresence`**: evento de whatsmeow que indica si un
cliente está escribiendo (`composing`) o ha dejado de escribir
(`paused`) en una conversación concreta.

**`*events.Receipt`**: evento de whatsmeow que llega cuando Meta
confirma un cambio de estado de un mensaje (entregado, leído,
reproducido). qrsgen filtra por `Type` y solo propaga `read` y
`read-self`.

**`typingTracker`**: estructura in-memory per-conversación que
deduplica los POSTs `toggle_typing_status` al downstream. Cambios de
estado siempre emiten; mismo estado dentro de `minInterval` (default
4s) NO emite.

**Mark-as-read outgoing (v0.39.0)**: propagación de la lectura del
agente en Chatwoot hacia WhatsApp via `client.MarkRead()` de whatsmeow.
Es el inverso de read receipts incoming (v0.34.1) y cierra el ciclo
bidireccional del doble check azul. Controlado por
`QRSGEN_MARK_AS_READ_OUTGOING`. Requiere que el webhook de Chatwoot
suscriba el evento `conversation_updated` además de `message_created`.

**`waidTracker`**: estructura in-memory per-conversación introducida en
v0.39.0 que registra los WAIDs de mensajes incoming junto con su
timestamp, para drenarlos cuando el agente lea en el downstream.
Compartida entre `bridge.Incoming` (registra) y `bridge.Outgoing`
(drena). Cap de 50 entradas/conv (FIFO) y reset on restart.

**`ReadMarker`**: interfaz minimal de qrsgen (v0.39.0) con un solo
método `MarkRead(ctx, chat, sender, messageIDs, ts)`. Desacopla
`bridge.Outgoing` del cliente WA concreto e implementada por
`wameow.Conn.MarkRead`.

**`conversation_updated`**: webhook que Chatwoot emite cuando algo
cambia en una conversación (lectura, asignación, etiquetas). qrsgen
suscribe este evento desde v0.39.0 para detectar el
`agent_last_seen_at` y disparar `MarkRead` outgoing.

**`LocationMessage`**: payload de WhatsApp cuando el cliente comparte
una ubicación. Trae `DegreesLatitude`/`DegreesLongitude` y
opcionalmente `Name` (POI), `Address`, `Comment`, `IsLive`. Desde
v0.36.0 qrsgen lo serializa con `formatLocationContent` a un body
multilínea con link a Google Maps.

**Live location**: ubicación compartida de forma continua durante un
periodo (15min/1h/8h). WhatsApp envía múltiples `LocationMessage` con
`IsLive=true` y coords actualizadas. qrsgen NO agrega: cada snapshot
llega como mensaje incoming independiente al downstream.

**`PollCreationMessage` / `PollCreationMessageV3`**: dos shapes del
payload de encuesta (v1 legacy, v3 moderno). Mismos campos relevantes:
`Name` (pregunta), `Options[]`, `SelectableOptionsCount`. Desde v0.37.0
qrsgen renderiza ambos con `formatPollContent` como lista numerada.

**`PollUpdateMessage`**: payload de voto en una encuesta. NO se propaga
al downstream: Chatwoot no tiene UI de polls nativa que pueda reflejar
los votos aggregados sin contaminar el thread.

**`sanitizeMime`**: helper (v0.38.0) que quita el parámetro de codec
del `Content-Type` (`audio/ogg; codecs=opus` → `audio/ogg`). Maximiza
compatibilidad con reproductores HTML5.

**Voice note (PTT)**: grabación de audio del botón micrófono de WA
(push-to-talk). Detectado vía `am.GetPTT()`. Desde v0.38.0 se sube con
filename `voice-note.ogg` para que los browsers disparen el decoder
Opus automáticamente.
