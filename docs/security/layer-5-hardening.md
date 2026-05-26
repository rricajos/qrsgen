# Capa 5 — Container hardening

## Qué hace

Tres mecanismos combinados reducen drásticamente la superficie de un RCE:

1. **Imagen distroless** (`gcr.io/distroless/static-debian12:nonroot`) —
   sin shell, sin `apt`/`apk`, sin `curl`/`wget`. Solo el binario qrsgen
   y los certs CA.
2. **Root filesystem read-only** — el binario no escribe a disco; toda
   la persistencia vive en Postgres. Cualquier mutación es indicio de
   compromiso.
3. **Tmpfs en `/tmp`** (64 MB) — único path escribible, en memoria, se
   vacía con cada redeploy.
4. **Usuario `nonroot:nonroot`** — sin capabilities adicionales, no
   puede `chmod`, `chown` ni escalar a root.

## Configuración

```yaml
services:
  qrsgen:
    image: qrsgen:0.23.0-rc1   # distroless en Dockerfile.release
    read_only: true
    volumes:
      - type: tmpfs
        target: /tmp
        tmpfs:
          size: 67108864   # 64 MB
```

## Qué mitiga

Vector #2 (RCE escalation). Un atacante con código en el proceso:

- **No puede instalar herramientas** (rootfs read-only + sin package
  manager).
- **No puede persistir un implante** (solo /tmp escribible, se vacía con
  cada redeploy, además limitado a 64 MB y volátil).
- **No puede escalar a root** (nonroot user, sin capabilities extra).

## Cómo verificarla

```bash
# Intentar escribir en cualquier path fuera de /tmp debe fallar.
sudo docker exec qrsgen_qrsgen.X sh -c 'echo x > /test' 2>&1 || echo "blocked OK"
# (en distroless ni siquiera hay sh, así que se ve doble protección)

# Inspeccionar metadata del container:
sudo docker inspect $(sudo docker ps -q -f name=qrsgen_qrsgen) \
  --format '{{.HostConfig.ReadonlyRootfs}}'   # debe ser true
```
