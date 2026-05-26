# Firewall egress

`/etc/systemd/system/qrsgen-firewall.service` ejecuta el watcher que:

- Aplica iptables rules con allowlist (overlay + Meta CIDRs en :443).
- Escucha `docker events` para detectar restart de qrsgen.
- Re-aplica reglas con la nueva IP del container.

## Install

```bash
sudo install -m 0755 firewall.sh /opt/qrsgen-stack/firewall.sh
sudo install -m 0755 qrsgen-firewall-watcher.sh /opt/qrsgen-stack/qrsgen-firewall-watcher.sh
sudo install -m 0644 qrsgen-firewall.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qrsgen-firewall.service
```

## Verificar

```bash
sudo /opt/qrsgen-stack/firewall.sh status
journalctl -u qrsgen-firewall.service -n 20
```

Detalle de la allowlist y el modelo de amenaza:
[Security capa 3](../security/layer-3-firewall.md).

## Glosario

**Egress firewall**: filtro del tráfico saliente. Opuesto a ingress
(entrante).

**iptables**: herramienta clásica de Linux para definir reglas de
firewall a nivel kernel.

**Watchdog**: proceso que vigila eventos del sistema y reacciona. Aquí
escucha `docker events` y re-aplica iptables si qrsgen reinicia.

**docker events**: stream de eventos de Docker (start, stop, restart)
en tiempo real. Filtable por container/service.

**Allowlist**: lista de destinos permitidos. Todo lo demás se bloquea.
Opuesto a blocklist.

**CIDR**: notación de rango de IPs (`192.168.0.0/16`).

**LOG + DROP**: política iptables que loguea el paquete antes de
descartarlo. Útil para observabilidad.

**Rate-limited LOG**: limitar cuántos logs por segundo para no
inundar dmesg.
