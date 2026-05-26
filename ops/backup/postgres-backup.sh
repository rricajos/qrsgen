#!/usr/bin/env bash
# qrsgen — Postgres backup script.
#
# Dumps the `bridge` database (bridge_instance + whatsmeow_*) as a custom-format
# archive ready for pg_restore. Designed for fast restore granularity at the
# app level — VPS-snapshot backups still cover the full disk separately.
#
# Layout under BACKUP_DIR:
#   daily/qrsgen-bridge-YYYYMMDD-HHMM.dump  → kept 7 days
#   weekly/qrsgen-bridge-YYYY-WW.dump       → kept 4 weeks (Sunday rotation)
#
# Exit codes:
#   0  backup ok
#   1  pg_dump failed
#   2  could not locate postgres container
#   3  unwritable backup dir
#
# Configurable via env (defaults below):
#   POSTGRES_CONTAINER   container name (auto-detect: first container matching ^postgres)
#   POSTGRES_USER        postgres user (default: postgres)
#   POSTGRES_DB          db name (default: bridge)
#   BACKUP_DIR           dump root (default: /opt/qrsgen-stack/backups)
#   RETENTION_DAILY      days of daily dumps to keep (default: 7)
#   RETENTION_WEEKLY     weeks of weekly dumps to keep (default: 4)

set -euo pipefail

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-}"
POSTGRES_USER="${POSTGRES_USER:-postgres}"
POSTGRES_DB="${POSTGRES_DB:-bridge}"
BACKUP_DIR="${BACKUP_DIR:-/opt/qrsgen-stack/backups}"
RETENTION_DAILY="${RETENTION_DAILY:-7}"
RETENTION_WEEKLY="${RETENTION_WEEKLY:-4}"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# Auto-detect postgres container if not provided. Picks the first match.
if [[ -z "${POSTGRES_CONTAINER}" ]]; then
  POSTGRES_CONTAINER=$(docker ps --format '{{.Names}}' | grep -E '^postgres' | head -1 || true)
fi
if [[ -z "${POSTGRES_CONTAINER}" ]]; then
  log "ERROR: no postgres container found"
  exit 2
fi

DAILY_DIR="${BACKUP_DIR}/daily"
WEEKLY_DIR="${BACKUP_DIR}/weekly"
mkdir -p "${DAILY_DIR}" "${WEEKLY_DIR}" || { log "ERROR: cannot create backup dirs"; exit 3; }

TS=$(date '+%Y%m%d-%H%M')
DAILY_FILE="${DAILY_DIR}/qrsgen-bridge-${TS}.dump"

log "backup start db=${POSTGRES_DB} container=${POSTGRES_CONTAINER} → ${DAILY_FILE}"

# pg_dump custom format inside the container, stream to host file.
if ! docker exec "${POSTGRES_CONTAINER}" pg_dump -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -Fc \
     > "${DAILY_FILE}.tmp"; then
  log "ERROR: pg_dump failed"
  rm -f "${DAILY_FILE}.tmp"
  exit 1
fi
mv "${DAILY_FILE}.tmp" "${DAILY_FILE}"

SIZE=$(du -h "${DAILY_FILE}" | cut -f1)
log "backup ok size=${SIZE}"

# Weekly rotation on Sunday (day of week = 0).
if [[ "$(date '+%u')" == "7" ]]; then
  WEEKLY_FILE="${WEEKLY_DIR}/qrsgen-bridge-$(date '+%Y-%V').dump"
  cp "${DAILY_FILE}" "${WEEKLY_FILE}"
  log "weekly snapshot → ${WEEKLY_FILE}"
fi

# Retention. Use mtime to be timezone-safe.
find "${DAILY_DIR}" -name 'qrsgen-bridge-*.dump' -type f -mtime "+${RETENTION_DAILY}" -delete
find "${WEEKLY_DIR}" -name 'qrsgen-bridge-*.dump' -type f -mtime "+$((RETENTION_WEEKLY * 7))" -delete

DAILY_COUNT=$(find "${DAILY_DIR}" -name 'qrsgen-bridge-*.dump' -type f | wc -l)
WEEKLY_COUNT=$(find "${WEEKLY_DIR}" -name 'qrsgen-bridge-*.dump' -type f | wc -l)
log "retention done daily=${DAILY_COUNT} weekly=${WEEKLY_COUNT}"
