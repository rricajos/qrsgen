# Operations runbook

## Diagnóstico rápido

### "¿Está vivo qrsgen?"

```bash
docker service ps qrsgen_qrsgen
docker exec n8n_n8n_editor.X node -e "fetch('http://qrsgen:3100/api/health').then(r=>r.json()).then(console.log)"
```

### "¿Cuántas instancias están conectadas?"

```bash
docker exec n8n_n8n_editor.X node -e "
const TOK='...';
fetch('http://qrsgen:3100/api/instances',{headers:{'Authorization':'Bearer '+TOK}})
  .then(r=>r.json()).then(d=>d.forEach(i=>console.log(i.name,i.state,i.jid||'')))"
```

### "¿Una instancia específica está OK?"

```bash
curl -H "Authorization: Bearer $TOK" http://qrsgen:3100/api/instances/SAT-ALBERT
```

### "¿Cuántos mensajes han pasado hoy?"

`GET /metrics` → contadores `qrsgen_messages_total`. Diff entre snapshots.

### "¿Está bloqueando spam?"

```
qrsgen_spamguard_blocks_total{instance="SAT-ALBERT"}
```

## Procedimientos comunes

### Re-pareado de un técnico (sesión perdida)

1. n8n: pide al usuario ``POST /api/instances/SAT-XXX` (vía tu downstream o directamente con curl)` desde el chat con el contacto QR-X.
2. Si el inbox y contacto ya existen, el sub-workflow downstream refresca metadata y muestra el QR si la sesión se ha caído.
3. Si la sesión está perdida (logged_out), qrsgen emitirá `qr_generated` cada ~20s y el notifier postea el PNG renovado en la conv ops.

Manual:

```bash
curl -X POST -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/SAT-XXX/refresh-qr
```

### Borrar una instancia completamente

```bash
# 1. Desactiva sesión en WhatsApp servers
curl -X POST -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/SAT-XXX/logout

# 2. Borra config + state
curl -X DELETE -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/SAT-XXX
```

### Restart del backend

```bash
docker service update --force qrsgen_qrsgen
```

Tras `SIGTERM`, qrsgen:

1. Emite `backend_restarting` a cada panel QR-X (notif visible).
2. Espera 12 segundos de grace para que el downstream termine de procesar webhooks pendientes.
3. Cierra todas las conexiones whatsmeow.
4. Container nuevo arranca, ejecuta `Bootstrap()` que reconecta cada sesión en paralelo.
5. Tras 8s, emite `backend_started` a cada panel.

Downtime efectivo del WebSocket: ~5-10 segundos. Mensajes entrantes durante ese período se entregan cuando whatsmeow reconecta (catch-up via offline messages).

### Cambiar el `inbox_id` de una instancia

```bash
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"inbox_id":90}' \
  http://qrsgen:3100/api/instances/SAT-ALBERT
```

### Toggle spamguard

```bash
# Activar
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"spamguard_enabled":true}' \
  http://qrsgen:3100/api/instances/SAT-ALBERT
```

O desde el downstream: `spamguard on` / `spamguard off` / `spamguard status` en el panel QR-X conv.

## Troubleshooting

### Mensajes outgoing fallan con "Error al enviar" en el downstream

Causas frecuentes:

1. **Container down**: `docker service ps qrsgen_qrsgen` — ¿está running?
2. **Webhook URL mal configurada**: comprueba el inbox del downstream → `webhook_url` debe ser `http://bridge_bridge:3100/api/instances/SAT-XXX/webhook` o `http://qrsgen:3100/...`.
3. **Firewall bloquea**: `dmesg | grep QRSGEN-DROP` — paquetes droppeados. Si ves drops al downstream, añade su CIDR al allowlist en `firewall.sh`.
4. **Sesión WhatsApp perdida**: `GET /api/instances/SAT-XXX` → `state: "disconnected"`. Re-parear.

### "Inbox X — sin conexión activa" en el link de creation path

La instancia se creó pero la sesión nunca se estableció. Razón habitual: el técnico no escaneó el QR a tiempo (~2 minutos de pairing). Vuelve a ejecutar el comando equivalente en tu downstream o re-trigger desde la API qrsgen y escanéa más rápido.


### Backend reinicia pero los técnicos siguen viendo "Conexión perdida"

El grace period de Disconnected (2 minutos) probablemente se está agotando. Verifica:

- `journalctl -u qrsgen-firewall.service` — ¿hay drops?
- `docker service logs qrsgen_qrsgen --tail 100 | grep -E "connected|disconnected"` — ¿reconecta cada instancia?
- Si una NO reconecta: probablemente `logged_out` desde el servidor de WhatsApp. Necesita nuevo QR.

### El healthcheck diario no se ejecutó

Workflow `your-downstream-healthcheck-workflow` (n8n id `<workflow_id>`) corre a las 07:00 UTC. Verifica:

```bash
docker exec n8n_n8n_editor.X node -e "
fetch('http://localhost:5678/api/v1/executions?workflowId=o88mAvEzXNOT9s8n&limit=3',{headers:{'X-N8N-API-KEY':'$N8N_TOKEN'}})
  .then(r=>r.json()).then(d=>d.data.forEach(e=>console.log(e.id,e.status,e.startedAt)))"
```

Si está `inactive`: actívalo en la UI o vía API.

### "Error al enviar" en mensajes posteados como nota privada (no a WhatsApp)

Esto NO es problema de qrsgen. Es el downstream intentando dispatch un msg `private:true` como outgoing al webhook del inbox. qrsgen lo rechaza correctamente vía la safety net (`source_id WAID:`, `private:true`, prefijo `qrsgen-qr-`). El error visible en UI es cosmético — el mensaje sí está registrado en el downstream.

## Métricas para alerting

Sugerencias de alerta Prometheus:

```promql
# Instancias activas debajo del esperado
qrsgen_active_instances < 4

# Tasa alta de errores
rate(qrsgen_message_dispatch_errors_total[5m]) > 0.1

# Spamguard activo (alguien está duplicando)
increase(qrsgen_spamguard_blocks_total[5m]) > 5

# Strike de WhatsApp (¡acción inmediata!)
increase(qrsgen_lifecycle_events_total{event="strike"}[1h]) > 0
```

## Logs útiles

```bash
# qrsgen
docker service logs qrsgen_qrsgen --since 10m | grep -E "WARN|ERROR"

# firewall watcher
journalctl -u qrsgen-firewall.service --since "10 min ago"
tail -50 /var/log/qrsgen-firewall.log
