# Presencia (typing) y read receipts

A partir de **v0.34.0** y **v0.34.1**, qrsgen propaga al downstream dos
señales de interacción en tiempo real que antes se descartaban:

- **v0.34.0** — eventos de presencia de chat (`composing` / `paused`):
  cuando el cliente WhatsApp está escribiendo, el agente ve el indicador
  "está escribiendo..." en el panel del downstream.
- **v0.34.1** — read receipts: cuando el cliente abre el chat y lee los
  mensajes del agente, qrsgen actualiza `contact_last_seen_at` de la
  conversación y la UI marca los mensajes del agente como leídos
  (equivalente al doble tick azul).

> **Read-only sobre WhatsApp**: qrsgen solo lee `ChatPresence` y
> `Receipt` del WebSocket. No envía `MarkRead` ni presencia de vuelta a
> WhatsApp — la dirección de la sincronización es siempre WA →
> downstream.

## TL;DR

| Feature | Evento WA | API downstream | Env var | Versión |
|---|---|---|---|---|
| **Typing indicator** | `*events.ChatPresence` (`composing` / `paused`) | `POST .../conversations/Y/toggle_typing_status` body `{"typing_status":"on"\|"off"}` | `QRSGEN_TYPING_SYNC` (default `true`) | v0.34.0 |
| **Read receipt** | `*events.Receipt` (`kind in ("read","read-self")`) | `POST .../conversations/Y/update_last_seen` body `{agent_last_seen_at, contact_last_seen_at}` | `QRSGEN_READ_RECEIPTS_SYNC` (default `true`) | v0.34.1 |

Ambas son opt-out y fire-and-forget: ningún fallo bloquea el flujo del
mensaje. Comparten arquitectura: callback en `wameow.Conn`, propagación
vía `manager.Set*Handler`, dispatch a `bridge.Incoming.HandleChatPresence`
/ `HandleReceipt`, y POST platform-agnostic vía `downstream.Client`.

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
- **Sin `MarkRead` back-to-WhatsApp**. Si el agente lee un mensaje en
  Chatwoot, qrsgen **no** envía `MarkRead` a WhatsApp. El cliente
  remoto seguirá viendo el doble tick gris hasta que el bot owner abra
  el chat en su móvil. Candidato a versiones futuras.
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

**Read-only sobre WhatsApp**: convención de qrsgen donde nunca
escribe estado a WhatsApp (ni `MarkRead`, ni presencia, ni edición de
perfil). Solo lee. La dirección de propagación es siempre WA →
downstream.

**Privacy settings (WhatsApp)**: ajustes del cliente remoto que pueden
ocultar receipts. Si "Confirmaciones de lectura" está desactivado, Meta
no emite `read` para sus mensajes. La cobertura de read-receipts-sync
es parcial por esta razón.
