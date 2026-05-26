# Operations runbook

Recetas para operar qrsgen en producción. Asume que tienes Bearer token
en `$TOK` y los nombres de instancia en `whatsapp-main` / `whatsapp-sales`
como placeholders.

## Diagnóstico rápido

### "¿Está vivo qrsgen?"

```bash
docker service ps qrsgen_qrsgen
curl -sS http://qrsgen:3100/api/health | jq   # desde la overlay
```

Si el container no está running pero tampoco crashea:

```bash
docker service logs qrsgen_qrsgen --tail 50 | grep -E "ERROR|panic|FATAL"
```

### "¿Cuántas instancias están conectadas?"

```bash
curl -sS -H "Authorization: Bearer $TOK" http://qrsgen:3100/api/instances \
  | jq -r '.[] | [.name, .state, .jid // ""] | @tsv'
```

### "¿Una instancia específica está OK?"

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main | jq
```

Campos clave: `state` (`ready` = OK), `last_event_at` (¿hace cuánto que
no pasa nada?), `owner_tag` (tenant).

### "¿Cuántos mensajes han pasado hoy?"

Vía métricas:

```bash
curl -sS http://qrsgen:3100/metrics | grep qrsgen_messages_total
```

O vía usage tracking:

```bash
TODAY=$(date -u +%Y-%m-%d)
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/usage?from=$TODAY&to=$TODAY" | jq
```

### "¿Está bloqueando spam?"

```bash
curl -sS http://qrsgen:3100/metrics | grep qrsgen_spamguard_blocks_total
```

O en el snapshot de instancia, ver el campo `spamguard_blocks`.

### "¿Hay mensajes encolados sin entregar?"

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/outbox | jq
```

Campos: `pending` (los que aún se intentarán entregar), `sent`, `expired`,
`failed`. Si `pending > 0` durante mucho rato, la instancia probablemente
está disconnected.

### "¿Estamos cerca de un ban?"

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/ban-risk | jq
```

Mira `level` (`ok` / `low` / `moderate` / `high`) y `alerts`. Si hay
`alerts: ["high_velocity"]` o similar, **reduce ritmo de envíos**.

---

## Procedimientos comunes

### Re-pareado de un técnico (sesión perdida)

1. El integrador (n8n / app custom) pide al usuario
   `POST /api/instances/<INSTANCE_NAME>` desde el chat ops del contacto.
2. Si la instancia ya existe pero está `disconnected`/`logged_out`, qrsgen
   regenera el QR.
3. qrsgen emite `qr_generated` cada ~20s en el `events_webhook_url`.

Manual:

```bash
curl -X POST -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/refresh-qr
```

Descarga el PNG:

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/qr -o /tmp/qr.png
```

### Borrar una instancia completamente

```bash
# 1. Desactiva sesión en WhatsApp servers
curl -X POST -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/logout

# 2. Borra config + state
curl -X DELETE -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main
```

El DELETE queda registrado en `bridge_audit_log` (`action: instance.delete`).

> ⚠️ El DELETE **no** borra las tablas `whatsmeow_*` asociadas. Si quieres
> liberar también las keys, ejecuta manualmente sobre Postgres tras el
> DELETE.

### Restart del backend

```bash
docker service update --force qrsgen_qrsgen
```

Tras `SIGTERM`, qrsgen:

1. Emite `backend_restarting` a cada instancia con `events_webhook_url`.
2. Espera 12 s de grace para que el downstream termine de procesar
   webhooks pendientes.
3. Cierra todas las conexiones whatsmeow.
4. El `usage.Tracker` hace un flush final a la DB.
5. Container nuevo arranca, ejecuta `Bootstrap()` que reconecta cada
   sesión en paralelo.
6. Tras 8 s, emite `backend_started` por instancia.

Downtime efectivo del WebSocket: ~10-15 s (más con `order: stop-first`
en el update_config — diseñado así para evitar JID conflicts). **Mensajes
outgoing que llegan durante esa ventana se encolan en el outbox y se
entregan al volver**.

### Asignar `owner_tag` a una instancia (multi-tenant)

```bash
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"owner_tag":"tenant-acme"}' \
  http://qrsgen:3100/api/instances/whatsapp-main
```

Para borrar el tag:

