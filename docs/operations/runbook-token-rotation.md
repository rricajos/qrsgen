# Runbook: rotación de credenciales

Procedimientos para rotar las credenciales sensibles de qrsgen sin
downtime de las instancias. Cubre los 3 tokens críticos:

| Token | Qué autoriza | Frecuencia recomendada |
|---|---|---|
| `QRSGEN_API_TOKEN` | Acceso HTTP al qrsgen (Bearer) | 90 días |
| `DOWNSTREAM_API_TOKEN` | Calls de qrsgen al Chatwoot API | 90 días o tras incidente |
| `CHATWOOT_DB_URL` password | Backdate worker → Chatwoot DB | 180 días |
| `WEBHOOK_HMAC_SECRET` | Validación firma de webhooks downstream | 90 días o tras incidente |

> El password de Postgres del bridge (`POSTGRES_PASSWORD`) NO se cubre
> aquí — rotarlo requiere reiniciar todas las apps que tocan la DB
> (qrsgen + n8n si la usa). Procedimiento aparte.

## 1. Rotar `QRSGEN_API_TOKEN`

Es el Bearer token que usan integradores (n8n, scripts ops) para
hablar con qrsgen. Rotarlo requiere coordinar con los consumidores
porque el cambio rompe los calls en curso.

### Procedure (con downtime cero usando 2 tokens válidos)

qrsgen actualmente acepta un único token. Para rotar sin downtime
necesitas un grace period donde ambos tokens funcionen. Como qrsgen
sólo admite 1, el approach es:

1. **Genera el nuevo token**:

   ```bash
   NEW_TOKEN=$(openssl rand -base64 32 | tr -d '/+' | head -c 43)
   ```

2. **Comunica el cambio a los consumidores** (n8n, scripts) con
   ventana de 5 min.

3. **Actualiza qrsgen con el nuevo token**:

   ```bash
   sudo docker service update --env-add "QRSGEN_API_TOKEN=$NEW_TOKEN" qrsgen_qrsgen
   ```

   Esto dispara rolling update con stop-first (< 5s downtime).

4. **Actualiza cada consumidor**:
   - n8n: edita las credentials de "qrsgen API" en cada workflow.
   - Scripts ops: actualiza el `.env`/secrets que los disparan.

5. **Verifica con un health check**:

   ```bash
   curl -sS -H "Authorization: Bearer $NEW_TOKEN" \
     http://qrsgen:3100/api/health | jq '.status'
   # → "ok"
   ```

6. **Invalida el token viejo en tu password manager** (1Password,
   Bitwarden, etc.).

### Recovery si pierdes ambos tokens

El token viene de la env var del service. Si lo pierdes:

```bash
sudo docker service inspect qrsgen_qrsgen \
  --format '{{range .Spec.TaskTemplate.ContainerSpec.Env}}{{println .}}{{end}}' \
  | grep QRSGEN_API_TOKEN
```

(Asume acceso root al host swarm.)

## 2. Rotar `DOWNSTREAM_API_TOKEN`

Es el token que qrsgen presenta a Chatwoot en cada call. Vive en
dos sitios: la env var del service qrsgen + la config del usuario
admin en Chatwoot que lo emitió.

### Procedure

1. **En Chatwoot UI**, como agente admin:
   - `Profile Settings → Access Token`
   - Click "Generate new token" (o "Reset")
   - Copia el nuevo token

2. **Actualiza el qrsgen service**:

   ```bash
   sudo docker service update --env-add "DOWNSTREAM_API_TOKEN=$NEW_TOK" qrsgen_qrsgen
   ```

3. **Si usas multi-tenant**, además actualiza cada tenant que tenga
   override:

   ```bash
   curl -X PATCH \
     -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
     -H "Content-Type: application/json" \
     -d "{\"downstream_api_token\":\"$NEW_TOK\"}" \
     http://qrsgen:3100/api/tenants/CLIENT_TAG
   ```

4. **Verifica**: qrsgen debería poder seguir posteando msgs incoming.

   ```bash
   # En el móvil, envía un msg a una de las instancias
   # En Chatwoot, verifica que aparece en la conv correspondiente
   ```

