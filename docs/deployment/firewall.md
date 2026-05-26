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
