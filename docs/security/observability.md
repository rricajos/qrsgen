# Observabilidad y logs

| Fuente | Cómo leerlo |
|---|---|
| qrsgen — slog JSON estructurado | `docker service logs qrsgen_qrsgen` |
| Firewall — apply/flush | `journalctl -u qrsgen-firewall.service` + `/var/log/qrsgen-firewall.log` |
| Paquetes droppeados | `dmesg \| grep QRSGEN-DROP` (rate-limited 5/min) |
| Errores de despacho | Prometheus `qrsgen_message_dispatch_errors_total{kind,direction,instance}` |
| Operaciones de la API | `bridge_audit_log` ([capa 6](layer-6-audit.md)) |

## Alerting Prometheus básico

```promql
# Strikes (acción inmediata)
increase(qrsgen_lifecycle_events_total{event="strike"}[1h]) > 0

# Tasa alta de errores
rate(qrsgen_message_dispatch_errors_total[5m]) > 0.1

# Outbox creciendo (instancia probablemente desconectada largo rato)
increase(qrsgen_lifecycle_events_total{event="outgoing_expired"}[1h]) > 0
```
