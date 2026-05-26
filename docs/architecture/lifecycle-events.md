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

Ver [docs/api/lifecycle-webhooks.md](../api/lifecycle-webhooks.md) para
el catálogo completo y el shape del payload.