```bash
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"owner_tag":""}' \
  http://qrsgen:3100/api/instances/whatsapp-main
```

### Generar reporte mensual de uso para billing

```bash
THIS_MONTH=$(date -u +%Y-%m)
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/usage/summary?from=$THIS_MONTH&to=$THIS_MONTH" | jq
```

Devuelve filas agrupadas por `(owner_tag, mes)`. Mapeas tu modelo de
tenant al `owner_tag` y aplicas tu pricing.

### Cambiar el `inbox_id` de una instancia

```bash
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"inbox_id":90}' \
  http://qrsgen:3100/api/instances/whatsapp-sales
```

### Toggle spamguard

```bash
# Activar
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"spamguard_enabled":true}' \
  http://qrsgen:3100/api/instances/whatsapp-sales
```

### Drenar el outbox manualmente (no recomendado)

El drainer goroutine corre cada 5s — no debería necesitarse intervención
manual. Si quieres ver el contenido pendiente:

```bash
PG=$(docker ps --format '{{.Names}}' | grep ^postgres)
docker exec "$PG" psql -U postgres -d bridge -c "
  SELECT id, instance, remote_jid, attempts, expires_at, status
  FROM bridge_outgoing_queue WHERE status='pending'
  ORDER BY id LIMIT 50;"
```

Para forzar expiración de algo concreto (sin entregar):

```bash
docker exec "$PG" psql -U postgres -d bridge -c "
  UPDATE bridge_outgoing_queue
  SET expires_at=NOW() - INTERVAL '1 second'
  WHERE id=12345 AND status='pending';"
```

El expirer (cada 30s) lo recogerá y emitirá `outgoing_expired`.

---

## Investigaciones (forensics)

### Investigar un strike

```bash
# 1. Audit log inmutable (toda operación API quedó registrada).
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/audit?instance=whatsapp-main&limit=200" | jq

# 2. Pico de envíos previo.
WEEK_AGO=$(date -u -d '7 days ago' +%Y-%m-%d)
TODAY=$(date -u +%Y-%m-%d)
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/instances/whatsapp-main/usage?from=$WEEK_AGO&to=$TODAY" | jq

# 3. Snapshot actual de ban-risk.
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/ban-risk | jq

# 4. Lifecycle events recientes en Prometheus.
curl -sS http://qrsgen:3100/metrics | grep 'qrsgen_lifecycle_events_total.*strike\|.*ban_risk\|.*spam_blocked'
```

### Encontrar mensajes que expiraron en el outbox

```bash
docker exec postgres psql -U postgres -d bridge -c "
  SELECT id, instance, remote_jid, enqueued_at, expires_at, attempts, last_error,
         payload->>'content' AS preview
  FROM bridge_outgoing_queue
  WHERE status='expired'
  ORDER BY enqueued_at DESC LIMIT 50;"
```

### Auditoría de cambios de `owner_tag`

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/audit?limit=500" \
  | jq '.entries[] | select(.action=="instance.patch" and .metadata.owner_tag_set==true)'
```

---

## Troubleshooting

### Outgoing devuelve `202 queued` cuando se esperaba `200 sent`

**Diagnóstico**: la instancia está disconnected (probablemente
reconectando tras un blip o un restart en curso). El outbox tomó el
mensaje y lo entregará cuando vuelva.

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main | jq '.state'
```

**Si el `state` se mantiene en `disconnected` o `connecting` muchos
minutos**, la sesión probablemente está `logged_out` server-side. Mira el
último `events_webhook_url` por un evento `logged_out`. Solución:
re-parear.

### Mensajes outgoing siguen fallando con "Error al enviar" en el downstream

Causas frecuentes:

1. **Container down**: `docker service ps qrsgen_qrsgen` — ¿está running?
2. **Webhook URL mal configurada**: debe ser
   `http://qrsgen:3100/api/instances/<INSTANCE_NAME>/webhook` (sustituye
   `<INSTANCE_NAME>` por el nombre real).
3. **HMAC mismatch**: si tienes `WEBHOOK_HMAC_SECRET` set, comprueba que
   el orquestador firma con el mismo secret y manda el header
   `X-Qrsgen-Signature`.
