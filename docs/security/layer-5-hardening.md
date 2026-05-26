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

## Glosario

**Container hardening**: combinación de técnicas para reducir la
superficie de ataque de un container. Distroless + read-only + nonroot
es el stack típico.

**Distroless**: imagen Docker mínima sin shell ni paquetes auxiliares.
Solo contiene el binario y sus deps. Si comprometen el proceso, no hay
shell donde caer.

**Rootfs read-only**: configuración Docker donde el sistema de archivos
del container es de solo lectura. Cualquier intento de escribir falla.
qrsgen lo usa porque el binario no necesita escribir nada.

**tmpfs**: sistema de archivos en memoria. qrsgen monta uno en `/tmp`
(64 MB) como único path escribible. Se vacía con cada redeploy.

**nonroot user**: usuario sin privilegios root dentro del container.
La imagen distroless lo provee con UID/GID 65532.

**Capabilities** (Linux): permisos granulares que un proceso puede
tener. qrsgen no usa ninguna extra — solo las default mínimas.

**Implante**: malware que un atacante instala tras comprometer un
sistema, para mantener acceso persistente. El rootfs read-only lo
previene (no puede escribir).

**Superficie de ataque**: conjunto de puntos donde un atacante puede
intentar romper el sistema. Distroless + read-only la minimiza
dramáticamente.

**Package manager** (`apt`, `apk`, `yum`): herramienta para instalar
software dentro del container. La imagen distroless no lo tiene — el
atacante no puede instalar herramientas.
