# Presencia (typing) y read receipts

A partir de **v0.34.0**, **v0.34.1** y **v0.39.0**, qrsgen propaga
señales de interacción en tiempo real que antes se descartaban — ahora
en ambas direcciones:

- **v0.34.0** — eventos de presencia de chat (`composing` / `paused`)
  WA → downstream: cuando el cliente WhatsApp está escribiendo, el
  agente ve el indicador "está escribiendo..." en el panel del
  downstream.
- **v0.34.1** — read receipts WA → downstream: cuando el cliente abre
  el chat y lee los mensajes del agente, qrsgen actualiza
  `contact_last_seen_at` de la conversación y la UI marca los mensajes
  del agente como leídos (equivalente al doble tick azul).
- **v0.39.0** — mark-as-read downstream → WA: cuando el agente abre
  la conversación en Chatwoot y marca los mensajes como leídos, qrsgen
  envía `MarkRead` a WhatsApp para que el cliente remoto vea el doble
  check azul sobre sus mensajes incoming. Cierra el ciclo bidireccional.

> **WhatsApp ya no es read-only para receipts**: desde v0.39.0 qrsgen
> escribe `MarkRead` de vuelta a WA (única excepción). Sigue sin enviar
> presencia (typing) ni edición de perfil — el resto del estado WA
> permanece read-only.

## TL;DR

| Feature | Dirección | Evento de origen | Acción | Env var | Versión |
|---|---|---|---|---|---|
| **Typing indicator** | WA → downstream | `*events.ChatPresence` (`composing` / `paused`) | `POST .../conversations/Y/toggle_typing_status` body `{"typing_status":"on"\|"off"}` | `QRSGEN_TYPING_SYNC` (default `true`) | v0.34.0 |
| **Read receipt (incoming)** | WA → downstream | `*events.Receipt` (`kind in ("read","read-self")`) | `POST .../conversations/Y/update_last_seen` body `{agent_last_seen_at, contact_last_seen_at}` | `QRSGEN_READ_RECEIPTS_SYNC` (default `true`) | v0.34.1 |
| **Mark-as-read (outgoing)** | downstream → WA | Webhook `conversation_updated` con `agent_last_seen_at` | `wameow.Conn.MarkRead(chat, sender, messageIDs, ts)` sobre `client.MarkRead()` de whatsmeow | `QRSGEN_MARK_AS_READ_OUTGOING` (default `true`) | v0.39.0 |

Las tres son opt-out y fire-and-forget: ningún fallo bloquea el flujo
principal. Comparten arquitectura platform-agnostic: callbacks en
`wameow.Conn`, propagación vía `manager.Set*Handler`, dispatch a
`bridge.Incoming` / `bridge.Outgoing`, y POSTs vía `downstream.Client`
para los dos primeros / `MarkRead` de whatsmeow para el tercero.

## Typing indicators (v0.34.0)

### Configuración

| Env var | Default | Descripción |
|---|---|---|
| `QRSGEN_TYPING_SYNC` | `true` | Master switch. Si `false`, los eventos `ChatPresence` se descartan silenciosamente. |

### Cómo se dispatcha

1. whatsmeow emite `*events.ChatPresence` cuando el cliente remoto
   empieza o deja de escribir.
2. `wameow.Conn` ejecuta el callback registrado vía
   `SetChatPresenceHandler`. `manager.SetChatPresenceHandler` lo propaga
   a todas las `Conn` actuales y a las que se creen en `startLocked`
   (mismo patrón que `SetPictureHandler` en avatar sync).
3. El dispatcher entra en el case nuevo y llama a
   `bridge.Incoming.HandleChatPresence(ctx, instance, jid, state)`.
4. `HandleChatPresence`:
   - Busca contacto + conversación con `FindContact` / `FindConversation`.
     **No crea nada** — si no existen, descarta el evento.
   - Consulta el `typingTracker` para decidir si emite o silencia.
   - Si emite: llama `Client.SetTypingStatus(convID, typing bool)` que
     hace `POST /api/v1/accounts/X/conversations/Y/toggle_typing_status`
     con body `{"typing_status":"on"}` o `{"typing_status":"off"}`.

