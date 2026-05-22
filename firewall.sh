#!/bin/bash
# Egress firewall para el container qrsgen.
# Restringe outbound a:
#   - Established/related (return traffic, OK)
#   - DNS Docker (127.0.0.11)
#   - Overlay LAN (10.0.0.0/8 + 172.16.0.0/12)
#   - Localhost del VPS (37.60.252.104) — temporal: para que qrsgen alcance el downstream vía DNS público
#   - Meta/WhatsApp CIDRs (allowlist hardcoded de rangos AS32934)
# DROP el resto.
#
# Idempotente: borra reglas previas con label qrsgen-egress y re-aplica.
#
# Uso:
#   sudo /opt/qrsgen-stack/firewall.sh apply   # aplica DROP
#   sudo /opt/qrsgen-stack/firewall.sh log     # solo loguea (modo testing, no bloquea)
#   sudo /opt/qrsgen-stack/firewall.sh flush   # quita todas las reglas qrsgen
#   sudo /opt/qrsgen-stack/firewall.sh status  # muestra reglas activas

set -e

# Meta/WhatsApp/Facebook IP ranges (AS32934). Actualizar si Meta cambia rangos.
META_CIDRS=(
  "31.13.24.0/21"
  "31.13.64.0/19"
  "57.144.0.0/14"
  "66.220.144.0/20"
  "69.63.176.0/20"
  "69.171.224.0/19"
  "102.132.96.0/20"
  "129.134.0.0/17"
  "157.240.0.0/16"
  "163.70.128.0/17"
  "173.252.64.0/19"
  "179.60.192.0/22"
  "185.60.216.0/22"
  "204.15.20.0/22"
)
VPS_PUBLIC_IP="37.60.252.104"

CHAIN="QRSGEN_EGRESS"

cmd_flush() {
  # Borra TODAS las refs a QRSGEN_EGRESS en FORWARD (con o sin source IP).
  # Iteramos por line-number de mayor a menor para que los índices no cambien.
  local lines
  lines=$(iptables -L FORWARD --line-numbers -n 2>/dev/null | awk '/QRSGEN_EGRESS/ {print $1}' | sort -rn)
  for ln in $lines; do
    iptables -D FORWARD "$ln" 2>/dev/null || true
  done
  iptables -F "$CHAIN" 2>/dev/null || true
  iptables -X "$CHAIN" 2>/dev/null || true
  echo "flushed."
}

get_qrsgen_ip() {
  # En Docker Swarm el container tiene 2 ifaces: eth0 (overlay 10.x) + eth1
  # (docker_gwbridge 172.x). El outbound a internet sale por eth1 con SNAT.
  # Durante swarm rescheduling pueden coexistir 2 containers brevemente —
  # iteramos hasta encontrar uno running cuya PID exista.
  local CIDS CID PID IP
  CIDS=$(docker ps --filter "status=running" --format '{{.ID}} {{.Names}}' | grep qrsgen_qrsgen | awk '{print $1}')
  for CID in $CIDS; do
    PID=$(docker inspect -f '{{.State.Pid}}' "$CID" 2>/dev/null)
    [ -z "$PID" ] || [ "$PID" = "0" ] && continue
    IP=$(nsenter -t "$PID" -n ip route get 8.8.8.8 2>/dev/null | grep -oE 'src [0-9.]+' | awk '{print $2}')
    if [ -n "$IP" ]; then
      echo "$IP"
      return 0
    fi
  done
  return 1
}

cmd_apply() {
  local MODE="${1:-DROP}"
  local IP
  IP=$(get_qrsgen_ip)
  if [ -z "$IP" ]; then
    echo "ERROR: qrsgen container not found"; exit 1
  fi
  echo "qrsgen container IP (docker_gwbridge): $IP"
  echo "mode: $MODE"

  cmd_flush
  iptables -N "$CHAIN" 2>/dev/null || iptables -F "$CHAIN"

  # 1) Established/related → permitir (return traffic)
  iptables -A "$CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN

  # 2) DNS Docker (127.0.0.11) → permitir
  iptables -A "$CHAIN" -p udp -d 127.0.0.11 --dport 53 -j RETURN
  iptables -A "$CHAIN" -p tcp -d 127.0.0.11 --dport 53 -j RETURN

  # 3) LAN (overlay/bridge docker) → permitir
  iptables -A "$CHAIN" -d 10.0.0.0/8 -j RETURN
  iptables -A "$CHAIN" -d 172.16.0.0/12 -j RETURN

  # 4) Loopback al VPS (útil si el downstream se resuelve por DNS público del propio VPS)
  iptables -A "$CHAIN" -d "$VPS_PUBLIC_IP" -j RETURN

  # 5) Meta CIDRs en 443 → permitir
  for cidr in "${META_CIDRS[@]}"; do
    iptables -A "$CHAIN" -p tcp -d "$cidr" --dport 443 -j RETURN
  done

  # 6) Resto → según MODE
  if [ "$MODE" = "LOG" ]; then
    iptables -A "$CHAIN" -j LOG --log-prefix "QRSGEN-DROP: " --log-level 6
    iptables -A "$CHAIN" -j RETURN
  else
    iptables -A "$CHAIN" -j LOG --log-prefix "QRSGEN-DROP: " --log-level 6 -m limit --limit 5/min
    iptables -A "$CHAIN" -j DROP
  fi

  # Insertar QRSGEN_EGRESS al inicio del FORWARD, filtrando por source IP
  iptables -I FORWARD -s "$IP" -j "$CHAIN"

  echo "applied (mode=$MODE) — source IP $IP filtered through $CHAIN"
}

cmd_status() {
  echo "--- FORWARD references ---"
  iptables -L FORWARD -n -v | grep -E "Chain|QRSGEN" || true
  echo "--- $CHAIN rules ---"
  iptables -L "$CHAIN" -n -v 2>/dev/null || echo "(no chain)"
}

case "${1:-status}" in
  apply)  cmd_apply DROP ;;
  log)    cmd_apply LOG ;;
  flush)  cmd_flush ;;
  status) cmd_status ;;
  *) echo "usage: $0 [apply|log|flush|status]"; exit 1 ;;
esac
