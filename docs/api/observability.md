# Observabilidad

Endpoints para consultar estado, métricas, billing y forensics.

## `GET /api/instances/:name/outbox`

Stats del buffer outgoing por instancia.

```json
{"pending":0,"sent":127,"expired":1,"failed":0}
```

Cuenta sobre todas las filas históricas de `bridge_outgoing_queue` para
esa instancia. `pending` es el indicador que importa para alerting en
caliente.

---

## `GET /api/instances/:name/ban-risk`

Snapshot del detector proactivo de WhatsApp ban-risk (ver
[arquitectura](../architecture/banwatch.md) para detalle de las tres señales).

```json
{
  "instance": "whatsapp-main",
  "velocity_msgs_per_window": 12, "velocity_threshold": 30,
  "velocity_window_ns": 60000000000,
  "diversity_unique_jids": 8, "diversity_threshold": 20,
  "diversity_window_ns": 300000000000,
  "delivery_ratio": 0.97, "delivery_samples": 30,
  "delivery_threshold": 0.7, "delivery_min_samples": 10,
  "delivery_window_ns": 600000000000,
  "alerts": [],
  "score": 0.13,
  "level": "low"
}
```

`level`: `ok` | `low` | `moderate` | `high`. Cuando un signal cruza su
threshold (`alerts` no vacío), qrsgen emite el evento lifecycle
`ban_risk` en rising-edge (una sola vez hasta que se limpie).

---

## `GET /api/instances/:name/usage?from=YYYY-MM-DD&to=YYYY-MM-DD`

Counters diarios en UTC para una instancia. Default: últimos 30 días.

```json
{
  "instance": "whatsapp-main",
  "from": "2026-04-26",
  "to":   "2026-05-26",
  "rows": [
    {"instance":"whatsapp-main","day":"2026-05-26",
     "messages_in":24,"messages_out":31,
     "spamguard_blocks":0,"lifecycle_events":2}
  ]
}
```

---

## `GET /api/usage?from=YYYY-MM-DD&to=YYYY-MM-DD`

Igual que el anterior pero para **todas las instancias**. `rows` ordenado
por `instance, day`. Pensado para dashboards y exports CSV.

---

## `GET /api/usage/summary?from=YYYY-MM&to=YYYY-MM`

Agregado mensual por `(owner_tag, mes)`. Default: últimos 3 meses
naturales.

```json
{
  "from": "2026-03", "to": "2026-05",
  "rows": [
    {
      "owner_tag": "tenant-acme", "month": "2026-05",
      "messages_in": 4821, "messages_out": 5102,
      "spamguard_blocks": 14, "lifecycle_events": 23,
      "active_instances": 2
    },
    {
      "owner_tag": "", "month": "2026-05",
      "messages_in": 18, "messages_out": 22,
      "spamguard_blocks": 0, "lifecycle_events": 1,
      "active_instances": 1
    }
  ]
}
```

Pensado para billing: el integrador mapea `owner_tag` a su tenant y suma
los counters que tarifique.

---

## `GET /api/audit?instance=&owner_tag=&limit=`

Append-only log de operaciones (`instance.create / patch / delete`,
`tenant.upsert / delete`, `outbox.enqueue / expire / failed`,
`backend.boot`). La tabla subyacente tiene triggers que rechazan
UPDATE/DELETE — inmutable a nivel DB.

| Param | Default | Notas |
|---|---|---|
| `instance` | (vacío) | Filtrar por nombre de instancia. |
| `owner_tag` | (vacío) | Filtrar por tenant: solo entradas de instancias con ese `owner_tag` (desde v0.25.0). Combinable con `instance` (AND lógico). |
| `limit` | 100 | Máximo 500. |

```json
{
  "entries": [
    {
      "id": 412,
      "ts": "2026-05-26T08:15:33Z",
      "actor": "api",
      "action": "instance.patch",
      "instance": "whatsapp-main",
      "target": "",
      "metadata": {"owner_tag_set": true, "spamguard_enabled_set": false}
    }
  ]
}
```

---

## `GET /api/health`

Liveness + readiness check. Sin auth. Pensado tanto para Docker
HEALTHCHECK como para Prometheus / scraping de status.

```json
{
  "status": "ok",
  "version": "0.23.0",
  "ts": "2026-05-26T11:30:00Z",
  "uptime_seconds": 12345,
  "checks": {
    "db": {"ok": true, "latency_ms": 4},
    "instances_connected": 4,
    "instances_total": 4,
    "outbox_pending": 0
  },
  "instances": [{"name":"whatsapp-main","state":"ready","jid":"..."}]
}
```

