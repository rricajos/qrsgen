# Procedimientos comunes

## Re-pareado de un técnico (sesión perdida)

1. El integrador (n8n / app custom) pide al usuario
   `POST /api/instances/<INSTANCE_NAME>` desde el chat ops del contacto.
2. Si la instancia ya existe pero está `disconnected`/`logged_out`,
   qrsgen regenera el QR.
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

## Borrar una instancia completamente

```bash
# 1. Desactiva sesión en WhatsApp servers
curl -X POST -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/logout

# 2. Borra config + state
curl -X DELETE -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main
```

El DELETE queda registrado en `bridge_audit_log`
(`action: instance.delete`).

> ⚠️ El DELETE **no** borra las tablas `whatsmeow_*` asociadas. Si
> quieres liberar también las keys, ejecuta manualmente sobre Postgres
> tras el DELETE.

## Restart del backend

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
en el update_config — diseñado así para evitar JID conflicts).
**Mensajes outgoing que llegan durante esa ventana se encolan en el
outbox y se entregan al volver**.

## Asignar `owner_tag` a una instancia (multi-tenant)

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

## Generar reporte mensual de uso para billing

```bash
THIS_MONTH=$(date -u +%Y-%m)
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/usage/summary?from=$THIS_MONTH&to=$THIS_MONTH" | jq
```

Devuelve filas agrupadas por `(owner_tag, mes)`. Mapeas tu modelo de
tenant al `owner_tag` y aplicas tu pricing.

## Cambiar el `inbox_id` de una instancia

```bash
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"inbox_id":90}' \
  http://qrsgen:3100/api/instances/whatsapp-sales
```

## Toggle spamguard

```bash
# Activar
curl -X PATCH -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"spamguard_enabled":true}' \
  http://qrsgen:3100/api/instances/whatsapp-sales
```

## Drenar el outbox manualmente (no recomendado)

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

## Glosario

**Re-pareado**: regenerar el QR para que un usuario vuelva a escanear y
reactive la sesión. Necesario tras un `logged_out` server-side.

**Refresh QR**: forzar la regeneración del QR sin esperar al ciclo
natural de 20s. Útil cuando el QR mostrado caducó.

**Logout server-side**: invalidación de la sesión en los servidores de
Meta. Tras esto, la sesión no se puede recuperar — hay que escanear un
QR nuevo. Distinto del DELETE de qrsgen (que borra la fila pero no
toca Meta).

**Restart del backend**: redeploy del container qrsgen (ej.
`docker service update --force`). Provoca downtime de ~10-15s; el
outbox lo cubre.

**Backend restarting → backend started**: secuencia de eventos
lifecycle que qrsgen emite al SIGTERM y al boot. Útiles para
visualizar el cycle en el panel del agente.

**owner_tag**: string libre para mapear instancias a tenants/clientes.
Asignable vía `PATCH /api/instances/:name`. Aparece en
`/api/usage/summary` agrupado.

**Billing report**: agregado mensual del usage por `(owner_tag, mes)`.
Útil para facturación al cierre de mes.

**Spamguard toggle**: encender/apagar el filtro de duplicados outgoing
vía `PATCH spamguard_enabled`. Aplicable per-instancia.

**Drainer manual**: forzar el procesamiento del outbox para una
instancia. Normalmente innecesario — el drainer goroutine corre cada 5s.
