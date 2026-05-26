# Stack Docker Swarm

Compose portable en
[`docker-compose.yml`](https://github.com/rricajos/qrsgen/blob/main/docker-compose.yml)
del repo. Vars editables desde Portainer Stacks UI o `.env`.

## Despliegue

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

## Aliases internos

El servicio responde en la overlay como:

- `qrsgen:3100` (alias principal).
- `bridge_bridge:3100` (alias retro-compat con workflows n8n viejos).

## Healthcheck

El Dockerfile incluye:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/app/qrsgen", "-healthcheck"]
```

`-healthcheck` es un flag del propio binario que hace un GET interno a
`/api/health` y exitea 0/1. Funciona en distroless porque no necesita
curl/wget.

## Networks

El compose declara la red overlay externa `net` (por defecto). Crear si
no existe:

```bash
docker network create -d overlay --attachable net
```

Configurable vía `OVERLAY_NETWORK` env var si quieres otro nombre.
