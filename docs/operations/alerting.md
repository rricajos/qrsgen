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
