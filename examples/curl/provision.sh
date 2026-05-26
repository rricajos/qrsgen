#!/usr/bin/env bash
# Provisioning end-to-end con curl puro.
#
# Uso:
#   QRSGEN_TOKEN=... ./provision.sh whatsapp-main
#
# El nombre de la instancia es libre y suele coincidir con el nombre del
# canal/inbox en el downstream (e.g. "whatsapp-main", "whatsapp-sales",
# "support-eu"). Las llamadas posteriores lo referencian en la URL.
#
# Lo que hace:
#   1. Crea una instancia con tu nombre
#   2. Polleo el estado hasta que esté en qr_pending o ready
#   3. Descarga el PNG del QR
#   4. Muestra cómo acabaría enviando un msg

set -euo pipefail

INSTANCE="${1:?usage: $0 INSTANCE_NAME}"
QRSGEN_URL="${QRSGEN_URL:-http://qrsgen:3100}"
QRSGEN_TOKEN="${QRSGEN_TOKEN:?QRSGEN_TOKEN env var required}"
WEBHOOK_URL="${EVENTS_WEBHOOK_URL:-https://your-orchestrator.example.com/webhook/qrsgen-events}"

auth=(-H "Authorization: Bearer $QRSGEN_TOKEN")

echo "→ Creating instance $INSTANCE..."
curl -sf "${auth[@]}" -H 'Content-Type: application/json' \
  -X POST "$QRSGEN_URL/api/instances" \
  -d "{\"name\":\"$INSTANCE\",\"events_webhook_url\":\"$WEBHOOK_URL\"}" \
  | jq .

echo
echo "→ Polling state..."
for i in $(seq 1 30); do
  state=$(curl -sf "${auth[@]}" "$QRSGEN_URL/api/instances/$INSTANCE" | jq -r '.state')
  echo "  [$i] state=$state"
  case "$state" in
    qr_pending|ready|connected|paired) break ;;
  esac
  sleep 2
done

if [ "$state" = "qr_pending" ]; then
  echo
  echo "→ Fetching QR PNG..."
  curl -sf "${auth[@]}" "$QRSGEN_URL/api/instances/$INSTANCE/qr" -o "/tmp/${INSTANCE}_qr.png"
  echo "  saved → /tmp/${INSTANCE}_qr.png"
  echo "  escanea desde WhatsApp móvil > Dispositivos vinculados"
fi

echo
echo "Cuando esté conectada, para enviar un msg saliente:"
cat <<EOF
  curl -X POST "$QRSGEN_URL/api/instances/$INSTANCE/webhook" \\
    -H 'Content-Type: application/json' \\
    -d '{
      "event": "message_created",
      "message_type": "outgoing",
      "content": "Hola desde qrsgen",
      "conversation": {
        "id": 1,
        "meta": { "sender": { "identifier": "34600000000@s.whatsapp.net" } }
      },
      "id": 42,
      "private": false
    }'
EOF
