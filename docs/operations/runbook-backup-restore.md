# Runbook: backup y restore de qrsgen

Procedimiento para backup completo del estado de qrsgen y restore en un
nuevo VPS o tras corrupción del Postgres. Cubre las tablas propias del
bridge + el sqlstore de whatsmeow (que contiene las sesiones — sin
esto pierdes todas las sesiones paireadas y hay que re-escanear QR).

> Este runbook NO cubre el backup de Chatwoot. Para eso ver la
> documentación de Chatwoot upstream. Estos dos servicios son
> independientes — restore de qrsgen no requiere que Chatwoot esté
> en el mismo estado, sólo que sea alcanzable y que los `inbox_id`
> referenciados sigan existiendo.

## Qué hay que respaldar

### 1. Tablas Postgres del bridge

| Tabla | Contenido | Coste perderla |
|---|---|---|
| `bridge_instance` | Instancias + JIDs + owner_tags + webhook URLs | Alto — pierdes el mapeo |
| `bridge_msg_history` | Tracker de retroactive name update + replyto outgoing | Medio — retroactive update parará durante el TTL |
| `bridge_chat_anchor` | Anchors per-chat para on-demand history sync | Bajo — se reconstruye automáticamente |
| `bridge_outgoing_queue` | Outbox de mensajes en cola | Alto si hay msgs pendientes |
| `bridge_dedup` | Hashes per-instancia de los últimos N msgs | Bajo — ventana de minutos |
| `bridge_spamguard` | Estado del detector spam outgoing | Medio — historial perdido |
| `bridge_audit_log` | Log inmutable de ops admin | Alto si lo necesitas legal/forense |
| `bridge_tenant` | Config multi-tenant (downstream URLs/tokens) | Alto — pierdes mapeo cliente |
| `bridge_usage_*` | Counters per-instancia per-día para billing | Alto si facturas por uso |

### 2. Tablas Postgres de whatsmeow (sesiones)

whatsmeow guarda en el mismo Postgres su `sqlstore.Container`:

```
whatsmeow_device
whatsmeow_identity_keys
whatsmeow_pre_keys
whatsmeow_sessions
whatsmeow_sender_keys
whatsmeow_app_state_*
whatsmeow_contacts
whatsmeow_message_secrets
whatsmeow_chat_settings
whatsmeow_lid_map
```

**Crítico**: sin estas tablas hay que re-escanear QR de cada
instancia y se pierde el historial de pushnames + LID mappings.

### 3. Imagen Docker

El binario qrsgen está versionado por tag. No necesitas backup; lo
re-pulls de `ghcr.io/rricajos/qrsgen:X.Y.Z`. Backup tu `.env` del
deploy (variables como `QRSGEN_API_TOKEN`, `DOWNSTREAM_*`,
`CHATWOOT_DB_URL`, etc.).

## Backup procedure

### Backup completo (recomendado)

`pg_dump` de la database `bridge` entera. Captura las tablas qrsgen +
whatsmeow en una transacción consistente:

```bash
# Variables
POSTGRES_HOST=postgres        # alias overlay del swarm
POSTGRES_USER=postgres
POSTGRES_DB=bridge
PGPASSWORD=$(sudo docker service inspect qrsgen_qrsgen \
  --format '{{range .Spec.TaskTemplate.ContainerSpec.Env}}{{println .}}{{end}}' \
  | grep POSTGRES_PASSWORD | cut -d= -f2-)

# Dump
sudo docker exec -e PGPASSWORD="$PGPASSWORD" \
  $(sudo docker ps -q -f name=postgres | head -1) \
  pg_dump -U $POSTGRES_USER -d $POSTGRES_DB \
  --format=custom --no-owner --no-acl \
  > /var/backups/qrsgen-$(date +%Y%m%d-%H%M%S).dump

# Verifica tamaño + integridad mínima
ls -lh /var/backups/qrsgen-*.dump | tail -1
sudo docker exec $(sudo docker ps -q -f name=postgres | head -1) \
  pg_restore -l /var/backups/qrsgen-$(date +%Y%m%d-%H%M%S).dump | head -20
```

El formato `custom` (`.dump`) es comprimido + paralelizable en restore.
Para una DB de ~1GB el dump ronda 100-200MB.

### Schedule recomendado

- **Diario**: dump completo, retención 7 días.
- **Semanal**: copia del dump del lunes a almacenamiento off-site
  (S3, B2, otro VPS).
- **Pre-deploy**: dump antes de cada `docker service update` de
  qrsgen — permite rollback total si una migración rompe schema.

Ejemplo de cron mínimo:

```cron
# /etc/cron.d/qrsgen-backup
0 3 * * * root /opt/qrsgen/ops/backup/run.sh daily
0 4 * * 1 root /opt/qrsgen/ops/backup/run.sh weekly-offsite
```

