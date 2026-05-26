# Migrar desde Evolution API

[Evolution API](https://github.com/EvolutionAPI/evolution-api) es una
plataforma popular (TypeScript con Prisma + Postgres) muy usada en
LATAM. La migración a qrsgen es directa porque ambas exponen REST y
guardan sesiones en Postgres.

## Estructura de datos en Evolution

Evolution usa **Postgres** (o MySQL en versiones antiguas) con schema
Prisma. Tablas relevantes:

```
Instance                  -- una fila por sesión WhatsApp
├── id (UUID PK)
├── name                  -- identificador único (= name en qrsgen)
├── token                 -- token per-instance (auth API)
├── connectionStatus      -- "open" | "close" | "connecting"
├── number                -- número (cuando está pareado)
├── ownerJid              -- JID del owner
└── createdAt, updatedAt

Webhook                   -- config webhook per-instance
├── id, instanceId (FK)
├── url
├── events (JSON: ["MESSAGES_UPSERT", "QRCODE_UPDATED", ...])
├── webhookByEvents (bool, ruta por tipo de evento)
└── enabled

Message                   -- histórico completo
├── id, instanceId (FK)
├── keyId, keyRemoteJid   -- WAID + JID destino
├── message (JSONB)       -- payload completo whatsmeow
├── messageType
├── status
└── messageTimestamp

Contact                   -- contactos vistos
├── remoteJid (PK compuesta con instanceId)
├── pushName, profilePicUrl
└── isMyContact

Chat                      -- conversaciones
├── remoteJid, instanceId
├── unreadCount
└── lastMessageTimestamp

Session                   -- whatsmeow Multi-Device session state (NO PORTABLE)
PreKey, SenderKey, ...    -- claves crypto whatsmeow (NO PORTABLES)
```

**Lo que SÍ se migra a qrsgen**: `Instance.name` + `Webhook.url` (→
provisioning automático).

**Lo que NO**: `Session`, claves crypto. Aunque ambos usan whatsmeow
internamente, los formatos de serialización pueden diferir entre
versiones — no es seguro reusarlos.

**Lo que es opcional migrar**: `Message` histórico. qrsgen no guarda
mensajes intencionalmente (eso es trabajo del downstream). Si tu
downstream necesita el histórico, exporta `Message` directamente a su
DB sin pasar por qrsgen.

## Mapeo conceptual

| Evolution API | qrsgen |
|---|---|
| `instance_name` | `name` de la instancia |
| `token` (per-instance) | Bearer global `QRSGEN_API_TOKEN` |
| Webhook config por instancia | `events_webhook_url` de la instancia |
| Routing por inbox externo | `inbox_id` |
| Sesión WhatsApp en `whatsapp_sessions` (filesystem o DB) | **No portable — re-pairing obligatorio** |
| Webhook receiver del downstream | mismo endpoint, solo cambia source URL |

## Receta

### 1. Extraer inventory desde Evolution

```bash
# Asume Evolution API corriendo en http://evolution.local:8080
EVO_URL="http://evolution.local:8080"
EVO_TOKEN="tu_evolution_api_key"

# Listar instancias
curl -sS -H "apikey: $EVO_TOKEN" \
  "$EVO_URL/instance/fetchInstances" | jq '.[] | {
    name: .instanceName,
    webhook: .webhook,
    inbox_id: (.integration.inbox_id // null)
  }' > /tmp/evolution-instances.json
```

### 2. Generar plan JSON para qrsgen

```python
# tools/migrate/from-evolution.py (ejemplo)
import json

with open("/tmp/evolution-instances.json") as f:
    evo = [json.loads(line) for line in f]   # ajustar según el JSON exacto

plan = {
    "instances": [
        {
            "name": e["name"],
            "events_webhook_url": "https://your-new-downstream.example.com/qrsgen-events",
            "inbox_id": e.get("inbox_id"),
            "owner_tag": "migrated-from-evolution",
        }
        for e in evo
    ]
}

with open("/tmp/qrsgen-plan.json", "w") as f:
    json.dump(plan, f, indent=2)

print(f"Plan generated: {len(plan['instances'])} instances")
```

### 3. Revisar el plan (importante)

```bash
cat /tmp/qrsgen-plan.json | jq '.instances[] | .name'
# Edita manualmente los que no quieras migrar.
```

### 4. Aplicar el plan en qrsgen

```bash
QRSGEN_URL=http://qrsgen:3100 \
QRSGEN_TOKEN="$QRSGEN_API_TOKEN" \
python3 tools/migrate/bulk-provision.py /tmp/qrsgen-plan.json
```

Tras esto, todas las instancias existen en qrsgen pero están en estado
`qr_pending`. Ningún mensaje fluye todavía.

### 5. Re-pairing (manual, no automatizable)

Cada usuario tiene que **re-escanear su QR** desde la app de WhatsApp.

Workflow recomendado:
- Comunica a tus clientes/agentes el plan de migración con
  ventana concreta (ej. "viernes 22:00 a sábado 09:00").
- Mantén Evolution corriendo hasta que la mayoría haya migrado.
- Cuando un usuario reescanee, qrsgen empieza a emitir lifecycle
  `paired` → `connected`. Tu downstream redirige a qrsgen para esa
  instancia.

Puedes ver el progreso con:

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances | jq '.[] | {name, state}'
```

### 6. Webhook switch en el downstream

Cuando todas las instancias estén en `ready`, actualiza el downstream
para que el `webhook_url` apunte a qrsgen en lugar de a Evolution:

```
Antes (Evolution):
  webhook_url = http://evolution:8080/instance/${INSTANCE}/webhook

Después (qrsgen):
  webhook_url = http://qrsgen:3100/api/instances/${INSTANCE}/webhook
```

### 7. Apagar Evolution

Una vez todo está en `ready` y los webhooks redirigidos, puedes parar
el container de Evolution. **Mantén su Postgres por unas semanas** por
si necesitas consultar historial.

### 8. Validar

```bash
python3 tools/migrate/validate.py
# Salida:
# ✓ whatsapp-main      ready    +34650367855
# ✓ whatsapp-sales     ready    +34602253018
# ✗ whatsapp-legacy    qr_pending (no escaneada todavía)
```

## Migrar histórico de mensajes (opcional)

Si necesitas conservar el histórico de Evolution:

```sql
-- En el Postgres de Evolution
\copy (SELECT instance_name, contact_jid, content, message_type, created_at
       FROM messages WHERE created_at > NOW() - INTERVAL '30 days')
   TO '/tmp/evolution-messages.csv' CSV HEADER;
```

Luego importa en el sistema downstream (Chatwoot, tu CRM, etc.).
qrsgen NO guarda historial de mensajes intencionalmente — eso es
responsabilidad del downstream. Si quieres que qrsgen sepa de
mensajes pasados solo a efectos de dedup, no es necesario importarlos
(el dedup es por contenido, no por id histórico).

## Diferencias operativas a tener en cuenta

| Aspecto | Evolution | qrsgen |
|---|---|---|
| Auth API | `apikey` header per-instance | Bearer global |
| Webhook URL del downstream | Por instancia, vía config | Misma para todas, parametrizada por path |
| Telemetría | Logs PHP (Laravel) | Prometheus + lifecycle events HTTP |
| Backups | Manual / volúmenes | systemd timer + retention |
| Multi-Device dedup | No nativo | LID-twin dedup builtin |
| Ban prevention | Reactivo (cuando ya ocurrió) | Proactivo (BanWatcher) |

## Glosario

**Evolution API**: plataforma WhatsApp basada en Baileys con UI Laravel.
Popular en LATAM por su facilidad de instalación.

**Instance** (Evolution): equivalente a una instancia qrsgen — una
sesión WhatsApp.

**apikey** (Evolution): header de auth equivalente al Bearer token de
qrsgen, pero con granularidad per-instance.

**Webhook config** (Evolution): URL configurada por instancia para
recibir eventos. En qrsgen es `events_webhook_url`.

**Bulk provisioning**: crear muchas instancias en qrsgen de una sola
ejecución, generalmente a partir de un plan JSON.

**Re-pairing window**: período acordado con los usuarios para que
re-escaneen los QRs durante la migración.

**Histórico portable**: datos que el sistema origen exponía y que
puedes guardar/importar en el destino. qrsgen no guarda historial —
ese trabajo es del downstream.
