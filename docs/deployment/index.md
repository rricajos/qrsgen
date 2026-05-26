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
