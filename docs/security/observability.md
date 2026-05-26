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

## Glosario

**Observabilidad**: capacidad de entender qué está haciendo un sistema
desde fuera (logs, metrics, traces). qrsgen expone las 3 primeras.

**slog**: librería estándar de Go para logging estructurado (key=value
o JSON). qrsgen escribe JSON a stdout para que docker/journald lo
recoja.

**Logs estructurados**: cada línea es un objeto con campos parseables
(JSON o key=value). Mucho más fácil de filtrar/agrupar que texto libre.

**Journald**: subsistema de logs de systemd que indexa por unit, fecha,
prioridad. qrsgen lo usa indirectamente para los logs del firewall y
del backup timer.

**dmesg**: comando que muestra los logs del kernel Linux. Las reglas
iptables con `LOG` se ven aquí.

**Rate-limited (en LOG)**: limitar cuántos eventos similares se
loguean por unidad de tiempo. qrsgen rate-limita los `QRSGEN-DROP` a
5/min para evitar inundar logs.

**Prometheus alerting**: reglas declarativas en formato PromQL que
disparan alertas cuando una métrica cruza umbrales. Se evalúan
periódicamente en Prometheus.

**PromQL**: lenguaje de query de Prometheus. Funciones típicas:
`rate()` para tasas, `increase()` para incremento absoluto.
