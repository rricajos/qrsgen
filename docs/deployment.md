# Deployment

qrsgen se distribuye como **un binario y/o una imagen Docker**. El despliegue
de referencia es **Docker Swarm** + Postgres en la misma overlay LAN.

## Opciones de imagen

### 1. Imagen pre-built desde GHCR (recomendado)

```bash
docker pull ghcr.io/rricajos/qrsgen:0.23.0-rc1
# o el tag :latest
```

Multi-arch (amd64 + arm64). Firmada con cosign. Construida por GoReleaser
en cada tag `vX.Y.Z` desde GitHub Actions.

### 2. Build local desde el repo

```bash
cd /opt/qrsgen
docker build -t qrsgen:0.23.0-rc1 .
```

Multi-stage build: `golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot`.
Imagen final ~25 MB.

### 3. Binario nativo desde release

Para deploys sin Docker (raros pero posibles):

```bash
curl -L -o qrsgen.tar.gz \
  https://github.com/rricajos/qrsgen/releases/download/v0.23.0-rc1/qrsgen_0.23.0-rc1_linux_amd64.tar.gz
tar xzf qrsgen.tar.gz
chmod +x qrsgen
./qrsgen   # lee env vars; ver tabla abajo
```

Cada binario lleva SBOM (`*.sbom.json`) + checksum firmado.

---

## Stack Docker Swarm

