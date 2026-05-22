#!/bin/bash
# Watcher: escucha docker events y re-aplica el firewall cuando qrsgen
# arranca o se reschedula. Idempotente — re-aplicar es seguro.

FIREWALL="/opt/qrsgen-stack/firewall.sh"

apply_safely() {
  if "$FIREWALL" apply >> /var/log/qrsgen-firewall.log 2>&1; then
    echo "$(date -Is) firewall applied OK" >> /var/log/qrsgen-firewall.log
  else
    echo "$(date -Is) firewall apply FAILED" >> /var/log/qrsgen-firewall.log
  fi
}

# Apply inicial al arrancar el servicio (cubre host reboot)
apply_safely

# Listen forever; cualquier start de un container qrsgen → re-aplicar
docker events \
  --filter 'type=container' \
  --filter 'event=start' \
  --format '{{.Actor.Attributes.name}}' | \
while read -r name; do
  case "$name" in
    *qrsgen_qrsgen*)
      echo "$(date -Is) detected qrsgen container start: $name" >> /var/log/qrsgen-firewall.log
      # Delay para que swarm termine reschedule + gwbridge asigne IP
      sleep 8
      apply_safely
      ;;
  esac
done
