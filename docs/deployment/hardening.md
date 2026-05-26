# Hardening del container

El compose viene con tres mecanismos de hardening activos por defecto
(ver [Security capa 5](../security/layer-5-hardening.md)):

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

## Update strategy

```yaml
deploy:
  update_config:
    parallelism: 1
    order: stop-first         # ← previene WhatsApp JID race
    delay: 5s
    monitor: 5s
    failure_action: pause
```

`order: stop-first` significa que el container viejo para **antes** de
que el nuevo arranque — esto evita que dos containers compitan por la
misma sesión WhatsApp durante el rollout (WhatsApp kicea ambos si
detecta el conflicto). El precio es ~15s de downtime por deploy,
**cubierto por el outbox** (5 min TTL).

## Glosario

**Hardening**: aplicar capas defensivas para reducir la superficie de
ataque de un sistema.

**Read-only rootfs**: filesystem del container que no permite
escrituras. Cualquier intento falla — buena señal de compromiso si
ocurre.

**tmpfs**: filesystem en memoria. qrsgen monta `/tmp` así como único
path escribible. Se vacía con cada redeploy.

**nonroot user**: usuario sin privilegios root dentro del container.
Distroless lo provee con UID 65532.

**Update strategy**: cómo Docker Swarm pasa de la versión vieja a la
nueva durante un deploy.

**stop-first**: política donde el container viejo se detiene antes de
arrancar el nuevo. Más downtime pero evita race conditions (qrsgen lo
necesita por la sesión única de WhatsApp).

**start-first**: política opuesta (default). El nuevo arranca, valida
health, y solo entonces se mata el viejo. No sirve para qrsgen.

**Rolling update**: actualización gradual de réplicas. No aplica a
qrsgen porque corre con replicas=1.

**Race condition**: situación donde dos procesos compiten por un
recurso compartido y el resultado depende del orden temporal. qrsgen lo
evita con stop-first.
