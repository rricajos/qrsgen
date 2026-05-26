# Seguridad

qrsgen se diseña con **defense-in-depth**: ninguna capa es suficiente por sí
sola, pero la combinación cubre los tres vectores más probables.

## Modelo de amenaza

qrsgen vive en una overlay docker compartida con el downstream y, típicamente,
un orquestador como n8n. Asumimos tres vectores principales:

1. **Compromiso lateral en LAN** — otro container del overlay queda
   vulnerado. ¿Qué puede hacer contra qrsgen?
2. **Compromiso del propio qrsgen** — un atacante gana RCE en el proceso.
   ¿Qué puede exfiltrar o usar como pivote?
3. **MITM en el WebSocket WhatsApp** — un atacante con acceso al host o a
   la red ¿puede interceptar el tráfico hacia Meta?

Las siete capas siguientes mitigan estos vectores en distinta medida.
Cada capa documenta **qué hace**, **cómo configurarla**, **qué mitiga** y
**cómo verificarla**.

---

## Capa 1 — Bearer auth de la API

### Qué hace

Todos los endpoints `/api/*` exigen `Authorization: Bearer
$QRSGEN_API_TOKEN`. Excepciones:

- `GET /api/health` — para liveness/readiness probes.
- `POST /api/instances/:name/webhook` — el downstream rara vez manda
  headers arbitrarios; usa HMAC en otro header (capa 2).
- `GET /metrics` — Prometheus scrape.
- `GET /static/*` — assets públicos.

### Configuración

```yaml
environment:
  QRSGEN_API_TOKEN: "${QRSGEN_API_TOKEN}"
```

Generación del token (32 bytes URL-safe):

```bash
python3 -c "import secrets;print(secrets.token_urlsafe(32))"
```

Si la variable está vacía, qrsgen arranca con auth desactivada (modo dev) y
emite un WARNING al boot.

### Qué mitiga

Vector #1 (compromiso lateral): aunque un container del overlay resuelva
`qrsgen:3100`, sin token no puede crear/borrar instancias, leer QRs, ni
consultar audit / usage.

### Cómo verificarla

```bash
# Sin token → 401
curl -sS -o /dev/null -w "%{http_code}\n" http://qrsgen:3100/api/instances
# 401

# Con token correcto → 200
curl -sS -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances
# 200
```

---

## Capa 2 — HMAC del webhook entrante

### Qué hace

El endpoint `POST /api/instances/:name/webhook` está exento del Bearer
token de la capa 1 (los downstream típicos no firman con auth genérica).
En su lugar, qrsgen acepta una firma HMAC en un header dedicado.

Cuando `WEBHOOK_HMAC_SECRET` está set:

```
X-Qrsgen-Signature: sha256=<hex>

donde <hex> = HMAC-SHA256(WEBHOOK_HMAC_SECRET, raw_body)
```

Mismatches devuelven `401`. Si la env var está vacía, el endpoint queda
abierto en LAN (backward-compat).

### Configuración

```yaml
environment:
  WEBHOOK_HMAC_SECRET: "${WEBHOOK_HMAC_SECRET}"
```

En el downstream, firmar antes de POST:

```javascript
const crypto = require('crypto');
const body = JSON.stringify(payload);
const sig  = 'sha256=' + crypto.createHmac('sha256', secret).update(body).digest('hex');
fetch('http://qrsgen:3100/api/instances/whatsapp-main/webhook', {
  method: 'POST', body,
  headers: { 'Content-Type':'application/json', 'X-Qrsgen-Signature': sig },
});
```

### Qué mitiga

Vector #1 dirigido al webhook: un container LAN que adivine la URL del
endpoint no puede inyectar mensajes outgoing — no tiene el secret. Sin esta
capa, cualquier container del overlay podría POSTear y enviar mensajes
arbitrarios al cliente.

### Cómo verificarla

```bash
# Sin firma → 401
curl -sS -o /dev/null -w "%{http_code}\n" -X POST \
  http://qrsgen:3100/api/instances/whatsapp-main/webhook \
  -H 'Content-Type: application/json' -d '{}'
# 401

# Firma correcta → 200/202
BODY='{"event":"message_created","message_type":"outgoing","content":"hola","conversation":{"id":1,"meta":{"sender":{"identifier":"test@s.whatsapp.net"}}},"id":1}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_HMAC_SECRET" -hex | awk '{print $2}')"
curl -sS -X POST http://qrsgen:3100/api/instances/whatsapp-main/webhook \
  -H 'Content-Type: application/json' \
  -H "X-Qrsgen-Signature: $SIG" \
  -d "$BODY"
```

---

## Capa 3 — Egress firewall

### Qué hace

`firewall.sh` mantiene una cadena `QRSGEN_EGRESS` en iptables que filtra
todo el outbound del container qrsgen. Lo que no esté en la allowlist se
DROPpea con LOG (rate-limited 5/min).

### Allowlist

