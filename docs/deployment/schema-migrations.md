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

## Glosario

**Schema migration**: cambio a la estructura de las tablas (nuevas
columnas, índices, tablas, triggers).

**Idempotente**: una migración que se puede ejecutar múltiples veces
sin efectos secundarios. qrsgen usa `IF NOT EXISTS` por todas partes.

**`CREATE TABLE IF NOT EXISTS`**: crea la tabla solo si no existe. No
falla si ya está.

**`ALTER TABLE ... ADD COLUMN IF NOT EXISTS`**: añade una columna solo
si no existe. Disponible en Postgres 9.6+.

**Aditivo**: cambios que solo añaden (nuevas tablas/columnas) sin
modificar o borrar las existentes. Compatibles con rollback.

**Schema versioning** (no implementado): registro explícito de qué
versión del schema está aplicada (tabla `schema_migrations`). qrsgen
no la tiene aún.

**golang-migrate**: librería Go popular para migraciones con
versionado y up/down. Más robusto si qrsgen crece.

**Backwards compatibility**: garantía de que una versión nueva puede
correr con un schema que sigue válido para la versión anterior. qrsgen
lo respeta para permitir rollbacks seguros.
