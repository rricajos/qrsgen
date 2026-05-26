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
