# Alerting Prometheus

Sugerencias de reglas:

```promql
# Instancias activas debajo del esperado
qrsgen_active_instances < 4

# Tasa alta de errores
rate(qrsgen_message_dispatch_errors_total[5m]) > 0.1

# Spamguard activo (alguien está duplicando)
increase(qrsgen_spamguard_blocks_total[5m]) > 5

# Strike de WhatsApp (¡acción inmediata!)
increase(qrsgen_lifecycle_events_total{event="strike"}[1h]) > 0

# Ban risk alto sostenido
increase(qrsgen_lifecycle_events_total{event="ban_risk"}[10m]) > 0

# Outbox creciendo (instancia probablemente caída)
increase(qrsgen_lifecycle_events_total{event="outgoing_expired"}[1h]) > 0
```

## Glosario

**Alerting rule**: expresión PromQL que, cuando se cumple, dispara
una alerta. Se evalúa cada N segundos por Prometheus.

**increase()** (PromQL): incremento absoluto de un counter en una
ventana. Útil para "cuántas veces pasó X en el último periodo".

**rate()** (PromQL): tasa promedio por segundo de un counter en una
ventana. Útil para "cuánto está ocurriendo X por unidad de tiempo".

**Threshold (alerta)**: valor de la expresión por encima del cual se
considera "alerta activa". Más bajo = más sensible (más false
positives); más alto = menos sensible (false negatives).

**Strike (lifecycle event)**: TemporaryBan o ConnectFailure 4xx de
WhatsApp. Crítico — debería paginar inmediatamente.

**Outbox expired (lifecycle event)**: mensaje en el outbox que no se
entregó antes del TTL. Indica que una instancia lleva caída más de 5
min.

**Active instances (gauge)**: snapshot del número de instancias
actualmente conectadas. Si baja del esperado, alerta.

**Dispatch error**: fallo al enviar un mensaje (a WhatsApp o al
downstream). El label `kind` indica el tipo concreto.