- Established / related (TCP return traffic).
- DNS Docker (`127.0.0.11:53`).
- Overlay LAN (`10.0.0.0/8`, `172.16.0.0/12`).
- IP pública del VPS (para llamadas vía DNS público al downstream si fuera necesario).
- Meta/Facebook CIDRs en `:443` (14 rangos AS32934).

### Operación

```bash
sudo /opt/qrsgen-stack/firewall.sh apply    # aplica DROP
sudo /opt/qrsgen-stack/firewall.sh log      # solo LOG (testing/dry-run)
sudo /opt/qrsgen-stack/firewall.sh flush    # quita todas las reglas
sudo /opt/qrsgen-stack/firewall.sh status   # muestra reglas + counters
```

El watchdog systemd `qrsgen-firewall.service` re-aplica automáticamente
cuando docker reporta `container start` para qrsgen — necesario porque la
IP del container en `docker_gwbridge` cambia tras restarts.

### Qué mitiga

Vector #2 (RCE en qrsgen): un atacante con shell en el proceso no puede
exfiltrar datos a un C2 arbitrario en internet. Solo Meta y la LAN están
permitidos. Para minar crypto, hacer reverse shell, o subir el dump de DB a
un servicio externo, necesitaría romper también esta capa.

### Cómo verificarla

```bash
# Desde dentro del container, intentar conexión a un IP arbitrario debe fallar.
sudo docker exec qrsgen_qrsgen.X /app/qrsgen -healthcheck   # localhost OK
# Para conexiones outbound bloqueadas, verás drops:
sudo dmesg | grep QRSGEN-DROP | tail -5
```

---

## Capa 4 — TLS WhatsApp

### Qué hace

whatsmeow usa el cliente TCP/TLS estándar de Go. El bundle de CAs de la
imagen distroless valida los certificados de Meta. **MITM pasivo es
imposible** (TLS estricto).

### Qué mitiga

Vector #3 (MITM): un atacante en la red que intente leer el tráfico
qrsgen ↔ Meta solo verá ciphertext.

### Limitaciones

MITM **activo** requeriría:

- Comprometer una CA root del VPS (requiere root del host).
- Forzar al cliente a aceptar un cert arbitrario (whatsmeow no lo permite
  sin patches).

Sin **cert pinning** explícito, un atacante con root del VPS podría
inyectar una CA root y MITM. Pero si tienes root del VPS comprometido, el
MITM del WebSocket es la menor de tus preocupaciones — pueden simplemente
leer la memoria del proceso.

### Mejora futura

Cert pinning en whatsmeow para defender ante CA root compromise (alto
esfuerzo de mantenimiento — los certs de Meta rotan).

### Cómo verificarla

```bash
# Capturar tráfico saliente; debe ser todo TLS hacia *.whatsapp.net
sudo tcpdump -i any -nn 'host whatsapp.net' -c 5
```

---

## Capa 5 — Container hardening

### Qué hace

Tres mecanismos combinados reducen drásticamente la superficie de un RCE:

1. **Imagen distroless** (`gcr.io/distroless/static-debian12:nonroot`) —
   sin shell, sin `apt`/`apk`, sin `curl`/`wget`. Solo el binario qrsgen y
   los certs CA.
2. **Root filesystem read-only** — el binario no escribe a disco; toda la
   persistencia vive en Postgres. Cualquier mutación es indicio de
   compromiso.
3. **Tmpfs en `/tmp`** (64 MB) — único path escribible, en memoria, se
   vacía con cada redeploy.
4. **Usuario `nonroot:nonroot`** — sin capabilities adicionales, no puede
   `chmod`, `chown` ni escalar a root.

### Configuración

```yaml
services:
  qrsgen:
    image: qrsgen:0.23.0-rc1   # distroless en Dockerfile.release
    read_only: true
    volumes:
      - type: tmpfs
        target: /tmp
        tmpfs:
          size: 67108864   # 64 MB
```

### Qué mitiga

Vector #2 (RCE escalation). Un atacante con código en el proceso:

- **No puede instalar herramientas** (rootfs read-only + sin package manager).
- **No puede persistir un implante** (solo /tmp escribible, se vacía con
  cada redeploy, además limitado a 64 MB y volátil).
- **No puede escalar a root** (nonroot user, sin capabilities extra).

### Cómo verificarla

```bash
# Intentar escribir en cualquier path fuera de /tmp debe fallar.
sudo docker exec qrsgen_qrsgen.X sh -c 'echo x > /test' 2>&1 || echo "blocked OK"
# (en distroless ni siquiera hay sh, así que se ve doble protección)

# Inspeccionar metadata del container:
sudo docker inspect $(sudo docker ps -q -f name=qrsgen_qrsgen) \
  --format '{{.HostConfig.ReadonlyRootfs}}'   # debe ser true
```

---

## Capa 6 — Audit log inmutable

### Qué hace

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

### Qué mitiga