5. **Revoca el token viejo en Chatwoot** (Profile Settings → mismo
   sitio donde lo regeneraste; o si no se puede revoke, déjalo
   inutilizado).

## 3. Rotar password de `CHATWOOT_DB_URL`

Necesario si el password de Postgres de Chatwoot cambia (incidente
o rotación periódica). El backdate worker queda sin servicio mientras.

### Procedure

1. **Cambia el password en la DB de Chatwoot** (lado Chatwoot ops).

2. **Construye la nueva DSN**:

   ```
   postgres://postgres:NEW_PASS@pgvector:5432/chatwoot?sslmode=disable
   ```

3. **Actualiza qrsgen**:

   ```bash
   sudo docker service update \
     --env-add "CHATWOOT_DB_URL=postgres://postgres:NEW_PASS@pgvector:5432/chatwoot?sslmode=disable" \
     qrsgen_qrsgen
   ```

4. **Verifica en logs**:

   ```bash
   sudo docker service logs qrsgen_qrsgen --since 1m | grep backdate
   # → backdate worker started interval=30s ...
   ```

5. Si el backdate worker reporta errores de conexión durante varios
   ticks, es probable que el DSN esté mal — revisa con `psql` desde
   un container del overlay.

### Disable temporal del backdate worker

Si necesitas parar el backdate worker (e.g. durante una migración
de Chatwoot DB), simplemente quita la env:

```bash
sudo docker service update --env-rm CHATWOOT_DB_URL qrsgen_qrsgen
```

qrsgen arrancará con el log `backdate worker disabled (CHATWOOT_DB_URL
not set)`. Re-añade la env cuando esté listo.

## 4. Rotar `WEBHOOK_HMAC_SECRET`

Si el HMAC secret se compromete o lo rotas periódicamente. Requiere
coordinar con Chatwoot porque ambos lados firman/validan.

### Procedure (global secret)

1. **Genera el nuevo secret**:

   ```bash
   NEW_SECRET=$(openssl rand -hex 32)
   ```

2. **Actualiza qrsgen**:

   ```bash
   sudo docker service update --env-add "WEBHOOK_HMAC_SECRET=$NEW_SECRET" qrsgen_qrsgen
   ```

3. **Actualiza Chatwoot** — en la config del api_channel inbox,
   sección "Webhook Settings", paste el nuevo HMAC secret.

4. **Verifica**: dispara un msg de prueba desde Chatwoot. qrsgen debe
   loguear "webhook hmac: signature mismatch" si Chatwoot todavía
   tiene el viejo, o procesar limpiamente si ambos están en sync.

### Procedure (per-tenant secret)

Si usas multi-tenant con HMAC override per cliente:

```bash
curl -X PATCH \
  -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  -d "{\"webhook_hmac_secret\":\"$NEW_SECRET\"}" \
  http://qrsgen:3100/api/tenants/CLIENT_TAG
```

El cambio aplica en el próximo webhook (cache TTL ~30s).

## Audit trail

Todas las operaciones de rotación generan rows en `bridge_audit_log`
(via el handler `audit.Record`). Para verificar quién rotó qué:

```sql
SELECT ts, actor, action, target, metadata
FROM bridge_audit_log
WHERE action LIKE 'tenant.%' OR action LIKE 'instance.%'
ORDER BY ts DESC
LIMIT 50;
```

Los rotations de env vars del service NO quedan en audit log (son
acciones del host swarm, no de qrsgen). Para esos, revisa los logs
de docker service.

## Cadencia recomendada

- **Semestralmente** (cada 6 meses): rotar todos los secrets como
  baseline.
- **Tras incidente** (PAT leaked, repo público accidental, etc):
  rotar inmediatamente el credential afectado + audit el log para
  detectar usos sospechosos durante la ventana.
- **Cuando deja un dev** con acceso ops: rotar `QRSGEN_API_TOKEN` y
  cualquier credencial que pudiera haber visto.

---

Última actualización: v0.64.6 (2026-06-01)
