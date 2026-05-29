# Sincronización de reacciones WhatsApp

A partir de **v0.33.0**, las reacciones que un usuario añade a un
mensaje en WhatsApp (long-press → emoji) se propagan al downstream
como un nuevo mensaje incoming. Antes de esta versión, los eventos
`ReactionMessage` caían en el path "sin texto ni media" y se
descartaban silenciosamente.

Desde **v0.39.7** el formato del body se realinea con el del prefijo
de grupo (v0.39.6): header completo envuelto en un **inline code
block** (backticks), **teléfono primero** en E.164, separador
**middle dot `·` (U+00B7)** con espacios a ambos lados, y nombre al
final con tilde `~` **solo si el contacto no está guardado** en la
libreta del bot owner (lógica `IsContactSaved` introducida en
v0.39.5). El mismo formato aplica en chats 1:1 y en grupos.

> **Read-only sobre WhatsApp**: qrsgen solo escucha las reacciones que
> llegan por el WebSocket. No envía reacciones de vuelta a WhatsApp
> (eso requiere un flujo outgoing dedicado — candidato a v0.34.x).

## TL;DR

| Caso | Formato del body en downstream |
|---|---|
| Contacto saved (en agenda) | `` `+34604021705 · Jean Paul reaccionó con 👍` `` |
| Contacto no saved (PushName) | `` `+34663504782 · ~Marcelo Lopez reaccionó con ❤️` `` |
| Reacción retirada (text="") | `` `+34604021705 · Jean Paul quitó su reacción` `` |

El header (`+phone · <~?>name`) y el sufijo (`reaccionó con <emoji>`
o `quitó su reacción`) van **dentro del mismo par de backticks**: el
inline code block envuelve la línea entera. Eso difiere del prefijo
de grupo de v0.39.6 — donde el code block envuelve solo el header y
el body del mensaje viene fuera —, porque la reacción no tiene body
propio: el "contenido" es el sufijo descriptivo.

El mensaje se POSTea con `message_type: "incoming"` y
`source_id: "WAID:reaction:<msg.Info.ID>"` para no colisionar con el
mensaje original en la deduplicación.

> **Pérdida del italic en "quitó su reacción"**: hasta v0.39.6 el
> sufijo de retracted iba en italic con markdown (`_quitó su
> reacción_`). En v0.39.7, al envolver toda la línea en un inline
> code block, Chatwoot **no procesa markdown dentro de inline code**
> y los underscores quedarían literales. El sufijo pasa a ser texto
> plano dentro del code block — la diferenciación visual con el
> resto de reacciones (que terminan en `reaccionó con <emoji>`) se
> mantiene por el cambio léxico, no por el estilo tipográfico.

## Configuración

| Env var | Default | Descripción |
|---|---|---|
| `QRSGEN_REACTIONS_SYNC` | `true` | Master switch. Si `false`, todas las reacciones se ignoran silenciosamente (comportamiento pre-v0.33.0). |

Opt-out: el comportamiento por defecto sincroniza reacciones.

## Cómo se dispatcha

En `bridge.Incoming.Handle`, antes del path estándar de texto/media,
se chequea si el mensaje es una reacción:

```go
if msg.Message.GetReactionMessage() != nil {
    return i.handleReaction(ctx, instance, msg)
}
```

`handleReaction`:

1. Resuelve sender + conversación por el mismo camino que un mensaje
   normal (LID → PN si aplica, lookup del contact en downstream).
2. Aplica el mismo name resolver que `applyGroupSenderPrefix` —
   incluyendo `IsContactSaved` (v0.32.0). Desde v0.39.7 el bit del
   tilde se calcula con la misma regla que en el prefijo de grupo
   v0.39.5: `~` solo si `IsContactSaved(jid) == false`.
3. Construye el body con el formato unificado **v0.39.7**:
   `` `+<E164> · <tilde><name> reaccionó con <emoji>` `` (header y
   sufijo dentro del mismo inline code block). Si `Text == ""` el
   sufijo es ` quitó su reacción` (sin italic — los underscores
   markdown no se procesan dentro de inline code).
