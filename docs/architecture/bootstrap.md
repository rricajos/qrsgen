# Bootstrap

`main.go` al arrancar:

1. `config.Load()` parsea env vars.
2. `pgxpool` conecta a Postgres.
3. `lib.EnsureBridgeSchema` + `usage.EnsureSchema` + `audit.EnsureSchema`
   + `outbox.EnsureSchema` + `manager.EnsureSchema` aplican migraciones
   idempotentes.
4. `manager.New()` crea el container whatsmeow apuntando al mismo
   Postgres (whatsmeow gestiona sus tablas `whatsmeow_*` allí).
5. `usage.Tracker` arranca su goroutine de flush cada 60s.
6. `audit.Logger` registra `backend.boot`.
7. `manager.Bootstrap()`:
   - `SELECT name, jid FROM bridge_instance`.
   - Marca ventana de bootstrap (15s) → suprime webhooks `connected` de
     la avalancha de reconexiones.
   - Para cada fila: crea `wameow.Conn`, whatsmeow carga la sesión por
     JID, abre WebSocket, emite `paired` + `connected`.
8. `banwatch.Start()` arranca el evaluator cada 30s.
9. `outbox.Start()` arranca drainer (5s) + expirer (30s).
10. Tras 8s, `BroadcastBackendStarted()` emite `backend_started` por
    instancia.
11. Echo HTTP server arranca en `:3100`.

## Glosario

**Bootstrap**: secuencia de arranque del proceso, donde se inicializan
todos los recursos (DB, schemas, container whatsmeow, goroutines) antes
de aceptar tráfico HTTP.

**`pgxpool`**: pool de conexiones Postgres para Go (de la librería pgx).
Reusa conexiones TCP en lugar de abrir una nueva por query, lo que es
mucho más rápido.

**EnsureSchema**: convención del proyecto donde cada paquete tiene una
función que crea sus tablas/índices con `IF NOT EXISTS`. Idempotente:
seguro de ejecutar en cada boot.

**Container whatsmeow**: instancia del `sqlstore.Container` de
whatsmeow que gestiona las tablas `whatsmeow_*` (sesiones, claves
criptográficas, etc.).

**Migración idempotente**: cambio de schema (CREATE / ALTER) que se
puede ejecutar múltiples veces sin efectos secundarios. Permite
relanzar el bootstrap sin riesgo.

**Bootstrap window**: ventana de 15 s al arrancar durante la cual qrsgen
suprime los webhooks `connected` (avalancha de reconexión). En su lugar
emite un único `backend_started` por instancia al cumplir 8 s post-boot.

**Goroutine**: hilo ligero gestionado por el runtime de Go. qrsgen
arranca varias goroutines de larga duración para drainer del outbox,
flush del usage tracker, evaluator del banwatch, etc.

**Echo HTTP server**: framework HTTP de Go que qrsgen usa para servir
`/api/*`. Maneja routing, middleware y JSON binding.