### Throttle (`typingTracker`)

El tracker (`internal/bridge/typing_tracker.go`) es in-memory,
per-conversación, y deduplica llamadas redundantes al downstream:

- **Cambio de estado** (typing → not typing, o viceversa): **siempre
  emite**. No queremos perder transiciones reales.
- **Mismo estado dentro de `minInterval`** (default **4s**): NO emite.
  El cliente WhatsApp puede repetir `composing` cada vez que el usuario
  pulsa una tecla — sin throttle inundaríamos el downstream con POSTs
  idénticos.
- **Mismo estado fuera de `minInterval`**: emite (recordatorio al
  downstream de que la sesión sigue "viva").

5 tests cubren las combinaciones (estado distinto, estado igual dentro
del intervalo, estado igual fuera del intervalo, primera llamada, reset).

El estado del tracker es per-conversación, in-memory, y se pierde en
restart. Worst case tras restart: una llamada HTTP extra al downstream
en el primer evento de cada conversación activa.

### Formato en el downstream

Chatwoot (y compatibles) muestra "está escribiendo..." debajo de la
caja de input cuando recibe `typing_status: "on"`. Al recibir `"off"`
(o tras un timeout interno del downstream) el indicador desaparece.
qrsgen propaga ambos estados explícitamente — no confía en el timeout
implícito del downstream.

### Modos de fallo

| Situación | Resultado |
|---|---|
| `QRSGEN_TYPING_SYNC=false` | Evento descartado silenciosamente. No se loguea por evento (sería ruido). |
| Contacto no existe en downstream | Evento descartado. No creamos contactos por una notificación de typing. El primer mensaje "real" abrirá la ruta. |
| Conversación no abierta para el contacto | Igual: descartado. |
| Mismo estado dentro de `minInterval` | Throttled. No hay POST. |
| `SetTypingStatus` falla (4xx/5xx) | Log `warn`. No retry — el siguiente evento intentará de nuevo. |

## Read receipts (v0.34.1)

### Configuración

| Env var | Default | Descripción |
|---|---|---|
| `QRSGEN_READ_RECEIPTS_SYNC` | `true` | Master switch. Si `false`, los `*events.Receipt` se ignoran sin POST al downstream. |

### Qué se propaga

`bridge.Incoming.HandleReceipt` filtra por `receipt.Type`:

| Tipo de receipt | ¿Propagado? | Motivo |
|---|---|---|
| `read` | **Sí** | El cliente abrió el chat y leyó los mensajes del agente. Es la señal accionable. |
| `read-self` | **Sí** | Lectura desde otro device Multi-Device del propio usuario. Misma semántica para el agente. |
| `delivered` | No | El servidor de Meta confirmó entrega, pero el cliente no necesariamente abrió el chat. Poco actionable. |
| `played` | No | Audio reproducido. Específico a media; el downstream no tiene UI estándar para "audio escuchado". |
| `sender` | No | Confirmación de envío. Redundante para el flujo incoming. |

Tipos no listados se ignoran por construcción (`default` del switch).

### Cómo se propaga

1. whatsmeow emite `*events.Receipt`. Dispatcher entra en el case nuevo
   y llama a `bridge.Incoming.HandleReceipt`.
2. Filtra por `Type in ("read", "read-self")`. Resto: return early.
3. Resuelve contacto + conversación. Si no existen, descarta.
4. Llama `Client.UpdateContactLastSeen(convID, ts)`:

   ```
   POST /api/v1/accounts/X/conversations/Y/update_last_seen
   {
     "agent_last_seen_at": <unix ts>,
     "contact_last_seen_at": <unix ts>
   }
   ```

   Ambos campos llevan el mismo timestamp: el `receipt.Timestamp` (Unix
   epoch en segundos). Chatwoot espera ambos para considerar la
   conversación "vista por el contacto" en su modelo interno.

### Correlación de timestamps