4. POST al downstream como `message_type: "incoming"` con
   `source_id: "WAID:reaction:<msg.Info.ID>"`.

## Por qué `incoming` y no `activity`

Chatwoot expone `message_type: "activity"` para eventos de sistema
(conversation resolved, agent assigned, etc.), pero su API channel
inbound (`/conversations/.../messages`) en la práctica solo acepta
`incoming` y `outgoing` desde fuentes externas. Intentar postear con
`activity` resulta en mensajes que no aparecen en el panel o errores
de validación según versión.

Tratar la reacción como un mensaje `incoming` más es lo más simple,
funciona en todas las versiones de Chatwoot soportadas, y mantiene el
principio platform-agnostic: el mismo path
`downstream.Router.PostMessage` que cualquier otro mensaje.

## Por qué `source_id: "WAID:reaction:..."` importa

Los mensajes normales se POSTean con `source_id: "WAID:<msg.Info.ID>"`.
Si una reacción reutilizara ese mismo `source_id`, el dedup del
downstream (que indexa por `source_id`) la consideraría duplicado del
mensaje original y la descartaría.

Prefijar con `WAID:reaction:` mantiene el namespace `WAID:` para
trazabilidad pero garantiza que el `source_id` es único respecto al
mensaje target. Múltiples reacciones al mismo mensaje (raras, pero
posibles si el usuario cambia el emoji) también son únicas entre sí
porque cada reacción tiene su propio `msg.Info.ID`.

## Modos de fallo

| Situación | Resultado |
|---|---|
| `QRSGEN_REACTIONS_SYNC=false` | Reacción descartada silenciosamente. No se loguea por mensaje (sería ruido). |
| Reacción de `IsFromMe=true` | Ignorada. No queremos eco de reacciones que el propio bot owner añade desde su móvil. |
| Contacto del sender no existe en downstream | Reacción descartada. No creamos contactos para reacciones aisladas — esperamos a un mensaje "real" para el `CreateContact`. |
| Conversación no abierta para ese contacto | Reacción descartada por el mismo motivo (no abrimos conversaciones por reacciones huérfanas). |
| POST al downstream falla (5xx) | Log `warn`. No hay retry — la reacción se pierde. Caso raro; el mensaje target debería haber abierto la ruta minutos antes. |

## Verificar que funciona

Cuando un usuario reacciona a un mensaje, los logs deberían mostrar:

```bash
docker logs qrsgen 2>&1 | grep "reaction synced"
# → time=... msg="reaction synced" instance=... sender=...@s.whatsapp.net
#   target_msg_id=3EB0... emoji=👍 contact_id=42 source_id=WAID:reaction:ABC123
```

En el panel del downstream (Chatwoot), debería aparecer un mensaje
incoming nuevo en la conversación con el texto del formato adaptativo
(ver TL;DR).

Si no aparece nada:

1. ¿`QRSGEN_REACTIONS_SYNC=false`? Master switch off.
2. ¿El contacto existe en downstream? Una reacción al primer mensaje
   de un sender nuevo puede llegar antes de que el `CreateContact`
   complete — la reacción se pierde, los siguientes mensajes ya
   funcionan normal.
3. ¿Reacción del propio número conectado? `IsFromMe=true` → ignorada
   por diseño.

## Caveats

- **Sin reacciones outgoing**. qrsgen no envía reacciones a WhatsApp.
  Si un agente reacciona a un mensaje en el downstream (cuando el
  downstream soporte reacciones, ej. Chatwoot 3.x), el emoji no se
  propaga de vuelta. Posible v0.34.x.
- **Sin asociación visual con el mensaje target**. El `target_msg_id`
  se loguea pero no se incluye en el payload visible. Chatwoot no
  tiene API estándar para "este mensaje es una reacción a aquel otro"
  vía `/messages`. El agente infiere por proximidad temporal: la
  reacción llega segundos después del mensaje al que reacciona.
- **Aumenta el volumen de mensajes**. Una conversación activa puede
  generar 2-5x más mensajes en downstream si el sender es de los que
  reaccionan a todo. Considera el impacto en facturación si tu
  downstream cobra por mensaje (Chatwoot self-hosted no, SaaS sí).
