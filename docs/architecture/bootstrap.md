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
