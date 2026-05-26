# Deployment — Visión general

qrsgen se distribuye como **un binario y/o una imagen Docker**. El
despliegue de referencia es **Docker Swarm** + Postgres en la misma
overlay LAN.

## Navegación

- [Imágenes](images.md) — GHCR, build local, binario nativo.
- [Stack Swarm](swarm.md) — compose, despliegue, verificación.
- [Variables de entorno](env-vars.md) — requeridas y opcionales.
- [Telemetría pública](public-stats.md) — endpoint opt-in para landing
  pages con stats en vivo (con snippet Traefik).
- [Hardening](hardening.md) — read-only rootfs + tmpfs + update strategy.
- [Schema migrations](schema-migrations.md) — qué tablas crea cada
  paquete en bootstrap.
- [Portabilidad multi-VPS](portability.md) — copiar el stack a otro host.
- [Backups](backups.md) — systemd timer, manual trigger, off-site.
- [Firewall egress](firewall.md) — install + verificación.
- [Rollback](rollback.md) — cómo volver a la versión anterior.

## Glosario

**Imagen Docker**: paquete inmutable que incluye binario + librerías +
config mínima. qrsgen se distribuye como imagen (`distroless`).

**GHCR**: GitHub Container Registry. qrsgen publica imágenes
multi-arch firmadas tras cada tag `v*`.

**Docker Swarm**: orquestador nativo de Docker para clusters. qrsgen
usa `docker stack deploy` como referencia.

**Stack**: conjunto de servicios definidos en un `docker-compose.yml`
que Docker Swarm despliega como unidad.

**Overlay network**: red privada virtual entre nodos del Swarm. Los
servicios se ven entre sí por nombre.

**Schema migration**: cambio a la estructura de las tablas en
Postgres. qrsgen lo hace idempotente con `IF NOT EXISTS` en boot.

**Portabilidad**: facilidad de mover el stack a otro host con
cambios mínimos. qrsgen lo soporta vía `scp` del compose + ajuste del
`.env`.

**Rollback**: volver a una versión anterior de la imagen. qrsgen lo
permite cambiando solo `QRSGEN_VERSION` en `.env`.