(El repo incluye `ops/backup/` con scripts más completos — adapta
según tu storage.)

## Restore procedure

### Caso A: Restaurar en mismo Postgres tras corrupción de la DB qrsgen

1. **Para qrsgen** para que no escriba durante el restore:

   ```bash
   sudo docker service scale qrsgen_qrsgen=0
   ```

2. **Drop la database actual** (cuidado):

   ```bash
   PGPASSWORD=$PGPASS sudo docker exec -i $(sudo docker ps -q -f name=postgres | head -1) \
     psql -U postgres -d postgres -c "DROP DATABASE bridge WITH (FORCE);"
   PGPASSWORD=$PGPASS sudo docker exec -i $(sudo docker ps -q -f name=postgres | head -1) \
     psql -U postgres -d postgres -c "CREATE DATABASE bridge;"
   ```

3. **Restore del dump**:

   ```bash
   PGPASSWORD=$PGPASS sudo docker exec -i $(sudo docker ps -q -f name=postgres | head -1) \
     pg_restore -U postgres -d bridge --no-owner --no-acl \
     < /var/backups/qrsgen-YYYYMMDD-HHMMSS.dump
   ```

4. **Re-arranca qrsgen**:

   ```bash
   sudo docker service scale qrsgen_qrsgen=1
   ```

5. **Verifica**:

   ```bash
   sudo docker service logs qrsgen_qrsgen --since 2m | grep -i "qrsgen ready"
   curl -sS -H "Authorization: Bearer $TOK" http://qrsgen:3100/api/instances | jq '.[].state'
   ```

   Cada instancia debería pasar `connected` en <30s sin necesidad
   de re-escanear QR (porque las sesiones whatsmeow restauradas
   incluyen los identity keys).

### Caso B: Restaurar en VPS nuevo (DR completo)

1. Provisiona el nuevo VPS con Docker Swarm + Postgres + Chatwoot.
2. Carga el dump en el nuevo Postgres (mismo procedimiento que
   Caso A, paso 3).
3. Despliega qrsgen con el mismo `.env` (mismas credentials,
   webhook URLs, etc.).
4. Si Chatwoot cambia de host (nuevo dominio), actualiza:
   - `DOWNSTREAM_BASE_URL` en el `.env` del stack qrsgen
   - `webhook_url` en cada `channel_api` row de Chatwoot — SQL
     directo en la DB de Chatwoot

### Caso C: Restaurar solo `bridge_*` (no whatsmeow)

Si quieres restaurar el estado del bridge pero **forzar re-pareo
de todas las instancias** (porque sospechas que las sesiones están
quemadas por algún incident), restaura selectivo:

```bash
# Restore solo las tablas bridge_*, excluye whatsmeow_*
pg_restore -l backup.dump | grep "bridge_" | pg_restore --use-list=- \
  -U postgres -d bridge < backup.dump
```

Tras esto, cada instancia mostrará `state=qr_pending` al arrancar y
necesitarás escanear el QR (desde Chatwoot conv `QR - X` con el
flujo `OMNIA_QR_CHAT` `qr` command).

## Verificación post-restore

Checklist:

- [ ] `/api/health` devuelve 200 con `instances_connected ==
      instances_total`.
- [ ] Cada conv `QR - X` en Chatwoot recibe el pill
      `🟢 QRsGEN vX operativo` tras `backend_started`.
- [ ] Un mensaje de prueba (curl outgoing o agente escribiendo
      desde Chatwoot) llega al móvil destino.
- [ ] Tablas `bridge_audit_log` tiene rows recientes (verifica
      que los triggers anti-UPDATE/DELETE están reactivados):

  ```sql
  -- Esto DEBE fallar tras el restore con un mensaje del trigger:
  UPDATE bridge_audit_log SET actor='test' WHERE id=1;
  ```

  Si el UPDATE pasa sin error, los triggers no se restauraron
  correctamente. Re-aplica el schema:

  ```sql
  -- Conecta al pod qrsgen y al arrancar EnsureAuditSchema recrea triggers idempotentemente.
  ```

## Notas de capacidad

- DB `bridge` típicamente crece ~10-50MB/día con 4-5 instancias
  activas (history import puntual añade picos).
- `bridge_audit_log` es append-only — sin retención automática
  crece linealmente. Considera particionado por mes si superas
  100k rows (postpuesto a v0.65+ por ahora).
- `bridge_outgoing_queue` se auto-purga (TTL 5 min) — no debería
  bloatear nunca.
- `whatsmeow_*` crece con cada msg recibido y con cada nuevo
  contacto/grupo. ~1GB tras 6 meses de operación con 5 instancias
  conversando activamente.

---

Última actualización: v0.64.6 (2026-06-01)
