# Capa 6 — Audit log inmutable

## Qué hace

Toda operación relevante (provisión, patch, delete, eventos de outbox,
boot del proceso) se persiste en `bridge_audit_log` con:

- `id` BIGSERIAL.
- `ts` TIMESTAMPTZ.
- `actor` (`api` | `system`).
- `action` (`instance.create` | `instance.patch` | `instance.delete` |
  `outbox.enqueue` | `outbox.expire` | `outbox.failed` | `backend.boot`).
- `instance`, `target`, `metadata` (JSONB).

Dos triggers plpgsql rechazan `UPDATE` y `DELETE` sobre la tabla:

```sql
CREATE TRIGGER bridge_audit_log_no_update
 BEFORE UPDATE ON bridge_audit_log
 FOR EACH ROW EXECUTE FUNCTION bridge_audit_log_reject();
```

```sql
CREATE OR REPLACE FUNCTION bridge_audit_log_reject() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'bridge_audit_log is append-only; UPDATE/DELETE forbidden';
END $$;
```

Una app comprometida no puede reescribir el log sin privilegios DBA
directos sobre la DB.

## Qué mitiga

Forensics post-incidente: cualquier acción contra la API queda registrada
con timestamp. Para investigar un strike o un mensaje fantasma, vas al
audit log y reconstruyes la cadena.

## Cómo verificarla

```bash
# Listar últimas 5 entradas:
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/audit?limit=5" | jq

# Probar que UPDATE está bloqueado a nivel DB:
docker exec postgres psql -U postgres -d bridge \
  -c "UPDATE bridge_audit_log SET actor='nope' WHERE id=1;"
# ERROR: bridge_audit_log is append-only; UPDATE/DELETE forbidden
```

## Limitaciones

- El audit es **append-only en la tabla**, no signed. Un atacante con
  acceso DBA podría drop the trigger + tamper. Pero si tienes ese nivel
  de compromiso, el sistema está perdido por otras razones.
- Para evidence en juicio se debería **firmar cada entrada** y/o
  shipearla a un syslog inmutable (CloudWatch Logs, Loki con immutable
  retention). Pendiente.

## Glosario

**Audit log**: registro cronológico inmutable de operaciones relevantes
del sistema. Pensado para forensics, compliance y auditoría.

**Append-only**: tabla/storage donde solo se permiten INSERTs. Las
filas existentes no se pueden modificar ni borrar. qrsgen lo garantiza
a nivel DB con triggers.

**Trigger (PL/pgSQL)**: función almacenada en Postgres que se ejecuta
automáticamente ante INSERT/UPDATE/DELETE. qrsgen los usa para forzar
inmutabilidad.

**PL/pgSQL**: lenguaje procedural de Postgres para escribir triggers,
funciones y procedimientos almacenados.

**RAISE EXCEPTION**: instrucción PL/pgSQL que aborta la operación
actual con un mensaje de error. Los triggers de qrsgen la usan para
rechazar UPDATE/DELETE.

**Inmutabilidad a nivel DB**: la garantía se aplica antes de que
cualquier app pueda interferir. Una app comprometida no puede rewriter
filas sin permisos especiales sobre Postgres.

**DBA**: rol con permisos administrativos completos sobre la DB.
Suficiente para drop triggers + tampering — pero requiere acceso
explícito a las credenciales de DBA.

**Tamper-evident**: propiedad donde cualquier modificación es
detectable. qrsgen va un paso más allá — es tamper-resistant (no solo
se detecta, se previene).

**Audit entry firmada**: extensión donde cada fila lleva una firma
HMAC del actor. Permite detectar incluso modificaciones desde DBA.
Pendiente en qrsgen.

**Syslog inmutable**: servicio de logs externo con retención forzosa
(no se pueden borrar). Útil para compliance — la app no puede
manipular el log post-hoc. Ejemplos: CloudWatch Logs con retention,
Loki con immutable mode.
