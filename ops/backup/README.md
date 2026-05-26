# Backups — Postgres (bridge database)

Daily `pg_dump -Fc` of the `bridge` database (used by qrsgen for session
storage and dedup). Runs at 03:00 server-local via systemd timer.

## Layout

```
/opt/qrsgen-stack/backups/
├── daily/
│   └── qrsgen-bridge-YYYYMMDD-HHMM.dump   (kept 7 days)
└── weekly/
    └── qrsgen-bridge-YYYY-WW.dump         (kept 4 weeks)
```

Weekly snapshots are copied on Sundays.

## Install

```bash
# Place script + units (root-owned).
sudo install -m 0755 ops/backup/postgres-backup.sh /opt/qrsgen-stack/postgres-backup.sh
sudo install -m 0644 ops/backup/qrsgen-postgres-backup.service /etc/systemd/system/
sudo install -m 0644 ops/backup/qrsgen-postgres-backup.timer /etc/systemd/system/

# Enable.
sudo systemctl daemon-reload
sudo systemctl enable --now qrsgen-postgres-backup.timer

# Verify schedule.
systemctl list-timers qrsgen-postgres-backup.timer
```

## Manual trigger

```bash
sudo systemctl start qrsgen-postgres-backup.service
sudo journalctl -u qrsgen-postgres-backup.service -n 20
```

## Restore

```bash
# 1. Copy the dump into the container.
DUMP=/opt/qrsgen-stack/backups/daily/qrsgen-bridge-YYYYMMDD-HHMM.dump
docker cp "$DUMP" postgres:/tmp/restore.dump

# 2. Drop the in-place database (DESTRUCTIVE — verify the dump first).
docker exec -i postgres pg_restore -l /tmp/restore.dump | head -20

# 3. Restore into a fresh database.
docker exec postgres psql -U postgres -c 'CREATE DATABASE bridge_restored;'
docker exec postgres pg_restore -U postgres -d bridge_restored /tmp/restore.dump

# 4. Swap (downtime: stop qrsgen, rename, restart).
docker service scale qrsgen_qrsgen=0
docker exec postgres psql -U postgres -c 'ALTER DATABASE bridge RENAME TO bridge_old;'
docker exec postgres psql -U postgres -c 'ALTER DATABASE bridge_restored RENAME TO bridge;'
docker service scale qrsgen_qrsgen=1
```

## Configuration

Override via systemd drop-in (`/etc/systemd/system/qrsgen-postgres-backup.service.d/override.conf`):

```ini
[Service]
Environment=BACKUP_DIR=/srv/backups/qrsgen
Environment=RETENTION_DAILY=14
Environment=RETENTION_WEEKLY=8
```

Then `sudo systemctl daemon-reload`.

## Notes

- The dump size is small (~1 MB for typical multi-instance qrsgen) — these are
  session keys + dedup metadata, not message bodies.
- VPS-level snapshots cover the full disk separately; this script gives faster
  per-database restore granularity.
- For off-site retention, pipe the daily dump into your object store of choice
  via a drop-in `ExecStartPost=`.
