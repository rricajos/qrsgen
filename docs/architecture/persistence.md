# Persistencia

## Postgres (DB `bridge`)

```
bridge_instance              -- config + state machine timestamps
├── name (PK)
├── jid                      -- WhatsApp JID (NULL hasta pareado)
├── paired_at, ready_at, last_event_at
├── inbox_id                 -- arbitrario, lo decide el integrador
├── events_webhook_url       -- POST destino de lifecycle events
├── spamguard_enabled, spamguard_window_ms, spamguard_min_chars
├── last_qr_msg_id
└── owner_tag                -- string libre para correlación tenant→instancia

bridge_dedup                 -- idempotencia incoming + LID-twin
├── instance_name, remote_jid, content_hash (composite PK)
└── seen_at

bridge_usage_daily           -- counters diarios por instancia
├── instance, day (PK)
├── messages_in, messages_out
├── spamguard_blocks, lifecycle_events
└── updated_at

bridge_outgoing_queue        -- outbox persistido para reconnect
├── id (BIGSERIAL PK)
├── instance, remote_jid
├── payload (JSONB)          -- WebhookPayload completo
├── enqueued_at, expires_at  -- TTL default 5 min
├── attempts, last_error
├── status                   -- pending | sent | expired | failed
└── sent_at

bridge_audit_log             -- append-only, triggers rechazan UPDATE/DELETE
├── id (BIGSERIAL PK)
├── ts, actor, action
├── instance, target
└── metadata (JSONB)

whatsmeow_*                  -- internas de whatsmeow (sessions, keys, etc.)
```

## State in-memory (Go)

| Estructura | Granularidad | Persiste en restart |
|---|---|---|
| `SpamguardTracker` last-2 history + block counter | (instance, jid_user) | no |
| `manager.disconnectNotified` | instance | no |
| `manager.pendingReconnected` | instance | no |
| `manager.pendingUnreachable` | instance | no |
| `banwatch` ring buffer de eventos send | instance | no |
| `usage.Tracker` deltas no-flushed | (instance, day) | no (DB se actualiza c/60s) |

Todo el state in-memory es transitorio por diseño — los efectos
importantes (usage counters, audit log, outbox payloads, spamguard
config) viven en Postgres y sobreviven restarts.

## Glosario

**Primary key (PK)**: columna(s) que identifican unívocamente una fila.
qrsgen las usa para idempotencia en upserts y para `JOIN`s rápidos.

**Composite PK**: clave primaria formada por varias columnas. Ejemplo:
`(instance_name, remote_jid, content_hash)` en `bridge_dedup` — la
combinación de las tres identifica una entrada única.

**JSONB**: tipo de Postgres que guarda JSON en formato binario indexable.
qrsgen lo usa para `payload` (outbox) y `metadata` (audit log).

**BIGSERIAL**: secuencia auto-incrementable de 64 bits que Postgres
genera automáticamente para columnas `id`. Sobrevive a `TRUNCATE` y
restarts.

**TIMESTAMPTZ**: timestamp con zona horaria. Se almacena en UTC
internamente y Postgres lo convierte al timezone del cliente al leer.

**Schema migrations**: cambios a la estructura de las tablas (nuevas
columnas, nuevas tablas, índices). qrsgen las ejecuta cada boot vía
`EnsureSchema()` con `IF NOT EXISTS`.

**Ring buffer**: estructura circular de tamaño fijo que sobrescribe
elementos viejos cuando se llena. BanWatcher la usa para conservar solo
los últimos N minutos de eventos por instancia.

**State machine**: modelo donde un sistema solo puede estar en uno de
varios estados predefinidos y transiciona entre ellos según eventos.
`bridge_instance` modela el ciclo de una sesión WhatsApp con timestamps
(`paired_at`, `ready_at`, etc.).

**LID-twin dedup**: técnica para detectar y descartar mensajes
duplicados que WhatsApp Multi-Device emite por ambas direcciones (JID PN
y JID LID) del mismo cliente.