El timestamp del POST viene de `receipt.Timestamp` — el momento en que
**WhatsApp registró el receipt**, no el momento en que qrsgen recibe el
evento. Si hay lag de WebSocket (segundos), el `contact_last_seen_at`
sigue siendo precisa respecto a cuándo el contacto realmente leyó el
mensaje.

### Modos de fallo

| Situación | Resultado |
|---|---|
| `QRSGEN_READ_RECEIPTS_SYNC=false` | Evento descartado silenciosamente. |
| Tipo distinto de `read`/`read-self` | Ignorado por filtro. |
| Contacto/conversación no existen en downstream | Descartado. No creamos nada por un receipt aislado. |
| `UpdateContactLastSeen` falla (4xx/5xx) | Log `warn`. No retry — el próximo receipt para esa conv corregirá el valor. |

## Mark-as-read outgoing (v0.39.0)

Cierra el ciclo bidireccional. Mientras v0.34.1 propaga las lecturas
del cliente WhatsApp hacia el downstream (`contact_last_seen_at`),
v0.39.0 hace el camino inverso: cuando el agente lee la conversación en
Chatwoot, qrsgen envía `MarkRead` a WhatsApp para que el cliente
remoto vea el doble check azul sobre los mensajes que envió.

### Configuración

| Env var | Default | Descripción |
|---|---|---|
| `QRSGEN_MARK_AS_READ_OUTGOING` | `true` | Master switch. Si `false`, los webhooks `conversation_updated` se ignoran sin llamar a `MarkRead`. |

### Configuración REQUERIDA en el downstream

Para que el feature funcione, en Chatwoot:

- El webhook ya configurado contra
  `POST /api/instances/{name}/webhook` de qrsgen es el mismo endpoint
  que para `message_created`. **No se añade URL nueva.**
- ADEMÁS del evento `message_created`, hay que activar el evento
  `conversation_updated` en la configuración del webhook de Chatwoot.

Sin esa suscripción el feature no hace nada — no rompe nada, pero
qrsgen nunca recibe la señal de cuándo el agente leyó. Cliente WA
seguirá viendo doble tick gris.

### Flujo

1. Cliente envía mensaje incoming → qrsgen lo postea al downstream
   (path normal de incoming).
2. Tras `PostMessage` exitoso, qrsgen registra el WAID del mensaje en
   un tracker in-memory (`waidTracker`) per-conversación, junto con su
   timestamp.
3. El agente abre la conversación en Chatwoot y la marca como leída.
   Chatwoot actualiza internamente `agent_last_seen_at`.
4. Chatwoot dispara el webhook `conversation_updated` contra qrsgen,
   incluyendo el nuevo `agent_last_seen_at`.
5. qrsgen drena del tracker todos los WAIDs cuyo timestamp es
   `≤ agent_last_seen_at`, los agrupa por `(chat, sender)` y llama
   `wameow.Conn.MarkRead(ctx, chat, sender, messageIDs, ts)` que envuelve
   `client.MarkRead()` de whatsmeow.
6. El cliente WA ve el doble check azul sobre todos sus mensajes
   incluidos en ese batch.

### Cómo se cablea internamente

- `wameow.Conn.MarkRead(ctx, chat, sender, messageIDs, ts)` es un
  wrapper fino sobre `client.MarkRead()`. Idempotente: una segunda
  llamada con los mismos IDs no produce efecto adicional.
- `bridge.waidTracker` (archivo nuevo) es un tracker in-memory
  per-conversación con cap por defecto de **50 entradas/conv** (FIFO al
  desbordar). Métodos: `RecordIncoming(convID, waid, ts)` y
  `DrainBefore(convID, cutoffTS)`.
- `bridge.ReadMarker` es una interfaz pequeña con un solo método,
  desacopla `bridge.Outgoing` de `wameow.Conn` y permite testear sin
  cliente WA real.
- `bridge.Outgoing.EnableMarkAsRead(waids *waidTracker, marker ReadMarker)`
  cablea los componentes en `main.go`.
