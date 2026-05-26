# Audit log inmutable

`internal/audit` escribe en `bridge_audit_log` cada operación relevante:

- `instance.create / patch / delete` — operaciones contra `/api/instances/*`.
- `outbox.enqueue / expire / failed` — eventos del outbox.
- `backend.boot` — al arrancar el proceso.

## Tabla y triggers

```
bridge_audit_log
├── id (BIGSERIAL PK)
├── ts (TIMESTAMPTZ, default NOW())
├── actor (TEXT, "api" | "system")
├── action (TEXT)
├── instance (TEXT, nullable)
├── target (TEXT, nullable)
└── metadata (JSONB)
```

La tabla tiene dos triggers en plpgsql:

```sql
BEFORE UPDATE → RAISE 'append-only'
BEFORE DELETE → RAISE 'append-only'
```

Una app comprometida no puede reescribir el log sin privilegios DBA.

## Endpoint

`GET /api/audit?instance=&limit=` lo lista para compliance/forensics.
Default 100 entradas, máximo 500.

## Diseño

- **Best-effort writes**: si Postgres está caído, qrsgen loguea un
  warning y **sigue** — la operación user-facing no se bloquea por
  audit-log unavailability.
- **Inmutabilidad a nivel DB**: incluso si el binario qrsgen es
  comprometido, no puede rewriter el log (las queries UPDATE/DELETE las
  rechaza Postgres directamente vía trigger).
- **Lo que NO es**: signed/encrypted. Para evidence en juicio se debería
  firmar cada fila con HMAC + ship a syslog externo (CloudWatch Logs,
  Loki con immutable retention). Pendiente para v0.24+.

## Audit log vs Prometheus metrics

| Aspecto | Audit log | Prometheus |
|---|---|---|
| Granularidad | 1 fila por evento | counters agregados |
| Latencia de retención | persistido forever | retención del scraper (típicamente 15 días) |
| Búsqueda | SQL completo (instance, action, JSON metadata) | label-based query (PromQL) |
| Caso de uso | Forensics + compliance | Dashboards + alerting |

Ambos son complementarios — el audit log responde "¿quién hizo qué
cuándo?", Prometheus responde "¿cuánto ocurrió en el último periodo?".
