# Concurrencia

## Goroutines por instancia

- **1 goroutine por instancia** dentro de whatsmeow para su WebSocket.
- Goroutines compartidas:
  - `usage.Tracker.loop` — flush periódico cada 60s.
  - `banwatch.loop` — evaluator cada 30s.
  - `outbox.drainLoop` — drainer cada 5s.
  - `outbox.expireLoop` — expirer cada 30s.
  - 1 goroutine por lifecycle webhook outbound (no bloquea).

## Mutexes

| Mutex | Protege |
|---|---|
| `manager.mu` (RWMutex) | `instances` map |
| `manager.reconMu` | `disconnectNotified`, `pendingReconnected` |
| `manager.unreachMu` | `pendingUnreachable` |
| `SpamguardTracker.mu` | historial last-2 + counter de bloqueos |
| `banwatch.mu` | buckets de eventos send |
| `usage.mu` | buckets pendientes de flush |
| `outbox.mu` | serializa el drainer (DB transactions ordenadas) |

`Deduper` usa pgxpool directamente (thread-safe nativo).

## Lifecycle webhooks: fire-and-forget

Los webhooks salen en goroutines independientes con timeout 10s. Si el
orquestador tarda, qrsgen no se bloquea. Esto significa que **el orden
de los eventos en el orquestador no está garantizado** entre instancias
distintas — pero sí dentro de una misma instancia.

## Graceful shutdown

`signal.NotifyContext(SIGTERM)` activa la secuencia:

```
SIGTERM recibido
   │
   ▼
BroadcastBackendRestarting() → emit lifecycle a cada instancia
   │
   ▼
sleep 12s   ← el downstream drena webhooks pendientes
   │
   ▼
e.Shutdown(ctx)   ← Echo server cierra accept loop
   │
   ▼
mgr.Shutdown()   ← cierra todas las wameow.Conn (con sus WebSockets)
   │
   ▼
usage.Tracker flush final (no bloquea más de 1s)
   │
   ▼
proceso exit
```

Downtime efectivo del WebSocket: ~10-15 segundos (más con
`order: stop-first` del compose, diseñado así para evitar JID
conflicts). Mensajes outgoing que llegan durante esa ventana se encolan
en el outbox y se entregan al volver.
