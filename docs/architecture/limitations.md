# Limitaciones conocidas

- **Único downstream por proceso**: `DOWNSTREAM_BASE_URL` y
  `DOWNSTREAM_API_TOKEN` son globales. Para servir varios downstream
  distintos desde un solo qrsgen habría que enrutar por `owner_tag` y
  mantener un mapa de clientes HTTP. Workaround actual: un proceso
  qrsgen por downstream, todos apuntando al mismo Postgres (separan
  namespaces por nombres de instancia).
- **LID twin del cliente**: el dedup limpia lo que sincronizamos
  downstream, pero el destinatario sigue recibiendo 2 mensajes si
  WhatsApp ya hizo dispatch dual. Se resolvería migrando a Cloud API
  oficial.
- **Outbox sin cifrado en disco**: los payloads de WhatsApp viven en
  `bridge_outgoing_queue` durante hasta 5 min. Si comprometen el
  Postgres, los mensajes en cola son legibles. En multi-tenant serio se
  debería cifrar el payload por tenant.
- **BanWatcher per-process**: no comparte estado entre réplicas (qrsgen
  corre con `replicas: 1` por diseño — una sesión WhatsApp por proceso —
  así que esto no es un problema en producción típica).
- **Audit log no firmado**: la inmutabilidad la garantizan triggers DB,
  pero un atacante con permisos DBA puede drop the trigger + tamper.
  Para evidence en juicio, firmar cada fila + ship a syslog externo.
- **Sin versionado formal de schema migrations**: las llamadas
  `EnsureSchema()` son idempotentes pero no llevan un version table.
  Para producción con varios deployers, considerar `golang-migrate`.
