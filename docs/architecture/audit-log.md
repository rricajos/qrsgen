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

## Glosario

**Audit log**: registro inmutable y cronológico de operaciones
relevantes del sistema. Pensado para forensics, compliance y
disputas de facturación.

**Append-only**: tabla donde solo se permite INSERT. Las filas
existentes no se pueden modificar ni borrar. qrsgen lo garantiza a nivel
DB con triggers PL/pgSQL.

**Trigger** (PL/pgSQL): función almacenada en Postgres que se ejecuta
automáticamente en ciertos eventos (INSERT, UPDATE, DELETE) sobre una
tabla. qrsgen lo usa para hacer cumplir la inmutabilidad.

**PL/pgSQL**: lenguaje procedural de Postgres para escribir triggers y
funciones almacenadas. Sintaxis parecida a SQL extendido.

**Tamper-evident**: propiedad de un registro donde cualquier intento de
modificación es detectable o imposible. El audit log de qrsgen lo
garantiza vía triggers (modificación = excepción Postgres).

**Forensics**: investigación post-incidente para reconstruir qué pasó.
El audit log da la cronología "alguien creó X a las HH:MM con Y
metadata".

**Compliance**: cumplimiento normativo (GDPR, SOC2, ISO 27001, etc.).
Muchas normas exigen registro inmutable de operaciones críticas — el
audit log de qrsgen ayuda a cumplirlo.

**Actor**: quien ejecutó la operación. Valores típicos: `api` (alguien
con Bearer token), `system` (un proceso interno de qrsgen, como el
boot o el outbox).

**Best-effort write**: la inserción en el audit log no bloquea la
operación user-facing si falla. Se loguea y se sigue — preferible a
denegar la operación si la DB tiene un blip transitorio.

**Counter (Prometheus)** vs **audit entry**: el counter da volúmenes
agregados ("cuántos deletes en la última hora"), la entrada audit da
contexto individual ("qué instancia, quién, cuándo, qué metadata").

**Signed audit**: extensión futura donde cada fila lleva una firma
HMAC del actor, evitando que un atacante con DBA pueda forjar
entradas. Pendiente en qrsgen.
