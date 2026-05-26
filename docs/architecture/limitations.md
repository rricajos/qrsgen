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

## Glosario

**Limitación conocida**: comportamiento o capacidad que qrsgen no
implementa actualmente y que es visible (no un bug oculto). Documentada
para que el integrador la conozca al evaluar el sistema.

**Multi-downstream**: capacidad de servir varios destinos downstream
desde un mismo proceso. NO soportado — `DOWNSTREAM_*` vars son globales.

**LID twin**: cuando WhatsApp Multi-Device emite el mismo mensaje desde
un cliente tanto vía JID PN como JID LID, generando duplicados.

**Cloud API (oficial)**: API oficial de WhatsApp Business mantenida por
Meta. Pagada, sin riesgo de ban, con SLA. Migrar a ella eliminaría
muchas limitaciones (LID twin, ban risk, etc.) pero implica cambio de
modelo de negocio.

**Cifrado en disco**: protección de datos persistidos. qrsgen guarda los
payloads en `bridge_outgoing_queue` sin cifrar. Para multi-tenant serio,
considerar cifrar por tenant antes de persistir.

**eBPF**: tecnología del kernel Linux para insertar programas seguros
en el path del syscall. Permite observabilidad y filtros a nivel kernel
(Falco, Tetragon). qrsgen no la usa actualmente.

**Réplica**: copia del proceso/servicio corriendo en paralelo para HA o
load balancing. qrsgen corre con `replicas: 1` por diseño (una sesión
WhatsApp = un proceso).

**DBA** (Database Administrator): rol con permisos completos sobre la
DB (CREATE/DROP de objetos, alter triggers, etc.). Un atacante con
permisos DBA puede romper la inmutabilidad del audit log dropping los
triggers.

**Schema versioning**: registro explícito de qué versión del schema
está aplicada (típicamente una tabla `schema_migrations` con histórico).
qrsgen no la tiene — sus migraciones son idempotentes pero sin tracking
formal.

**golang-migrate**: librería Go popular para gestionar migraciones de
schema con versionado, up/down, etc. Sería una alternativa más robusta
que el `IF NOT EXISTS` actual si qrsgen crece.