- `bridge.Incoming.EnableMarkAsRead()` devuelve un `*waidTracker` recién
  construido — `Incoming` y `Outgoing` comparten el mismo tracker. El
  `sync()` de incoming llama `tracker.RecordIncoming` tras un
  `PostMessage` exitoso.
- `WebhookPayload.Conversation.AgentLastSeenAt` y `ContactLastSeenAt`
  son campos nuevos en el payload del webhook que qrsgen parsea.
- `senderAdapter.MarkRead` en `main.go` puentea hacia
  `wameow.Conn.MarkRead`.

### Tests

Cuatro tests cubren `waidTracker`:

- `RecordIncoming` + `DrainBefore` con cutoff exacto (incluye y excluye
  según `≤`).
- Cap de 50: el 51º entry expulsa el más viejo (FIFO).
- Aislamiento per-conversación: drenar `convA` no afecta `convB`.
- Conversación vacía: `DrainBefore` devuelve slice vacío sin error.

### Modos de fallo

| Situación | Resultado |
|---|---|
| `QRSGEN_MARK_AS_READ_OUTGOING=false` | Webhook `conversation_updated` ignorado silenciosamente. |
| Evento `conversation_updated` sin `agent_last_seen_at` (o `=0`) | Ignorado. Chatwoot emite `conversation_updated` para muchas cosas (cambio de etiqueta, asignación de agente, status); solo procesamos los que traen lectura real. |
| Tracker vacío para esa conv (drainada previamente o restart reciente) | `DrainBefore` devuelve `[]`. No se llama `MarkRead`. |
| `client.MarkRead()` falla (instance disconnected, etc.) | Log `warn`. No retry — la siguiente `conversation_updated` reintentará (si todavía hay WAIDs en el tracker). |
| Llamada duplicada con mismo cutoff | Idempotente. Los WAIDs ya drenados no se vuelven a marcar. |

### Verificar que funciona

Cuando el agente lee una conversación en Chatwoot:

```bash
docker logs qrsgen 2>&1 | grep "mark-as-read sent to WhatsApp"
# → time=... msg="mark-as-read sent to WhatsApp" instance=...
#   conv_id=42 chat=...@s.whatsapp.net count=3 cutoff_ts=1716889200
```

En el móvil del cliente WA, los mensajes incluidos en el batch deberían
mostrar doble check azul.

Si no aparece nada:

1. ¿`QRSGEN_MARK_AS_READ_OUTGOING=false`?
2. ¿Está activado el evento `conversation_updated` en el webhook de
   Chatwoot? Si solo está `message_created`, qrsgen nunca recibe la
   señal.
3. ¿Reciente restart de qrsgen? El tracker es in-memory — los WAIDs
   registrados antes del restart se pierden y no se pueden marcar
   retroactivamente (ver caveats).

## Caveats y edge cases

- **Grupos: typing per participante, un solo indicador**. Si tres
  miembros del grupo escriben a la vez, whatsmeow emite tres
  `ChatPresence` con JIDs distintos pero el mismo `Chat`. El downstream
  solo soporta un indicador por conversación, así que el agente leerá
  "alguien está escribiendo..." sin saber quién. Es una limitación del
  modelo del downstream, no de qrsgen.
- **Privacy settings ocultan receipts**. Si el sender desactivó las
  confirmaciones de lectura en su WhatsApp (Ajustes → Cuenta →
  Privacidad → Confirmaciones de lectura), Meta no envía
  `*events.Receipt` con `Type=read` para sus mensajes. qrsgen recibe la
  ausencia silenciosa — `contact_last_seen_at` se queda obsoleto. La
  cobertura es **parcial**: depende de la configuración del cliente
  remoto.
- **In-memory state, no persistencia**. El `typingTracker` vive en
  memoria. Restart del proceso = throttle resetea. Worst case: una
  llamada HTTP extra al downstream por conversación activa
  inmediatamente tras restart. Deliberado — no merece migración DB.
