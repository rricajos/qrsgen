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

## Glosario

**Goroutine**: unidad de concurrencia en Go. Más ligera que un thread
de OS — un proceso puede tener miles. qrsgen las usa para WebSockets,
loops de mantenimiento y webhooks async.

**Mutex** (Mutual Exclusion): primitiva que protege acceso concurrente
a estructuras compartidas. Solo una goroutine puede tener el lock a
la vez.

**RWMutex** (Read-Write Mutex): variante del mutex que permite varios
lectores concurrentes pero un solo escritor. Más eficiente cuando las
lecturas dominan (`manager.mu`).

**pgxpool**: pool de conexiones Postgres thread-safe nativamente. Cada
goroutine pide una conexión, hace su query y la devuelve.

**Fire-and-forget**: patrón donde se lanza una operación async y no se
espera resultado. qrsgen emite lifecycle webhooks así para no bloquear
si el integrador tarda.

**SIGTERM**: señal Unix que pide a un proceso que se cierre limpiamente
(en contraste con SIGKILL, que lo mata inmediatamente). Docker envía
SIGTERM al actualizar/parar containers.

**signal.NotifyContext**: helper Go que crea un context cancelable
cuando llega una señal específica. qrsgen lo usa para orquestar el
graceful shutdown.

**Graceful shutdown**: secuencia ordenada para cerrar el proceso sin
perder datos. qrsgen emite `backend_restarting`, espera 12s, cierra el
HTTP server, cierra los WebSockets, hace flush final del usage tracker
y sale.

**stop-first (update_config)**: política Docker Swarm donde el
container viejo se detiene antes de arrancar el nuevo. Evita
condiciones de carrera (dos containers compitiendo por la misma sesión
WhatsApp). Tradeoff: ~15s de downtime por deploy.

**JID conflict**: situación donde WhatsApp ve dos clientes intentando
usar la misma sesión simultáneamente, y desconecta ambos por seguridad.
Lo previenes con stop-first.