- **Reacciones a media**. Si el sender reacciona a un mensaje que era
  una imagen/audio, el formato es el mismo (`reaccionó con <emoji>`)
  — no se cita el contenido del media original.
- **El emoji llega como string Unicode**. Para emojis compuestos
  (ZWJ sequences) el rendering depende del downstream y de la fuente
  del navegador del agente. qrsgen los pasa tal cual sin
  normalización.
- **Sin persistencia**. La dispatch es in-memory desde el event
  handler. Si qrsgen está down cuando llega la reacción, se pierde
  (no hay outbox para inbound). Coherente con el resto del incoming.

## Histórico de versiones

| Versión | Cambio |
|---|---|
| **v0.33.0** | Introducción del feature. Body con formato markdown `**<name>** reaccionó con <emoji>` (siempre con tilde, nombre primero, sin teléfono en 1:1). En grupos con sender no saved, teléfono en code block detrás del nombre: `` **~Name** `+E164` reaccionó con <emoji> ``. Reacción retirada en italic: `**~Name** _quitó su reacción_`. |
| v0.34.0 | Sin cambios en el formato. |
| v0.39.4 | `applyGroupSenderPrefix` se unifica con teléfono siempre y nombre en code block, pero `handleReaction` mantiene su formato propio. |
| v0.39.5 | `applyGroupSenderPrefix` introduce la lógica `IsContactSaved` para condicionar el tilde. `handleReaction` no se toca. |
| v0.39.6 | El prefijo de grupo pasa a `` `+<E164> · <tilde><name>` `` (phone-first, middle dot, sin bold ni tabs). `handleReaction` sigue con el formato de v0.33.0 — desalineado. |
| **v0.39.7** | `handleReaction` se realinea con el prefijo de grupo v0.39.6. Toda la línea (header + sufijo) va dentro de un único inline code block: `` `+<E164> · <tilde><name> reaccionó con <emoji>` `` y `` `+<E164> · <tilde><name> quitó su reacción` ``. Cambios concretos respecto a v0.33.0..v0.39.6: (1) **teléfono siempre presente** (antes solo en grupos con sender no saved), (2) **tilde solo si no saved** (antes siempre presente), (3) **wrap en inline code block** en lugar de `**bold**`, (4) **phone-first**, nombre detrás, (5) **`quitó su reacción` pierde el italic** porque markdown no se procesa dentro de inline code; se queda como texto literal del code block. |

## Glosario

**Reacción (WhatsApp)**: emoji que un usuario añade a un mensaje
existente mediante long-press → tap en el emoji. WhatsApp lo entrega
al backend como un `ReactionMessage` con referencia al `msg.Info.ID`
del mensaje target.

**`ReactionMessage`**: tipo de payload en `events.Message` cuando el
usuario reaccionó (en vez de enviar texto/media). Contiene `Text`
(el emoji o `""` si retiró la reacción), `Key` (target msg ID), y
`SenderTimestampMS`.

**Reacción retirada**: cuando el usuario quita la reacción que había
puesto, WhatsApp envía un nuevo `ReactionMessage` con `Text: ""`.
Desde v0.39.7 qrsgen lo renderiza como
`` `+<E164> · <tilde><name> quitó su reacción` `` (sufijo dentro del
mismo code block que el header, sin italic — los `_..._` markdown no
se procesan dentro de inline code en Chatwoot).

**`source_id` namespace**: prefijo `WAID:` que qrsgen usa para
identificar todos los mensajes que vienen de WhatsApp en el dedup del
downstream. Para reacciones, el namespace es `WAID:reaction:` para no
colisionar con el mensaje original.

**Target msg ID**: el `msg.Info.ID` del mensaje al que se reaccionó.
qrsgen lo loguea (para trazabilidad/forensics) pero no lo incluye en
el payload visible — el downstream no tiene UI nativa para enlazar
reacción ↔ target.

**Master switch**: env var binaria que activa o desactiva
completamente una feature. `QRSGEN_REACTIONS_SYNC` es el master switch
de esta feature; `QRSGEN_AVATAR_SYNC` el de la sincronización de
avatares.
