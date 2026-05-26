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