- **`MarkRead` back-to-WhatsApp en v0.39.0**. Desde v0.39.0 el agente
  leyendo en Chatwoot **sí** propaga `MarkRead` a WhatsApp (doble
  check azul al cliente remoto). Requiere suscribir el evento
  `conversation_updated` en el webhook del downstream — sin esa config
  el feature no opera. Ver sección
  [Mark-as-read outgoing (v0.39.0)](#mark-as-read-outgoing-v0390).
- **Tracker mark-as-read in-memory**. El `waidTracker` (v0.39.0) vive
  en memoria igual que el `typingTracker`. Restart de qrsgen pierde el
  historial: los mensajes incoming registrados antes del restart no se
  podrán marcar como leídos en WA aunque el agente los lea después. El
  efecto es cosmético — solo afecta al doble check azul en mensajes
  viejos. El cap de 50 WAIDs/conv también puede expulsar los más viejos
  en conversaciones muy activas, pero `MarkRead` solo importa para los
  recientes.
- **`conversation_updated` sin `agent_last_seen_at` se ignora**.
  Chatwoot emite ese webhook por muchos motivos (cambio de etiqueta,
  asignación de agente, status...). qrsgen solo procesa los que traen
  un `agent_last_seen_at > 0`, evitando reintentos espurios.
- **Idempotencia accidental del mark-as-read**. Si Chatwoot emite dos
  `conversation_updated` con el mismo `agent_last_seen_at`, el segundo
  no produce efecto: los WAIDs ya drenados no vuelven al tracker. No es
  un fallo — es una propiedad deseable.
- **Sin reintento exponencial**. Igual que en avatar sync y reactions
  sync: si el downstream devuelve 5xx, no hay backoff — el próximo
  evento intentará de nuevo. Para read receipts esto es benigno porque
  el siguiente `read` corrige el `last_seen_at`; para typing el evento
  se pierde sin más (el agente verá menos transiciones).
- **Throttle compartido entre transiciones rápidas**. Si el usuario
  empieza a escribir, para a los 2s, y vuelve a escribir, qrsgen emite
  cada cambio porque son **estados distintos**. Si escribe
  continuamente durante 10s, solo emite al inicio (composing) y al
  final (paused) — los `composing` repetidos del medio quedan
  throttled.

## Verificar que funciona

### Typing

Cuando un cliente WA empieza a escribir en una conversación abierta en
el downstream:

```bash
docker logs qrsgen 2>&1 | grep "typing sync"
# → time=... msg="typing sync" instance=... jid=...@s.whatsapp.net
#   conv_id=42 state=composing emitted=true
```

En Chatwoot, el indicador "está escribiendo..." debería aparecer
debajo del input de la conversación.

### Read receipts

Cuando el cliente abre el chat y lee los mensajes del agente:

```bash
docker logs qrsgen 2>&1 | grep "receipt sync"
# → time=... msg="receipt sync" instance=... jid=...@s.whatsapp.net
#   conv_id=42 kind=read ts=1716889200
```

En Chatwoot, los mensajes outgoing previos del agente deberían marcarse
como leídos (el icono cambia a doble check azul o equivalente según
tema).

Si no aparece nada:

1. ¿Master switch off? `QRSGEN_TYPING_SYNC=false` o
   `QRSGEN_READ_RECEIPTS_SYNC=false`.
2. ¿El contacto existe en downstream? Ambas features requieren contact
   + conv pre-existentes — no se crean por estas señales.
3. ¿Privacy settings del sender? (solo aplica a read receipts) — si
   tiene confirmaciones de lectura desactivadas, no llegan eventos.

## Glosario

**`*events.ChatPresence`**: evento de whatsmeow que indica si un
cliente está escribiendo (`composing`) o ha dejado de escribir
(`paused`) en una conversación concreta. Diferente de `*events.Presence`
(presencia general "online/offline" del JID).

**`*events.Receipt`**: evento de whatsmeow que llega cuando Meta
confirma un cambio de estado de un mensaje (entregado, leído,
reproducido). Su campo `Type` distingue los subtipos.

**`composing` / `paused`**: los dos estados que `ChatPresence` puede
reportar. `composing` mientras el usuario tipea; `paused` cuando
detiene la escritura sin enviar (o pasa un timeout interno de WhatsApp).

**`read` / `read-self`**: subtipos de `Receipt` que qrsgen propaga. El
primero es el cliente remoto leyendo los mensajes del agente; el
segundo es el propio bot owner leyendo desde otro Multi-Device. Ambos
indican que la conversación fue vista.

**`delivered` / `played` / `sender`**: subtipos de `Receipt` que qrsgen
**no** propaga. Son menos accionables para el agente (entrega ≠
lectura; audio reproducido no necesariamente leído; confirmación de
envío redundante).

**`typingTracker`**: estructura in-memory (per-conversación) que
recuerda el último estado typing reportado y cuándo. Implementa la
política "cambio de estado siempre emite; mismo estado dentro del
intervalo throttled".

**`minInterval`**: parámetro del `typingTracker` que define el throttle
para estados repetidos. Default 4s. Cubre el caso "el cliente WA
spammea `composing` cada keystroke".

**`SetTypingStatus`**: método de `downstream.Client` que hace
`POST /toggle_typing_status` con `typing_status: on|off`. Es la API
estándar de Chatwoot para mostrar el indicador "está escribiendo".

**`UpdateContactLastSeen`**: método de `downstream.Client` que hace
`POST /update_last_seen` con `agent_last_seen_at` y
`contact_last_seen_at`. Es la API estándar de Chatwoot para marcar la
conversación como vista por el contacto.

**`contact_last_seen_at`**: campo de la conversación en el modelo de
Chatwoot que registra cuándo el contacto vio la conversación por
última vez. La UI lo usa para renderizar el doble check azul en los
mensajes outgoing previos al timestamp.

**Throttle**: política que limita la frecuencia de POSTs al downstream
para evitar sobrecargarlo con eventos redundantes. En typing el
throttle es per-conversación con `minInterval=4s`.

**Read-only sobre WhatsApp**: convención de qrsgen donde casi nunca
escribe estado a WhatsApp (ni presencia, ni edición de perfil). Solo
lee. Única excepción desde v0.39.0: `MarkRead` se envía de vuelta a WA
cuando el agente lee en el downstream, para cerrar el ciclo del doble
check azul. Typing y edición de perfil siguen siendo read-only.

**`waidTracker`** (v0.39.0): estructura in-memory per-conversación que
guarda los WAIDs de mensajes incoming junto con su timestamp. Métodos:
`RecordIncoming(convID, waid, ts)` que el `sync()` de incoming llama
tras un `PostMessage` exitoso, y `DrainBefore(convID, cutoffTS)` que
`Outgoing` llama al recibir un `conversation_updated`. Cap por defecto
de 50 entradas/conv (FIFO al desbordar). Reset on restart.

**`ReadMarker`** (v0.39.0): interfaz minimal con un solo método
(`MarkRead(ctx, chat, sender, messageIDs, ts)`) que desacopla
`bridge.Outgoing` del cliente WA concreto. Implementada por
`wameow.Conn.MarkRead` en producción y por mocks en tests.

**`conversation_updated`**: webhook que Chatwoot emite cuando algo
cambia en una conversación (lectura, asignación, etiquetas, status).
Desde v0.39.0 qrsgen suscribe este evento para detectar el
`agent_last_seen_at` y disparar el `MarkRead` outgoing. Requiere
suscripción explícita en la configuración del webhook de Chatwoot.

**`agent_last_seen_at`**: campo de la conversación de Chatwoot que
registra cuándo el agente la vio por última vez. qrsgen lo lee del
webhook `conversation_updated` y lo usa como cutoff para drenar el
`waidTracker` y disparar `MarkRead` sobre los mensajes leídos.

**Privacy settings (WhatsApp)**: ajustes del cliente remoto que pueden
ocultar receipts. Si "Confirmaciones de lectura" está desactivado, Meta
no emite `read` para sus mensajes. La cobertura de read-receipts-sync
es parcial por esta razón.
