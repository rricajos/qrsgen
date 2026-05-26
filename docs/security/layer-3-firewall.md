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

## Glosario

**Egress firewall**: filtro que controla el tráfico **saliente** de un
host o container (opuesto a ingress, que controla el entrante). Útil
para contener un proceso comprometido.

**iptables**: herramienta clásica de Linux para definir reglas de
firewall a nivel kernel (Netfilter). qrsgen la usa para la cadena
`QRSGEN_EGRESS`.

**Cadena (chain)**: estructura de iptables que agrupa reglas. La cadena
`QRSGEN_EGRESS` mantiene la allowlist de qrsgen.

**Allowlist**: enfoque de "permitir por defecto a una lista corta,
bloquear todo lo demás". Opuesto a blocklist. Más seguro pero requiere
mantenimiento (al cambiar las CIDRs de Meta hay que actualizar).

**Blocklist**: enfoque opuesto: "permitir todo excepto X conocido malo".
Menos seguro porque hay que enumerar exhaustivamente lo malo.

**CIDR**: notación para rangos de IPs (`192.168.0.0/16` = 65536 IPs).
Las CIDRs de Meta están publicadas en sus tools y qrsgen mantiene una
copia en `firewall.sh`.

**AS** (Autonomous System): grupo de bloques de IPs gestionados por una
organización. AS32934 = Meta/Facebook.

**Established/related**: estados de conexión TCP. Permitir tráfico
relacionado con conexiones ya establecidas evita romper sesiones
salientes (Meta responde, NAT lo permite).

**LOG + DROP** (vs DROP solo): además de bloquear el paquete, el kernel
logguea el intento. qrsgen lo limita a 5/min para evitar inundar logs.

**docker_gwbridge**: bridge interno de Docker Swarm por donde sale el
tráfico de los containers. La IP del container ahí cambia tras restart,
por eso hace falta el watchdog que re-aplica las reglas.

**C2** (Command & Control): servidor que un atacante usa para
controlar malware en máquinas comprometidas. El egress firewall lo
bloquea (Meta no es C2; un IP arbitrario sí lo sería).
