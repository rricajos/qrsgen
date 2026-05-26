# Salir de qrsgen

> 👋 **Honest dealing**: si decides usar otra plataforma, documentamos
> aquí cómo hacerlo con el mismo cuidado que documentamos la entrada.
> No hay lock-in técnico ni penalizaciones contractuales.

Las razones legítimas para migrar fuera de qrsgen pueden ser:

- Quieres un SaaS managed para no gestionar VPS.
- Volumen tan bajo que self-host no compensa.
- Tu equipo prefiere Node/Python y embeber Baileys / whatsapp-web.js.
- Has decidido pasar a WhatsApp Cloud API oficial (recomendado para
  uso comercial intensivo regulado).

## Lo que es portable hacia fuera

| Dato | Cómo obtenerlo |
|---|---|
| Lista de instancias + config | `GET /api/instances` (autenticado) + `GET /api/instances/:name` por cada una |
| Audit log | `GET /api/audit?limit=500` (puedes paginar exportando todo) |
| Usage histórico | `GET /api/usage` (rango de fechas) + `GET /api/usage/summary` |
| Schema Postgres (todo) | `pg_dump -Fc -d bridge` desde el backup runbook |
| `owner_tag` mapping | Incluido en `GET /api/instances/:name` |
| Webhook URLs configurados | Campo `events_webhook_url` por instancia |

## Lo que NO es portable

- **Sesiones WhatsApp**: las claves criptográficas en `whatsmeow_*` son
  específicas del formato whatsmeow. Cambiar a Baileys / wajs / Cloud
  API obliga a **re-escanear todos los QRs**. Es una limitación del
  protocolo, no de qrsgen.

## Receta

### 1. Exportar config actual

```bash
TOK="$QRSGEN_API_TOKEN"
BASE="http://qrsgen:3100"

# Listado de instancias con shape rich
curl -sS -H "Authorization: Bearer $TOK" $BASE/api/instances \
  | jq '.[].name' -r | while read name; do
      curl -sS -H "Authorization: Bearer $TOK" "$BASE/api/instances/$name"
      echo
  done > /tmp/qrsgen-instances-full.json
```

O usa el script en `tools/migrate/export-config.py`:

```bash
QRSGEN_URL=$BASE QRSGEN_TOKEN=$TOK \
  python3 tools/migrate/export-config.py > /tmp/qrsgen-export.json
```

### 2. Exportar audit log

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  "$BASE/api/audit?limit=500" | jq > /tmp/qrsgen-audit.json
```

### 3. Exportar usage

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  "$BASE/api/usage?from=2024-01-01&to=$(date +%Y-%m-%d)" | jq > /tmp/qrsgen-usage.json
```

### 4. Dump completo de Postgres

```bash
sudo systemctl start qrsgen-postgres-backup.service
ls -lh /opt/qrsgen-stack/backups/daily/
# El dump más reciente lo tienes
```

Este dump incluye:
- `bridge_instance` (config)
- `bridge_dedup` (idempotencia)
- `bridge_usage_daily` (counters)
- `bridge_outgoing_queue` (mensajes pendientes — si hay)
- `bridge_audit_log` (forensics)
- `whatsmeow_*` (sesiones, no portables fuera de whatsmeow pero
  archivar por si vuelves)

### 5. Apuntar el downstream al nuevo sistema

Lo que hace tu downstream (n8n, app custom) hoy:

```
POST http://qrsgen:3100/api/instances/whatsapp-main/webhook
```

Lo cambias a:

```
POST <NUEVO_BACKEND>/...
```

Cada nueva plataforma tendrá su URL/format. Adapta tu nodo
HTTP Request o tu cliente HTTP.

### 6. Re-pairing en el destino

Los usuarios escanean nuevamente en la nueva plataforma. Mantén
qrsgen corriendo hasta que la mayoría haya migrado, igual que en una
migración entrante.

### 7. Apagar qrsgen

Cuando todo el tráfico esté en el nuevo sistema:

```bash
docker stack rm qrsgen
```

**Conserva el Postgres `bridge`** unas semanas más por si necesitas
consultar audit o usage retrospectivo. Si vas a apagar también
Postgres:

```bash
# Backup off-site final antes de borrar
sudo systemctl start qrsgen-postgres-backup.service
scp /opt/qrsgen-stack/backups/daily/*.dump archive@backup-host:/qrsgen-archive/
```

## Recomendaciones por destino

### → WhatsApp Cloud API oficial

La mejor opción si tu volumen y presupuesto lo aguantan. Sin riesgo
de ban, soporte oficial, SLA. Coste: ~$0.005-0.08 por conversación
según país.

- Migra primero las instancias críticas (el reescaneo ahí lo hace el
  business owner).
- Mantén qrsgen para los casos donde Cloud API es prohibitivo
  (testing, internal, pequeños).

### → Baileys / whatsapp-web.js embebido

Si quieres meter todo en una app Node y simplificar stack:

- Aceptas perder outbox, audit log, BanWatcher.
- Ganas: menos componentes, sin proceso separado.

### → Otro SaaS (Whapi.cloud, MaytAPI)

Si decides outsourcing total:

- Re-pairings completos.
- Pierdes control sobre dónde viven los datos.
- Ganas: cero ops a tu cargo.

## Glosario

**Lock-in**: situación donde cambiar de plataforma es prohibitivamente
caro o destructivo. qrsgen lo evita exponiendo API REST estándar +
audit log abierto + scripts de export.

**Honest dealing**: documentar la salida con el mismo cuidado que la
entrada. Genera confianza B2B porque demuestra que no juegas con la
inercia del cliente.

**Migration window**: período acordado para ejecutar el switchover.
Típicamente fin de semana o noche de menor tráfico.

**Switchover**: el momento donde el tráfico productivo se mueve del
sistema antiguo al nuevo. Gradual (per-instance) o big-bang (todos a
la vez).

**Forensics post-migración**: tras el cambio, dejar el sistema
antiguo en read-only por unas semanas para poder investigar
incidentes que afecten datos del periodo anterior.

**Cloud API (oficial)**: API de WhatsApp Business mantenida por Meta.
Con SLA, sin riesgo de ban, de pago. La alternativa "premium" a qrsgen
y otros wrappers no oficiales.
