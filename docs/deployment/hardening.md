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
