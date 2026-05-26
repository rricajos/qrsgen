# Troubleshooting

## Outgoing devuelve `202 queued` cuando se esperaba `200 sent`

**Diagnóstico**: la instancia está disconnected (probablemente
reconectando tras un blip o un restart en curso). El outbox tomó el
mensaje y lo entregará cuando vuelva.

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main | jq '.state'
```

**Si el `state` se mantiene en `disconnected` o `connecting` muchos
minutos**, la sesión probablemente está `logged_out` server-side. Mira
el último `events_webhook_url` por un evento `logged_out`. Solución:
re-parear.

## Mensajes outgoing siguen fallando con "Error al enviar" en el downstream

Causas frecuentes:

1. **Container down**: `docker service ps qrsgen_qrsgen` — ¿está running?
2. **Webhook URL mal configurada**: debe ser
   `http://qrsgen:3100/api/instances/<INSTANCE_NAME>/webhook` (sustituye
   `<INSTANCE_NAME>` por el nombre real).
3. **HMAC mismatch**: si tienes `WEBHOOK_HMAC_SECRET` set, comprueba que
   el orquestador firma con el mismo secret y manda el header
   `X-Qrsgen-Signature`.
4. **Firewall bloquea**: `dmesg | grep QRSGEN-DROP` — paquetes
   droppeados. Si ves drops al downstream, añade su CIDR al allowlist en
   `firewall.sh`.
5. **Sesión WhatsApp perdida**: `GET /api/instances/<INSTANCE_NAME>` →
   `state: "disconnected"`. Re-parear.

## Outbox queue llena (`503 queue full`)

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

## "Instance X — sin conexión activa"

La instancia se creó pero la sesión nunca se estableció. Razón habitual:
el usuario no escaneó el QR a tiempo (~2 min de pairing). Vuelve a
ejecutar `refresh-qr` y avisa de escanear más rápido.

## Backend reinicia pero los técnicos siguen viendo "Conexión perdida"

El grace period de Disconnected (60 s para `unreachable` + 2 min para
`disconnected`) probablemente se está agotando. Verifica:

- `journalctl -u qrsgen-firewall.service` — ¿hay drops?
- `docker service logs qrsgen_qrsgen --tail 100 | grep -E "connected|disconnected"`
  — ¿reconecta cada instancia?
- Si una NO reconecta: probablemente `logged_out` desde el servidor de
  WhatsApp. Necesita nuevo QR.

## "Error al enviar" en mensajes posteados como nota privada

Esto NO es problema de qrsgen. Es el downstream intentando despachar un
msg `private:true` como outgoing al webhook del inbox. qrsgen lo rechaza
correctamente vía la safety net. El error visible en la UI es
**cosmético** — el mensaje sí está registrado en el downstream como
nota interna.

## Ban-risk `level: high` en una instancia

Tu volumen está cerca del threshold. **Reduce ritmo de envíos**:

- Pausa workflows masivos.
- Espacia los outgoings (al menos 2-3 s entre mensajes a JIDs nuevos).
- Mira `alerts` en el snapshot: si dice `high_velocity` es ritmo,
  `burst_outreach` es muchos JIDs nuevos en poco tiempo, `low_delivery`
  es que WhatsApp ya está rechazando.

Cuando se aclare (5-10 min sin nuevos triggers), el level baja
automáticamente.
