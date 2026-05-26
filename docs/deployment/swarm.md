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

## Glosario

**docker stack deploy**: comando para desplegar/actualizar un stack
completo desde un compose file en Docker Swarm.

**--resolve-image=changed**: opción de `stack deploy` que solo
re-resuelve digests para imágenes que cambiaron. Más rápido en re-deploys.

**Service**: unidad de despliegue en Swarm. Un service corre una o
varias réplicas de un container con la misma config.

**Replica**: una instancia corriendo del service. qrsgen usa `replicas:
1` por diseño (una sesión WhatsApp = un proceso).

**Alias** (overlay): nombre alternativo del servicio dentro del
overlay. qrsgen responde a `qrsgen:3100` y al alias retro `bridge_bridge:3100`.

**HEALTHCHECK**: directiva del Dockerfile que define cómo Docker chequea
si el container está sano. qrsgen lo implementa con un flag
`-healthcheck` del propio binario.

**Liveness probe**: comprobación de que el proceso está vivo y
responde. Docker la usa para reiniciar containers stuck.

**Distroless friendly**: característica que funciona en imágenes
distroless (sin shell/curl). El `-healthcheck` flag lo es porque usa
el propio binario.
