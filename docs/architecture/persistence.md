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
