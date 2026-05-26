# Logs útiles

```bash
# qrsgen (slog JSON estructurado en stdout)
docker service logs qrsgen_qrsgen --since 10m | grep -E "WARN|ERROR"

# Filtrar por instancia concreta
docker service logs qrsgen_qrsgen --since 1h | grep "instance=whatsapp-main"

# Eventos lifecycle emitidos
docker service logs qrsgen_qrsgen --since 1h | grep "events webhook sent"

# Outbox enqueues
docker service logs qrsgen_qrsgen --since 1h | grep "outbox enqueued"

# Banwatch alerts
docker service logs qrsgen_qrsgen --since 1h | grep "banwatch"

# Firewall watcher
journalctl -u qrsgen-firewall.service --since "10 min ago"
tail -50 /var/log/qrsgen-firewall.log

# Backups
journalctl -u qrsgen-postgres-backup.service --since "1 day ago"
```

## Glosario

**slog JSON estructurado**: cada línea de log de qrsgen es un objeto
JSON con campos parseables (timestamp, level, msg, ...kv). Filtrable
con `jq`.

**Stdout**: salida estándar del proceso. Docker la captura y la expone
vía `docker service logs`.

**docker service logs**: comando para ver los logs de todos los
containers de un servicio Swarm.

**Filter por instancia**: técnica para aislar las líneas de log de
una instancia concreta. qrsgen siempre emite el campo `instance`
cuando aplica.

**Lifecycle webhook sent**: línea de log de qrsgen tras emitir un
webhook de lifecycle event exitosamente.

**Outbox enqueued**: línea de log de qrsgen cuando encola un mensaje
en el outbox (instancia disconnected).

**Banwatch alert**: línea de log cuando el detector cruza un
threshold (info / warn nivel).

**journalctl**: comando para consultar los logs gestionados por
systemd (units). qrsgen lo usa indirectamente para el firewall
watcher y el backup timer.
