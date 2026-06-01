# Runbook: instancia atascada

Procedimiento para una instancia que está marcada como `disconnected`
o `connecting` durante muchos minutos y no recupera por sí sola.

## Síntomas

- `GET /api/instances/:name` devuelve `state=disconnected` o
  `state=connecting` durante > 5 min.
- Mensajes outgoing acumulándose en el outbox (`outbox_pending > 0`
  en `/api/health`).
- Pill rojo "QR Conexión perdida" o "QR Desconectado" en la conv
  `QR - X` de Chatwoot.

## Diagnóstico

### 1. Verificar el state actual

```bash
TOK=$(sudo docker service inspect qrsgen_qrsgen \
  --format '{{range .Spec.TaskTemplate.ContainerSpec.Env}}{{println .}}{{end}}' \
  | grep QRSGEN_API_TOKEN | cut -d= -f2-)

curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/INSTANCE_NAME | jq
```

Mira `state`, `jid`, `paired_at`, `last_event_at`. Si `jid=""` y
`paired_at=null`, la sesión está cerrada server-side — salta a la
sección "Re-pareo".

### 2. Mirar logs del container

```bash
sudo docker service logs qrsgen_qrsgen --since 10m 2>&1 \
  | grep -i INSTANCE_NAME | tail -50
```

Busca patrones:

- **`logged out — session invalidated`**: la sesión fue cerrada desde
  WhatsApp servidor. Necesita re-pareo (ver siguiente sección).
- **`disconnected from whatsapp`** seguido de reintentos sin éxito:
  problema de red overlay; reiniciar el container puede ayudar.
- **`temporary ban from WhatsApp`**: cuenta marcada como spam por
  WhatsApp. Esperar el tiempo indicado en `expires_in`; mientras tanto,
  bajar la frecuencia de mensajes salientes.

### 3. Verificar conectividad

```bash
sudo docker exec $(sudo docker ps -q -f name=qrsgen_qrsgen | head -1) \
  ping -c 3 mmg.whatsapp.net 2>/dev/null \
  || echo "container no tiene ping; intenta curl al server WA"
```

## Soluciones por escenario

### A) Logged-out (sesión cerrada en WA server)

WhatsApp cerró la sesión externamente (típicamente porque el usuario
hizo "Cerrar sesión" desde su móvil en Configuración > Dispositivos
vinculados, o porque el bot lleva muchos días sin conectar).

**Solución**: re-parear desde Chatwoot.

1. En Chatwoot, abre la conv `QR - INSTANCE_NAME`.
2. Escribe `qr` en la conv (esto dispara
   `OMNIA_QR_CHAT` que llama a `POST /api/instances/.../refresh-qr`).
3. El QR PNG aparece en la conv. Escanéalo desde el móvil
   (WhatsApp > Dispositivos vinculados > Vincular un dispositivo).
4. Espera 5-10s; el pill cambia a "🟢 QR Conectado".

Si el QR no aparece tras 30s, revisar logs del LIFECYCLE notifier
en n8n.

### B) Reconnect loop sin progreso

La instancia oscila entre `disconnected` y `connecting` sin estabilizar.

**Causas comunes**:

1. **Sesión expirada parcialmente**: WhatsApp pide re-auth y whatsmeow
   reintenta sin notificar. Solución: `POST /logout` + re-parear.

   ```bash
   curl -sS -X POST -H "Authorization: Bearer $TOK" \
     http://qrsgen:3100/api/instances/INSTANCE_NAME/logout
   ```

2. **Container atascado en estado intermedio**: rare pero posible tras
   un OOM o un kill -9. Solución: forzar restart del task.

   ```bash
   sudo docker service update --force qrsgen_qrsgen
   ```

3. **DNS overlay corrompido**: el container no resuelve `postgres` o
   `pgvector`. Solución: restart del task como arriba; si persiste,
   restart del daemon docker.

### C) Temporary ban

WhatsApp envió `*events.TemporaryBan` (ver logs:
`temporary ban from WhatsApp ... expires_in=24h0m0s`).

**Acción**:
1. NO reintentar conectar — empeora el ban.
2. Comunicar al técnico humano que pare envíos masivos.
3. Esperar a que el `expires_in` venza.
4. Reanudar gradualmente: bajar
   `QRSGEN_OUTGOING_RATE_LIMIT` si está alto.

## Prevención

- **Health-check externo**: monitorea `GET /api/health` desde tu
  observability stack. Alerta si `instances_connected < instances_total`
  durante > 5 min.
- **Lifecycle webhooks**: configura `events_webhook_url` por instancia
  para recibir transitions; n8n los procesa y postea pills a Chatwoot
  (ver `QRSGEN_LIFECYCLE_NOTIFIER` workflow).
- **Spamguard**: dejar habilitado (`QRSGEN_SPAMGUARD_ENABLED=true`)
  para evitar dispatch de duplicados que pueden disparar bans
  temporales.

## Escalado

Si tras los pasos anteriores la instancia sigue down:

1. Tag el incident en el canal de soporte.
2. Bajar la imagen al tag anterior si el problema empezó tras un
   deploy reciente:

   ```bash
   sudo docker service update --image qrsgen:0.53.3 qrsgen_qrsgen
   ```

3. Capturar logs completos:

   ```bash
   sudo docker service logs qrsgen_qrsgen --since 1h > /tmp/incident-$(date +%s).log
   ```

4. Abrir issue en GitHub con el log + output de
   `/api/instances/:name` + `/api/health`.

---

Última actualización: v0.63.0 (2026-06-01)
