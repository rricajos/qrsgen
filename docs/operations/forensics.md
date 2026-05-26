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
