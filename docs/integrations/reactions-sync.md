# Sincronización de reacciones WhatsApp

A partir de **v0.33.0**, las reacciones que un usuario añade a un
mensaje en WhatsApp (long-press → emoji) se propagan al downstream
como un nuevo mensaje incoming. Antes de esta versión, los eventos
`ReactionMessage` caían en el path "sin texto ni media" y se
descartaban silenciosamente.

> **Read-only sobre WhatsApp**: qrsgen solo escucha las reacciones que
> llegan por el WebSocket. No envía reacciones de vuelta a WhatsApp
> (eso requiere un flujo outgoing dedicado — candidato a v0.34.x).

## TL;DR

| Caso | Formato del body en downstream |
|---|---|
| Contacto guardado en libreta | `**~Jean Paul** reaccionó con 👍` |
| Sender no guardado en grupo | `` **~Richard** `+34604021705` reaccionó con ❤️ `` |
| Reacción retirada (text="") | `**~Jean Paul** _quitó su reacción_` |

El mensaje se POSTea con `message_type: "incoming"` y
`source_id: "WAID:reaction:<msg.Info.ID>"` para no colisionar con el
mensaje original en la deduplicación.

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
   incluyendo `IsContactSaved` introducido en v0.32.0. Contactos
   guardados muestran solo nombre; no guardados en grupos muestran
   nombre + code block con teléfono.
3. Construye el body según el formato de la tabla TL;DR.
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
qrsgen lo renderiza como `**~<name>** _quitó su reacción_`.

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