4. **Firewall bloquea**: `dmesg | grep QRSGEN-DROP` — paquetes droppeados.
   Si ves drops al downstream, añade su CIDR al allowlist en `firewall.sh`.
5. **Sesión WhatsApp perdida**: `GET /api/instances/<INSTANCE_NAME>` →
   `state: "disconnected"`. Re-parear.

### Outbox queue llena (`503 queue full`)

Una instancia con `MaxQueueDepth=200` lleno significa que está
disconnected hace mucho rato y nadie la rescató. Acción inmediata:

```bash
# Ver cuántos pending tiene
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/outbox

# Si la instancia está logged_out, reparear y/o limpiar la cola:
PG=$(docker ps --format '{{.Names}}' | grep ^postgres)
docker exec "$PG" psql -U postgres -d bridge -c "
  UPDATE bridge_outgoing_queue SET status='expired'
  WHERE instance='whatsapp-main' AND status='pending';"
```

### "Instance X — sin conexión activa"

La instancia se creó pero la sesión nunca se estableció. Razón habitual:
el usuario no escaneó el QR a tiempo (~2 min de pairing). Vuelve a
ejecutar `refresh-qr` y avisa de escanear más rápido.

### Backend reinicia pero los técnicos siguen viendo "Conexión perdida"

El grace period de Disconnected (60 s para `unreachable` + 2 min para
`disconnected`) probablemente se está agotando. Verifica:

- `journalctl -u qrsgen-firewall.service` — ¿hay drops?
- `docker service logs qrsgen_qrsgen --tail 100 | grep -E "connected|disconnected"`
  — ¿reconecta cada instancia?
- Si una NO reconecta: probablemente `logged_out` desde el servidor de
  WhatsApp. Necesita nuevo QR.

### "Error al enviar" en mensajes posteados como nota privada

Esto NO es problema de qrsgen. Es el downstream intentando despachar un
msg `private:true` como outgoing al webhook del inbox. qrsgen lo rechaza
correctamente vía la safety net. El error visible en la UI es **cosmético**
— el mensaje sí está registrado en el downstream como nota interna.

### Ban-risk `level: high` en una instancia

Tu volumen está cerca del threshold. **Reduce ritmo de envíos**:

- Pausa workflows masivos.
- Espacia los outgoings (al menos 2-3 s entre mensajes a JIDs nuevos).
- Mira `alerts` en el snapshot: si dice `high_velocity` es ritmo,
  `burst_outreach` es muchos JIDs nuevos en poco tiempo, `low_delivery`
  es que WhatsApp ya está rechazando.

Cuando se aclare (5-10 min sin nuevos triggers), el level baja
automáticamente.

---

## Métricas para alerting

Sugerencias de regla Prometheus:

```promql
# Instancias activas debajo del esperado
qrsgen_active_instances < 4

# Tasa alta de errores
rate(qrsgen_message_dispatch_errors_total[5m]) > 0.1

# Spamguard activo (alguien está duplicando)
increase(qrsgen_spamguard_blocks_total[5m]) > 5

# Strike de WhatsApp (¡acción inmediata!)
increase(qrsgen_lifecycle_events_total{event="strike"}[1h]) > 0

# Ban risk alto sostenido
increase(qrsgen_lifecycle_events_total{event="ban_risk"}[10m]) > 0

# Outbox creciendo (instancia probablemente caída)
increase(qrsgen_lifecycle_events_total{event="outgoing_expired"}[1h]) > 0
```

---

## Logs útiles

```bash
# qrsgen (slog JSON estructurado en stdout)
docker service logs qrsgen_qrsgen --since 10m | grep -E "WARN|ERROR"

# Filtrar por instancia concreta
docker service logs qrsgen_qrsgen --since 1h | grep "instance=whatsapp-main"

# Eventos lifecycle emitidos
docker service logs qrsgen_qrsgen --since 1h | grep "events webhook sent"

# Outbox enqueues
docker service logs qrsgen_qrsgen --since 1h | grep "outbox enqueued"

# Banwatch alerts
docker service logs qrsgen_qrsgen --since 1h | grep "banwatch"

# Firewall watcher
journalctl -u qrsgen-firewall.service --since "10 min ago"
tail -50 /var/log/qrsgen-firewall.log

# Backups
journalctl -u qrsgen-postgres-backup.service --since "1 day ago"
```
