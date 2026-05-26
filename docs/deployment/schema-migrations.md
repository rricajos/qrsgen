# Schema migrations

`main.go` ejecuta migraciones idempotentes en el bootstrap:

| Llamada | Crea |
|---|---|
| `lib.EnsureBridgeSchema` | `bridge_dedup` |
| `usage.EnsureSchema` | `bridge_usage_daily` |
| `audit.EnsureSchema` | `bridge_audit_log` + triggers append-only |
| `outbox.EnsureSchema` | `bridge_outgoing_queue` |
| `manager.EnsureSchema` | `bridge_instance` + columnas progresivas (`owner_tag`, `last_qr_msg_id`, spamguard, etc.) |

Todas usan `CREATE TABLE IF NOT EXISTS` y `ALTER TABLE ... ADD COLUMN IF
NOT EXISTS`. Sin versionado formal — para producción con varios
deployers considerar `golang-migrate` o equivalente.

## Compatibilidad backwards

Las migraciones son **siempre aditivas** (nuevas columnas con valores
sensibles default, nuevas tablas independientes). Esto garantiza que un
rollback a una versión anterior funcione sin tocar el schema: las
columnas/tablas extra simplemente quedan ignoradas por el binario viejo.