Códigos:

- `200 OK` cuando todo está sano (`status: "ok"`).
- `503 Service Unavailable` cuando la DB no responde en 2 s
  (`status: "degraded"`). Docker considera el container unhealthy y
  Swarm puede reiniciarlo según política.

Campos:

| Campo | Significado |
|---|---|
| `status` | `ok` o `degraded`. |
| `uptime_seconds` | Segundos desde que el binario arrancó. Útil para detectar restart loops. |
| `checks.db.ok` | `true` si Postgres respondió `Ping` en < 2 s. |
| `checks.db.latency_ms` | Tiempo que tardó el ping. > 100 ms sostenido = problema. |
| `checks.outbox_pending` | Mensajes en outbox pendientes de entregar globalmente. Alerta si crece. |

---

## `GET /metrics`

Prometheus scrape. Sin auth.

| Métrica | Tipo | Labels | Descripción |
|---|---|---|---|
| `qrsgen_messages_total` | counter | `direction`, `instance`, `owner_tag` | Mensajes procesados (in/out). |
| `qrsgen_spamguard_blocks_total` | counter | `instance`, `owner_tag` | Outgoings bloqueados por dup. |
| `qrsgen_lifecycle_events_total` | counter | `instance`, `event`, `owner_tag` | Eventos lifecycle emitidos. |
| `qrsgen_message_dispatch_errors_total` | counter | `direction`, `instance`, `kind`, `owner_tag` | Fallos de despacho. |
| `qrsgen_lifecycle_webhook_retries_total` | counter | `event`, `outcome` | Reintentos de webhooks críticos. |
| `qrsgen_active_instances` | gauge | – | Instancias en `connected` o `ready`. |
| `qrsgen_total_instances` | gauge | – | Total gestionadas. |
| `qrsgen_version_info` | gauge (info) | `version` | Fijo a 1; permite join en Grafana para mostrar versión activa. Desde v0.28.2. |

Plus métricas estándar Go runtime (`go_*`, `process_*`).

El label `owner_tag` (desde v0.25.0) permite separar métricas por
tenant en Grafana. Para instancias sin tenant configurado el label
sale vacío. Queries que no filtran por `owner_tag` siguen funcionando
(Prometheus agrega naturalmente al sumar). Ejemplo de query
multi-tenant:

```promql
sum by (owner_tag) (rate(qrsgen_messages_total{direction="out"}[5m]))
```

---

## `GET /static/brand-asset.png`

Asset estático (ej. avatar genérico). Útil si el downstream necesita
descargar un PNG por URL para asociar a un contacto sintético.

## Glosario

**Outbox**: cola persistida en Postgres donde qrsgen guarda outgoings
no entregables al instante. Cada fila tiene `status` (pending / sent /
expired / failed), `attempts`, `expires_at`.

**BanWatcher score**: número entre 0 y 1 que resume las tres señales
(velocity / diversity / delivery_ratio). Niveles cualitativos: `ok`,
`low`, `moderate`, `high`.

**Velocity**: mensajes saliente por unidad de tiempo. Si supera el
threshold se considera spam-like.

**Diversity**: número de destinatarios únicos por unidad de tiempo.
Outreach masivo dispara esta señal.

**Delivery ratio**: fracción de envíos exitosos sobre intentos totales.
Si WhatsApp rechaza muchos, este ratio baja → near-ban.

**Rising-edge alert**: alerta que se emite una sola vez al cruzar un
threshold (no se repite hasta que se limpia y vuelve a cruzar). Evita
ruido en el panel del agente.

**Usage tracking**: contadores diarios persistidos en
`bridge_usage_daily` (in/out + lifecycle + spamguard). Flushea cada 60s
desde memoria.

**Owner tag aggregate**: agregado mensual del usage agrupando por
`owner_tag` para facturación multi-tenant.

**Audit log inmutable**: tabla `bridge_audit_log` con triggers en
Postgres que rechazan UPDATE/DELETE. Solo permite INSERT. Útil para
forensics y compliance.

**Prometheus scrape**: técnica donde Prometheus pide periódicamente las
métricas a un endpoint HTTP (típicamente `/metrics`). qrsgen lo expone
sin auth porque las métricas son operacionales, no PII.

**Counter** (Prometheus): métrica que solo aumenta (mensajes totales,
errores). Para tasas se calcula `rate(counter[5m])`.

**Gauge** (Prometheus): métrica que sube y baja (instancias activas).
Refleja un valor instantáneo.