Compose portable en [`docker-compose.yml`](https://github.com/rricajos/qrsgen/blob/main/docker-compose.yml) del repo. Vars editables desde
Portainer Stacks UI o `.env`.

### Variables de entorno

**Requeridas:**

| Variable | Descripción |
|---|---|
| `POSTGRES_HOST` | Host del Postgres (típicamente `postgres` en overlay). |
| `POSTGRES_PASSWORD` | Password del usuario de qrsgen. |
| `DOWNSTREAM_BASE_URL` | URL del sistema downstream (ej: `https://chat.example.com`). |
| `DOWNSTREAM_API_TOKEN` | Token con permisos full sobre la cuenta downstream. |
| `INSTANCE_NAME` | Nombre de la instancia "default" creada al boot (puede coincidir con una existente para no crear nada nuevo). |

**Opcionales** (con defaults razonables):

| Variable | Default | Notas |
|---|---|---|
| `QRSGEN_VERSION` | `0.23.0-rc1` | Tag de imagen. |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_DB` | `bridge` | |
| `POSTGRES_USER` | `postgres` | |
| `DOWNSTREAM_ACCOUNT_ID` | `1` | |
| `DOWNSTREAM_INBOX_ID` | `0` | Inbox fallback cuando una instancia no tiene `inbox_id` configurado. |
| `QRSGEN_API_TOKEN` | (vacío) | Si vacío, **auth desactivada** (modo dev, log WARNING). Genera con `python3 -c "import secrets;print(secrets.token_urlsafe(32))"`. |
| `WEBHOOK_HMAC_SECRET` | (vacío) | Si vacío, el webhook entrante queda abierto. Si set, exige `X-Qrsgen-Signature` HMAC-SHA256. |
| `DEDUP_ENABLED` | `true` | |
| `DEDUP_WINDOW_MS` | `10000` | Ventana LID-twin dedup. |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `OVERLAY_NETWORK` | `conexianet` | Red docker overlay externa. |
| `PORT` | `3100` | HTTP listener. |

### Despliegue

```bash
cd /opt/qrsgen-stack
cp .env.example .env
# editar .env con tus valores
docker stack deploy -c docker-compose.yml --resolve-image=changed qrsgen
```

Verificar:

```bash
docker service ps qrsgen_qrsgen
docker service logs --tail=50 qrsgen_qrsgen
curl -sS http://qrsgen:3100/api/health | jq   # desde dentro de la overlay
```

### Hardening del container

El compose viene con tres mecanismos de hardening activos por defecto
(ver [`docs/security.md`](security.md) capa 5):

```yaml
services:
  qrsgen:
    read_only: true
    volumes:
      - type: tmpfs
        target: /tmp
        tmpfs:
          size: 67108864   # 64 MB
```

Combinado con la imagen distroless (sin shell ni paquetes) y el usuario
`nonroot:nonroot`, la superficie de un RCE queda reducida.

### Aliases internos

El servicio responde en la overlay como:

- `qrsgen:3100` (alias principal).
- `bridge_bridge:3100` (alias retro-compat con workflows n8n viejos).

### Update strategy

```yaml
deploy:
  update_config:
    parallelism: 1
    order: stop-first         # ← previene WhatsApp JID race
    delay: 5s
    monitor: 5s
    failure_action: pause
```

`order: stop-first` significa que el container viejo para **antes** de que
el nuevo arranque — esto evita que dos containers compitan por la misma
sesión WhatsApp durante el rollout (WhatsApp kicea ambos si detecta el
conflicto). El precio es ~15s de downtime por deploy, **cubierto por el
outbox** (5 min TTL).

### Healthcheck

El Dockerfile incluye:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/app/qrsgen", "-healthcheck"]
```

`-healthcheck` es un flag del propio binario que hace un GET interno a
`/api/health` y exitea 0/1. Funciona en distroless porque no necesita
curl/wget.

---

## Schema migrations

`main.go` ejecuta migraciones idempotentes en el bootstrap:

| Llamada | Crea |
|---|---|
| `lib.EnsureBridgeSchema` | `bridge_dedup` |
| `usage.EnsureSchema` | `bridge_usage_daily` |
| `audit.EnsureSchema` | `bridge_audit_log` + triggers append-only |
| `outbox.EnsureSchema` | `bridge_outgoing_queue` |
| `manager.EnsureSchema` | `bridge_instance` + columnas progresivas (`owner_tag`, `last_qr_msg_id`, spamguard, etc.) |

Todas usan `CREATE TABLE IF NOT EXISTS` y `ALTER TABLE ... ADD COLUMN IF
NOT EXISTS`. Sin versionado formal — para producción con varios deployers
considerar `golang-migrate` o equivalente.

---

## Portabilidad a otro VPS

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
- La overlay network creada (`docker network create -d overlay --attachable conexianet`).

---

## Backups

`ops/backup/postgres-backup.sh` + systemd timer hacen `pg_dump -Fc` diario
de la DB `bridge` con retención 7 días + 4 semanas (rotación dominical).

Install:

```bash
sudo install -m 0755 ops/backup/postgres-backup.sh /opt/qrsgen-stack/postgres-backup.sh
sudo install -m 0644 ops/backup/qrsgen-postgres-backup.service /etc/systemd/system/
sudo install -m 0644 ops/backup/qrsgen-postgres-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qrsgen-postgres-backup.timer
```

Runbook completo de install + restore + drop-in config:
[`ops/backup/README.md`](https://github.com/rricajos/qrsgen/blob/main/ops/backup/README.md).

### Backup manual

```bash
sudo systemctl start qrsgen-postgres-backup.service
sudo journalctl -u qrsgen-postgres-backup.service -n 20
ls -lh /opt/qrsgen-stack/backups/daily/
```

### Backup off-site (recomendado, no incluido)

Para enviar el dump a S3/Backblaze/Wasabi añade un drop-in:

```
/etc/systemd/system/qrsgen-postgres-backup.service.d/offsite.conf
```

```ini
[Service]
ExecStartPost=/usr/local/bin/aws s3 cp /opt/qrsgen-stack/backups/daily/ s3://my-bucket/qrsgen/ --recursive --only-show-errors
```

`systemctl daemon-reload` y listo.

---

## Firewall egress (recomendado)

`/etc/systemd/system/qrsgen-firewall.service` ejecuta el watcher que:

- Aplica iptables rules con allowlist (overlay + Meta CIDRs en :443).
- Escucha `docker events` para detectar restart de qrsgen.
- Re-aplica reglas con la nueva IP del container.

Install:

```bash
sudo install -m 0755 firewall.sh /opt/qrsgen-stack/firewall.sh
sudo install -m 0755 qrsgen-firewall-watcher.sh /opt/qrsgen-stack/qrsgen-firewall-watcher.sh
sudo install -m 0644 qrsgen-firewall.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qrsgen-firewall.service
```

Verificar:

```bash
sudo /opt/qrsgen-stack/firewall.sh status
journalctl -u qrsgen-firewall.service -n 20
```

Detalle en [`security.md` capa 3](security.md).

---

## Rollback

Si el nuevo tag se porta mal, vuelve al anterior cambiando solo el `.env`:

```bash
sed -i 's/^QRSGEN_VERSION=.*/QRSGEN_VERSION=0.21.0/' /opt/qrsgen-stack/.env
docker stack deploy -c /opt/qrsgen-stack/docker-compose.yml --resolve-image=changed qrsgen
```

~30s y vuelves al estado anterior. Las migraciones nuevas son aditivas
(columnas y tablas nuevas no rompen versiones viejas), así que el
rollback es seguro.
