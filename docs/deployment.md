# Deployment

## Stack Docker Swarm

Compose portable en `docker-compose.yml` (root del repo). Vars editables desde Portainer Stacks UI o `.env`.

### Variables de entorno

Requeridas:

| Variable | Descripción |
|---|---|
| `POSTGRES_HOST` | Host del Postgres (típicamente `postgres` en overlay) |
| `POSTGRES_PASSWORD` | Password del usuario de qrsgen |
| `DOWNSTREAM_BASE_URL` | Ej: `https://chat.example.com` |
| `DOWNSTREAM_API_TOKEN` | Token con permisos full sobre el account |
| `QRSGEN_API_TOKEN` | Bearer token para auth de la propia API qrsgen (recomendado en prod) |

Opcionales (con defaults razonables):

| Variable | Default | Notas |
|---|---|---|
| `QRSGEN_VERSION` | `0.21` | Tag de imagen |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_DB` | `bridge` | |
| `POSTGRES_USER` | `postgres` | |
| `DOWNSTREAM_ACCOUNT_ID` | `1` | |
| `DOWNSTREAM_INBOX_ID` | `0` | Inbox fallback cuando una instancia no tiene `inbox_id` configurado |
| `INSTANCE_NAME` | `DEVOPS` | Instancia "default" creada al boot |
| `DEDUP_ENABLED` | `true` | |
| `DEDUP_WINDOW_MS` | `10000` | Ventana LID-twin dedup |
| `LOG_LEVEL` | `info` | debug/info/warn/error |
| `OVERLAY_NETWORK` | `conexianet` | Red docker overlay externa |

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
```

### Aliases internos

El servicio responde en la overlay como:

- `qrsgen:3100`
- `bridge_bridge:3100` (alias retro-compat con workflows n8n viejos)

### Portabilidad a otro VPS

```bash
scp -r /opt/qrsgen-stack/ root@new-vps:/opt/
scp /etc/systemd/system/qrsgen-firewall.service root@new-vps:/etc/systemd/system/
ssh new-vps "
  cd /opt/qrsgen-stack
  cp .env.example .env && vim .env  # ajustar passwords/tokens/hostnames
  docker stack deploy -c docker-compose.yml qrsgen
  systemctl daemon-reload && systemctl enable --now qrsgen-firewall.service
"
```

Necesita la imagen `qrsgen:X` en el registry o build local del repo.

## Build de la imagen

```bash
cd /opt/qrsgen
docker build -t qrsgen:0.21 .
```

Multi-stage build: golang:1.25-alpine → distroless/static-debian12. Imagen final ~32MB.

## Schema migrations

`manager.EnsureSchema()` ejecuta una lista de `CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` en el bootstrap. Idempotente, pero sin versionado formal — para producción seria considerar `golang-migrate`.

## Backup

La sesión WhatsApp vive en las tablas `whatsmeow_*` del Postgres. La config en `bridge_instance`. Para backup completo:

```bash
docker exec postgres pg_dump -U postgres -d bridge -F c -f /tmp/qrsgen-$(date +%F).dump
docker cp postgres:/tmp/qrsgen-$(date +%F).dump ./
```

Restore:

```bash
docker exec -i postgres pg_restore -U postgres -d bridge --clean --if-exists < qrsgen-YYYY-MM-DD.dump
```

Tras restore, reiniciar qrsgen → bootstrap reconecta todas las sesiones.

## Watchdog del firewall

`/etc/systemd/system/qrsgen-firewall.service` ejecuta `qrsgen-firewall-watcher.sh` que:

- Aplica iptables rules al arrancar (cubre host reboot)
- Escucha `docker events` para detectar restart de qrsgen
- Re-aplica reglas con la nueva IP del container

Logs: `/var/log/qrsgen-firewall.log`

