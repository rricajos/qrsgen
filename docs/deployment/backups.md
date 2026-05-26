# Backups

`ops/backup/postgres-backup.sh` + systemd timer hacen `pg_dump -Fc`
diario de la DB `bridge` con retención 7 días + 4 semanas (rotación
dominical).

## Install

```bash
sudo install -m 0755 ops/backup/postgres-backup.sh /opt/qrsgen-stack/postgres-backup.sh
sudo install -m 0644 ops/backup/qrsgen-postgres-backup.service /etc/systemd/system/
sudo install -m 0644 ops/backup/qrsgen-postgres-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qrsgen-postgres-backup.timer
```

Runbook completo de install + restore + drop-in config:
[`ops/backup/README.md`](https://github.com/rricajos/qrsgen/blob/main/ops/backup/README.md).

## Backup manual

```bash
sudo systemctl start qrsgen-postgres-backup.service
sudo journalctl -u qrsgen-postgres-backup.service -n 20
ls -lh /opt/qrsgen-stack/backups/daily/
```

## Backup off-site (recomendado, no incluido)

Para enviar el dump a S3/Backblaze/Wasabi añade un drop-in:

```
/etc/systemd/system/qrsgen-postgres-backup.service.d/offsite.conf
```

```ini
[Service]
ExecStartPost=/usr/local/bin/aws s3 cp /opt/qrsgen-stack/backups/daily/ s3://my-bucket/qrsgen/ --recursive --only-show-errors
```

`systemctl daemon-reload` y listo.

## Restore

```bash
DUMP=/opt/qrsgen-stack/backups/daily/qrsgen-bridge-YYYYMMDD-HHMM.dump
docker cp "$DUMP" postgres:/tmp/restore.dump
docker exec postgres psql -U postgres -c 'CREATE DATABASE bridge_restored;'
docker exec postgres pg_restore -U postgres -d bridge_restored /tmp/restore.dump

# Swap (downtime: stop qrsgen, rename, restart)
docker service scale qrsgen_qrsgen=0
docker exec postgres psql -U postgres -c 'ALTER DATABASE bridge RENAME TO bridge_old;'
docker exec postgres psql -U postgres -c 'ALTER DATABASE bridge_restored RENAME TO bridge;'
docker service scale qrsgen_qrsgen=1
```
