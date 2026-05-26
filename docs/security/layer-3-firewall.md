# Capa 3 — Egress firewall (iptables)

## Qué hace

`firewall.sh` mantiene una cadena `QRSGEN_EGRESS` en iptables que filtra
todo el outbound del container qrsgen. Lo que no esté en la allowlist se
DROPpea con LOG (rate-limited 5/min).

## Allowlist

- Established / related (TCP return traffic).
- DNS Docker (`127.0.0.11:53`).
- Overlay LAN (`10.0.0.0/8`, `172.16.0.0/12`).
- IP pública del VPS (para llamadas vía DNS público al downstream si
  fuera necesario).
- Meta/Facebook CIDRs en `:443` (14 rangos AS32934).

## Operación

```bash
sudo /opt/qrsgen-stack/firewall.sh apply    # aplica DROP
sudo /opt/qrsgen-stack/firewall.sh log      # solo LOG (testing/dry-run)
sudo /opt/qrsgen-stack/firewall.sh flush    # quita todas las reglas
sudo /opt/qrsgen-stack/firewall.sh status   # muestra reglas + counters
```

El watchdog systemd `qrsgen-firewall.service` re-aplica automáticamente
cuando docker reporta `container start` para qrsgen — necesario porque la
IP del container en `docker_gwbridge` cambia tras restarts.

## Qué mitiga

Vector #2 (RCE en qrsgen): un atacante con shell en el proceso no puede
exfiltrar datos a un C2 arbitrario en internet. Solo Meta y la LAN están
permitidos. Para minar crypto, hacer reverse shell, o subir el dump de DB
a un servicio externo, necesitaría romper también esta capa.

## Cómo verificarla

```bash
# Desde dentro del container, intentar conexión a un IP arbitrario debe fallar.
sudo docker exec qrsgen_qrsgen.X /app/qrsgen -healthcheck   # localhost OK
# Para conexiones outbound bloqueadas, verás drops:
sudo dmesg | grep QRSGEN-DROP | tail -5
```