Forensics post-incidente: cualquier acción contra la API queda registrada
con timestamp. Para investigar un strike o un mensaje fantasma, vas al
audit log y reconstruyes la cadena.

### Cómo verificarla

```bash
# Listar últimas 5 entradas:
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/audit?limit=5" | jq

# Probar que UPDATE está bloqueado a nivel DB:
docker exec postgres psql -U postgres -d bridge \
  -c "UPDATE bridge_audit_log SET actor='nope' WHERE id=1;"
# ERROR: bridge_audit_log is append-only; UPDATE/DELETE forbidden
```

### Limitaciones

- El audit es **append-only en la tabla**, no signed. Un atacante con
  acceso DBA podría drop the trigger + tamper. Pero si tienes ese nivel
  de compromiso, el sistema está perdido por otras razones.
- Para evidence en juicio se debería **firmar cada entrada** y/o
  shipearla a un syslog inmutable (CloudWatch Logs, Loki con immutable
  retention). Pendiente.

---

## Capa 7 — Backups Postgres

### Qué hace

Un systemd timer (`qrsgen-postgres-backup.timer`) ejecuta `pg_dump -Fc` de
la DB `bridge` cada día a las 03:00 local. Layout en
`/opt/qrsgen-stack/backups/`:

```
daily/    qrsgen-bridge-YYYYMMDD-HHMM.dump     (retención 7 días)
weekly/   qrsgen-bridge-YYYY-WW.dump           (retención 4 semanas, copia los domingos)
```

El timer ejecuta como root (necesita acceso a docker socket). Logs en
`journalctl -u qrsgen-postgres-backup.service`.

### Qué mitiga

Pérdida o corrupción de la DB:

- Disco del VPS dañado.
- DROP TABLE accidental (incluido el audit log — recuerda: el trigger
  bloquea UPDATE/DELETE de **filas**, no la tabla entera).
- Migración fallida que deja la DB en estado inconsistente.

### Cómo verificarla

```bash
# Disparar backup manual:
sudo systemctl start qrsgen-postgres-backup.service
sudo journalctl -u qrsgen-postgres-backup.service -n 20

# Verificar dump:
ls -lh /opt/qrsgen-stack/backups/daily/

# Probar restore en un DB de pruebas (NO en la prod):
docker exec -i postgres pg_restore -l < latest.dump | head -20
```

Runbook de restore completo: [`ops/backup/README.md`](https://github.com/rricajos/qrsgen/blob/main/ops/backup/README.md).

### Limitación

Los backups están **en el mismo VPS**. Si el VPS se quema, se pierden.
Para producción crítica, configura un `ExecStartPost=` en el `.service`
que pushee el dump a S3/Backblaze/Wasabi.

---

## Credenciales en orquestadores externos

Los workflows del orquestador (n8n u otros) deben usar el **mecanismo de
credenciales propio** del orquestador, no hardcodear tokens en el JSON.

En n8n:

- Downstream API → `httpHeaderAuth` con header `api_access_token`.
- qrsgen API → `httpHeaderAuth` con header `Authorization: Bearer ...`.

Los credentials se guardan encriptados en la DB de n8n. `n8n export:workflow`
incluye solo los IDs de credentials, no los valores.

→ Mitiga el riesgo de filtrar secretos al hacer screenshot/export de
workflows.

---

## Observabilidad y logs

| Fuente | Cómo leerlo |
|---|---|
| qrsgen — slog JSON estructurado | `docker service logs qrsgen_qrsgen` |
| Firewall — apply/flush | `journalctl -u qrsgen-firewall.service` + `/var/log/qrsgen-firewall.log` |
| Paquetes droppeados | `dmesg \| grep QRSGEN-DROP` (rate-limited 5/min) |
| Errores de despacho | Prometheus `qrsgen_message_dispatch_errors_total{kind,direction,instance}` |
| Operaciones de la API | `bridge_audit_log` (capa 6) |

Para alerting Prometheus básico:

```promql
# Strikes (acción inmediata)
increase(qrsgen_lifecycle_events_total{event="strike"}[1h]) > 0

# Tasa alta de errores
rate(qrsgen_message_dispatch_errors_total[5m]) > 0.1

# Outbox creciendo (instancia probablemente desconectada largo rato)
qrsgen_outgoing_queue_depth > 50
```

---

## Mejoras pendientes

| Mejora | Vector que cubre | Esfuerzo |
|---|---|---|
| Backups off-site (S3/Backblaze) | Pérdida total del VPS | Bajo |
| Audit entries firmadas (HMAC por fila) | Forensics ante DBA compromise | Medio |
| Cert pinning whatsmeow | Vector #3 con CA root compromise | Alto |
| Rate-limiting de `/api/*` | Vector #1 abuso intra-LAN | Bajo |
| eBPF observability (Falco / Tetragon) | Detección de comportamiento anómalo | Alto |
| Multi-downstream con HMAC por tenant | Aislamiento entre clientes (multi-tenant real) | Medio |
