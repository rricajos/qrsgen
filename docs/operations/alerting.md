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

# Tasa de errores downstream en features real-time > 10% durante 5 min
# (avatar / reaction / typing / read_receipt — desde v0.35.0)
(
  sum by (feature) (rate(qrsgen_realtime_events_total{result="ds_error"}[5m]))
  /
  sum by (feature) (rate(qrsgen_realtime_events_total[5m]))
) > 0.10

# Burst de wa_error en avatares (sesión WA degradada)
increase(qrsgen_realtime_events_total{feature="avatar",result="wa_error"}[10m]) > 20
```

Las queries de `qrsgen_realtime_events_total` y más sugerencias de
paneles Grafana están documentadas en
[Observabilidad](../api/observability.md#qrsgen_realtime_events_total-v0350).

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
