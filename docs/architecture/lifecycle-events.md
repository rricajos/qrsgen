# Eventos de ciclo de vida

`wameow.Conn` dispara hacia `manager.onLifecycle`:

```
events.PairSuccess         → EventPaired
events.Connected           → EventConnected
events.Disconnected        → EventDisconnected (con grace 60s para evitar pills de blips)
events.LoggedOut           → EventLoggedOut
events.TemporaryBan        → EventStrike
events.ConnectFailure 4xx  → EventStrike
listenQR(qrChan) "code"    → EventQRGenerated
```

## Procesamiento

`manager.onLifecycle`:

1. UPDATE `bridge_instance` (timestamps + JID si aplica).
2. Decide qué webhook emitir, con grace/stabilize:
   - `EventDisconnected` → silencio 60s; si vuelve antes, blip
     silencioso; si no, emite `unreachable`.
   - `EventConnected` tras `unreachable` previo → espera 5s de
     estabilidad y emite `reconnected` (NO durante bootstrap window).
3. POST al `events_webhook_url` con
   `{instance, event, jid, occurred_at, ...}`.
4. `metrics.LifecycleEvents` + `usage.IncLifecycle`.

## Eventos custom (no de whatsmeow)

- `spam_blocked` — emitido desde `outgoing.HandleFor` con
  `{count, preview}`.
- `backend_restarting` — emitido por `BroadcastBackendRestarting()` al
  SIGTERM.
- `backend_started` — emitido por `BroadcastBackendStarted()` tras 8s
  post-bootstrap.
- `ban_risk` — emitido por `banwatch.evaluate` cuando un threshold cruza.
- `outgoing_expired` — emitido por `outbox.expirer` cuando un mensaje
  expira sin entregarse.

## Suavizado de pills

Para que el panel del agente no se inunde de notificaciones espurias:

- **`unreachable` con 60s de silencio**: blips cortos (red intermitente
  típica) NO generan pill.
- **`reconnected` con 5s de estabilidad**: tras un `unreachable` previo,
  esperamos 5s con la conexión estable antes de emitir el evento.
- **bootstrap window de 15s**: durante el arranque, se suprimen los
  webhooks `connected` de la avalancha de reconexiones. En su lugar,
  `backend_started` resume el estado a los 8s.

## Retry exponencial para eventos críticos

Algunos eventos lifecycle son **operativamente críticos** — si tu
orquestador está caído cuando llegan, perderlos tiene consecuencias
reales (estado del agente, contabilidad, decisiones de pausa de
envíos). qrsgen los reintenta automáticamente:

```
POST inicial (síncrono)
   │
   │ fallo (timeout, 4xx, 5xx, network error)
   ▼
attempt 2: tras 5s    (async, en goroutine)
   │
   │ fallo
   ▼
attempt 3: tras 30s
   │
   │ fallo
   ▼
attempt 4: tras 5 min
   │
   │ fallo
   ▼
Drop + log ERROR + métrica qrsgen_lifecycle_webhook_retries_total{outcome="exhausted"}
```

Total: ~5.5 min de ventana hasta abandonar.

**Eventos críticos** (con retry):

- `strike` — WhatsApp baneó/restringió. Requiere acción inmediata.
- `ban_risk` — Detector cruzó threshold. Preventivo de strike.
- `outgoing_expired` — Mensaje en outbox no entregado. Notificar al agente.
- `logged_out` — Sesión inválida. Necesita re-pairing.
- `spam_blocked` — Bloqueo spamguard. El usuario debe verlo (contador).
- `backend_restarting` — Aviso pre-shutdown. El agente debe verlo.

**Eventos NO críticos** (sin retry, solo intento inicial):

- `qr_generated` — Se re-emite cada 20s naturalmente.
- `paired` / `connected` / `reconnected` — Tu sistema puede deducirlo
  del próximo evento.
- `unreachable` / `disconnected` — Los re-emite WhatsApp cuando vuelva.
- `backend_started` — Si tu orquestador está caído cuando arranca
  qrsgen, perdértelo no rompe nada.

**Métrica**: `qrsgen_lifecycle_webhook_retries_total{event,outcome}`
con `outcome` ∈ {`success`, `exhausted`}. Para alertas Prometheus:

```promql
# Algún evento crítico llegó a agotarse — el orquestador está caído
# desde hace al menos 5 minutos.
increase(qrsgen_lifecycle_webhook_retries_total{outcome="exhausted"}[10m]) > 0
```

### ¿Por qué no persistir en DB como hace el outbox?

Trade-off considerado. Los lifecycle events son **idempotentes** desde
el punto de vista del agente humano (un `strike` notificado dos veces
es OK), y la mayoría se re-emite naturalmente cuando la conexión
vuelve. Persistir + drainer añadía complejidad por poco beneficio
real. Si en producción vemos pérdida real, se reconsidera.

Ver [docs/api/lifecycle-webhooks.md](../api/lifecycle-webhooks.md) para
el catálogo completo y el shape del payload.

## Glosario

**Lifecycle**: ciclo de vida de la conexión WhatsApp. Va de
`qr_pending` → `paired` → `connected` → ... → `disconnected` /
`logged_out`. qrsgen emite un evento en cada transición relevante.

**events.PairSuccess**: evento de la librería whatsmeow que se emite
cuando un usuario escanea el QR exitosamente. qrsgen lo mapea a
`EventPaired`.

**events.Connected**: evento de whatsmeow que indica que el WebSocket
contra Meta está activo y la sesión es válida.

**events.Disconnected**: evento de whatsmeow cuando se cae el WebSocket.
qrsgen aplica un grace period de 60s antes de emitir `unreachable` para
filtrar blips cortos.

**events.LoggedOut**: evento de whatsmeow cuando WhatsApp invalida la
sesión server-side. Hace falta nuevo QR; la fila bridge_instance se
preserva pero su jid pasa a NULL.

**events.TemporaryBan**: WhatsApp aplica una restricción temporal a la
sesión (anti-spam, comportamiento sospechoso). qrsgen lo eleva como
evento `strike`.

**events.ConnectFailure 4xx**: respuesta HTTP 4xx desde Meta durante el
handshake. Indica que el cliente está rechazado a nivel servidor.
También se mapea a `strike`.

**listenQR (qrChan)**: canal Go por el que whatsmeow emite los códigos
QR sucesivos. qrsgen lo escucha y emite `EventQRGenerated` por cada uno.

**Grace period**: tiempo de espera silencioso tras un evento antes de
emitir el webhook al integrador. Filtra blips transitorios.

**Stabilize delay**: tras un `unreachable`, qrsgen espera 5s de
conexión estable antes de emitir `reconnected`. Evita flapping si la
red está inestable.

**Custom event**: evento que NO viene de whatsmeow sino que qrsgen
inventa internamente (`spam_blocked`, `ban_risk`, `outgoing_expired`,
`backend_restarting`, `backend_started`).

**Bootstrap window suppression**: durante los primeros 15s tras arrancar,
qrsgen no emite `connected` events (evita avalancha tras restart).
Sustituido por un único `backend_started` a los 8s.
