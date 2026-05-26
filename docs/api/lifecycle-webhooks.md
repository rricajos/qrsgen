# Lifecycle webhooks

Cuando una instancia tiene `events_webhook_url` configurado, qrsgen
POSTea cambios de estado a esa URL.

## Esquema común

```json
{
  "instance": "whatsapp-main",
  "event": "connected",
  "occurred_at": "2026-05-26T11:30:00Z",
  "jid": "34650367855:28@s.whatsapp.net"
}
```

Algunos eventos llevan campos extra (`extras`). Catálogo completo:

| Event | Descripción | Extras |
|---|---|---|
| `qr_generated` | Hay un QR nuevo listo en `/qr`. | `last_qr_msg_id` (si lo configuraste vía PATCH) |
| `paired` | Usuario escaneó. Esperando primer `Connected`. | – |
| `connected` | Sesión activa, listo para enviar/recibir. | – |
| `reconnected` | Sesión vuelve tras un `unreachable`. Sólo se emite tras 5s de estabilidad. | – |
| `unreachable` | Disconnected silencioso 60s. Si vuelve antes → blip silencioso (no se emite). | – |
| `disconnected` | Confirmación de desconexión prolongada. | – |
| `logged_out` | Sesión invalidada server-side. Necesita nuevo QR. | – |
| `strike` | WhatsApp emitió ConnectFailure o TemporaryBan. **Acción inmediata recomendada.** | – |
| `spam_blocked` | El spamguard descartó un outgoing duplicado. | `count`, `preview` |
| `ban_risk` | Detector cruzó un threshold (velocity / diversity / delivery_ratio). | `alert`, `score`, `level`, `velocity`, `diversity`, `delivery_ratio` |
| `outgoing_expired` | Un mensaje en el outbox no se pudo entregar antes del TTL. | `queue_id`, `remote_jid`, `preview` |
| `backend_restarting` | Emitido al SIGTERM, antes del shutdown. | – |
| `backend_started` | Emitido por instancia tras `Bootstrap` (8s post-boot). | – |

## Comportamiento

- Los webhooks salen en goroutines independientes — qrsgen no bloquea
  cuando el orquestador tarda en responder.
- Si el POST falla (timeout 10s, 4xx, 5xx), se loguea y se sigue (no hay
  retry queue del lifecycle). Diseño intencional: WhatsApp seguirá
  emitiendo eventos según evolucione la conexión.
- `unreachable` se emite **tras 60s de silencio**: blips cortos no
  generan ruido en el panel del agente.
- `reconnected` se emite **tras 5s de estabilidad** tras un `unreachable`:
  evita flapping.

## Idempotencia

No hay deduplicación a nivel lifecycle — si el mismo evento se emite dos
veces (por ejemplo, dos transiciones `Connected` cercanas), tu orquestador
debe ser idempotente. La identidad práctica de un evento es `(instance,
event, occurred_at)`.

## Glosario

**Lifecycle event**: notificación HTTP que qrsgen POSTea cuando hay un
cambio relevante en una instancia. La URL destino se configura
per-instancia via el campo `events_webhook_url`.

**Rising-edge alert**: alerta que se emite solo cuando se cruza un
umbral (no cuando se sostiene). Usado por `ban_risk` y `spam_blocked`
para evitar inundar al integrador.

**Grace period**: tiempo de espera silencioso tras un evento antes de
emitir su pill al usuario. Sirve para filtrar blips cortos (transitorios)
y evitar ruido visual.

**Blip silencioso**: cuando una desconexión se resuelve sola antes de
expirar el grace period (60s para `unreachable`). qrsgen NO emite pill
en ese caso — el agente humano no se entera.

**Stabilize delay**: tiempo de espera con la conexión estable antes de
emitir `reconnected` tras un `unreachable`. Default 5s. Evita flapping
visual entre `connected` y `disconnected` durante reconexiones inestables.

**Bootstrap window**: ventana de 15s al arrancar el proceso durante la
cual qrsgen suprime los eventos `connected` (avalancha de reconexión).
En su lugar emite `backend_started` por instancia al cumplir 8s post-boot.

**Strike**: evento crítico — WhatsApp tomó una acción sancionatoria
(`TemporaryBan` o `ConnectFailure 4xx`). Requiere intervención manual:
puede indicar que el número está siendo penalizado por uso indebido.

**Spam blocked**: el filtro spamguard descartó un duplicado outgoing.
Lleva `count` (cuántos van bloqueados en la sesión) y `preview` del
contenido descartado.

**Ban risk**: el detector proactivo cruzó al menos un threshold. Lleva
`alert` (cuál de las 3 señales), `score` 0-1 y `level`
(`ok`/`low`/`moderate`/`high`).

**Outgoing expired**: un mensaje en el outbox no se entregó antes del
TTL (5 min). qrsgen lo marca como `expired` y avisa al integrador con
el `preview` para que decida (notificar agente, re-postear, archivar).

**Backend restarting / started**: eventos emitidos al SIGTERM (12s
antes del shutdown) y tras el bootstrap completo (8s post-boot). El
integrador los usa para mostrar pills "buscando conexión" / "conexión
restaurada".
