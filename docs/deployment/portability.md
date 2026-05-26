# Portabilidad multi-VPS

```bash
scp -r /opt/qrsgen-stack/ root@new-vps:/opt/
scp /etc/systemd/system/qrsgen-firewall.service root@new-vps:/etc/systemd/system/
scp /etc/systemd/system/qrsgen-postgres-backup.{service,timer} root@new-vps:/etc/systemd/system/

ssh new-vps "
  cd /opt/qrsgen-stack
  cp .env.example .env && vim .env   # ajustar passwords/tokens/hostnames
  docker stack deploy -c docker-compose.yml qrsgen
  systemctl daemon-reload
  systemctl enable --now qrsgen-firewall.service
  systemctl enable --now qrsgen-postgres-backup.timer
"
```

Necesitas:

- La imagen `qrsgen:X` en el registry o build local.
- Postgres reachable en el overlay (`postgres:5432`).
- La overlay network creada
  (`docker network create -d overlay --attachable net`).

## Migrar Postgres también

Si el nuevo VPS no comparte Postgres con el viejo:

```bash
# 1. Dump en el viejo
sudo systemctl start qrsgen-postgres-backup.service
scp /opt/qrsgen-stack/backups/daily/qrsgen-bridge-YYYYMMDD-HHMM.dump \
    root@new-vps:/tmp/

# 2. Restore en el nuevo
ssh new-vps "
  docker exec postgres psql -U postgres -c 'CREATE DATABASE bridge;'
  docker exec -i postgres pg_restore -U postgres -d bridge -F c < /tmp/qrsgen-bridge-*.dump
  docker stack deploy -c /opt/qrsgen-stack/docker-compose.yml qrsgen
"
```

Tras el restore, qrsgen reconecta automáticamente cada sesión persistida
en `whatsmeow_*` — no se necesita re-escanear QR.
