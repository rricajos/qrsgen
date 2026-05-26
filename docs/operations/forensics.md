# Investigaciones (forensics)

## Investigar un strike

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

## Encontrar mensajes que expiraron en el outbox

```bash
docker exec postgres psql -U postgres -d bridge -c "
  SELECT id, instance, remote_jid, enqueued_at, expires_at, attempts, last_error,
         payload->>'content' AS preview
  FROM bridge_outgoing_queue
  WHERE status='expired'
  ORDER BY enqueued_at DESC LIMIT 50;"
```

## Auditoría de cambios de `owner_tag`

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/audit?limit=500" \
  | jq '.entries[] | select(.action=="instance.patch" and .metadata.owner_tag_set==true)'
```

## Glosario

**Forensics**: investigación post-incidente. Combina audit log
(cronología de cambios), usage (volúmenes), ban-risk (estado actual)
para reconstruir qué pasó.

**Pico de envíos**: incremento brusco en el counter `messages_out`
durante una ventana corta. Indicador típico de antes de un strike.

**Audit query**: consulta al endpoint `/api/audit` para inspeccionar
operaciones pasadas. Filtrable por instancia y limitable.

**Outbox expirado**: mensaje en el outbox que no se entregó antes del
TTL (5 min). qrsgen lo marca como `expired` y emite el evento
lifecycle.

**Strike investigation**: protocolo para entender qué causó una
sanción WhatsApp. Empieza por audit + usage en las horas previas.

**Cadena de causalidad**: secuencia de eventos que llevó a un
incidente. El audit log da la cronología; el usage da los volúmenes;
el ban-risk da el indicador previo.
