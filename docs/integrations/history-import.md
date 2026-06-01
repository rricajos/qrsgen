# History import

Backfill 1-30 días de mensajes históricos de WhatsApp al downstream
(Chatwoot, etc.) sin necesidad de re-parear la instancia. Disponible
desde **v0.46.0**.

## Cuándo usarlo

- Adoptas qrsgen sobre un número con histórico — quieres que el
  agente vea los últimos N días de conversaciones que ocurrieron
  antes de qrsgen.
- Restauras una instancia desde backup y faltan msgs recientes que
  no llegaron a Chatwoot.
- Onboarding de un nuevo cliente — vista inicial poblada en lugar
  de "no hay mensajes todavía".

## Habilitar

Opt-in vía env vars:

```bash
QRSGEN_HISTORY_IMPORT_ENABLED=true
QRSGEN_HISTORY_IMPORT_DAYS=7              # 1..30
QRSGEN_HISTORY_IMPORT_RATE_PER_SEC=5      # throttle POSTs al downstream
QRSGEN_RETROACTIVE_NAME_UPDATE=true       # requerido (msg_history tracker)
```

`RETROACTIVE_NAME_UPDATE=true` (default) es obligatorio porque el
on-demand sync usa el tracker `bridge_msg_history` para resolver el
**anchor** (último msgID conocido del chat) que WhatsApp necesita
como referencia.

## Cómo funciona

```
┌─ qrsgen ─────────────────────┐
│                              │
│ 1. POST /history/import      │
│    {chat: <jid>, count: 50}  │
│                              │
│ 2. Lookup anchor             │
│    FindLastForChat() en      │
│    bridge_msg_history        │
│                              │
│ 3. BuildHistorySyncRequest   │ ──► WhatsApp primary device
│    SendPeerMessage           │     (tu phone)
│                              │
│ 4. WAIT *events.HistorySync  │ ◄── ON_DEMAND response (1-30s)
│    type=ON_DEMAND            │
│                              │
│ 5. Filter por edad           │
│    Sort cronológico          │
│    POST a Chatwoot con       │
│    created_at backdated +    │
│    source_id=WAID            │
│                              │
└──────────────────────────────┘
```

## Endpoints

### `POST /api/instances/:n/history/import` — single chat

Importa el histórico de UN chat específico.

```bash
curl -X POST -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  "$BASE/api/instances/ATC/history/import?chat=34604021705@s.whatsapp.net&count=50"
```

Query params:
- `chat=<jid>` (requerido) — JID del chat
- `count=N` (default 50, max 200) — msgs a pedir al phone
- `timeout_sec=N` (default 30) — cuánto esperar la respuesta

Response 200:
```json
{
  "instance": "ATC",
  "conversations": 1,
  "messages_seen": 47,
  "messages_kept": 17,
  "posted": 17,
  "skipped": 0,
  "errors": 0,
  "oldest_ts": 1778046834,
  "newest_ts": 1778835233
}
```

Error 500 si no hay anchor en el tracker:
```json
{"error": "no message anchor for chat ... — qrsgen needs at least one tracked incoming msg from this chat to request more history; wait for an incoming or send a test msg first"}
```

### `POST /api/instances/:n/history/import-all` — bulk

Itera todos los contactos del inbox y dispara el single-chat import
para cada uno. Secuencial (no paralelo) para no estresar al phone.

```bash
curl -m 600 -X POST -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  "$BASE/api/instances/ATC/history/import-all?count_per_chat=50&timeout_per_chat=30"
```

Query params:
- `count_per_chat=N` (default 50)
- `timeout_per_chat=N` (default 30)

Response 200:
```json
{
  "instance": "ATC",
  "pages": 12,
  "scanned": 171,
  "imported": 3,
  "skipped": 7,
  "no_anchor": 161,
  "errors": 0,
  "total_posted": 45,
  "total_skipped": 0,
  "total_errors": 0
}
```

Campos:
- `scanned` — contactos totales del inbox
- `imported` — chats con sync exitoso
- `skipped` — identifier no parseable / server no soportado
- `no_anchor` — chats sin msg tracked (no es error, esperado)
- `errors` — timeouts/fallos reales
- `total_posted` — sum de msgs posteados en todos los chats

**Tiempos**: para 100 chats con anchor × 5s promedio = ~8 min. Usar
`curl -m 600` para no cortar la conexión.

## Limitaciones

### Anchor requirement

El feature **requiere ≥1 msg incoming tracked en `msg_history`** por
chat para tener anchor real que WhatsApp acepte. La cobertura del
bulk depende de cuántos chats han recibido al menos un msg desde
que `QRSGEN_RETROACTIVE_NAME_UPDATE=true` está activo.

**Mejora prevista (v0.49.0)**: tabla `bridge_chat_anchor` que
trackea el último WAID de TODOS los chats activos
(no solo los con prefix de grupo). Eso sube cobertura del bulk
de ~2% al ~100% en producción.

### Media files

Hoy NO se importan los binarios — solo placeholders:
- `🖼️ [imagen — no importada]`
- `🎥 [video — no importado]`
- `🎤 [nota de voz — no importada]`
- `🎵 [audio — no importado]`
- `📄 [documento — no importado]`
- `🟩 [sticker — no importado]`
- `📍 [ubicación]` (este sí, vía formatLocationContent)

Captions de media SÍ se importan con emoji prefix:
- `🖼️ foto del verano` (caption="foto del verano")

### Profundidad del histórico

WhatsApp solo devuelve lo que el phone tiene guardado. Ajustes
típicos del phone: 30 / 90 / 180 días / 1 año. Pedir 30 días
cuando el phone solo guarda 7 da los 7.

## Idempotencia

Cada msg se postea con `source_id=WAID:<msgID>`. Chatwoot rechaza
con 422 si ya existe — qrsgen lo cuenta como `duplicate` y sigue.
Re-correr el import sobre los mismos msgs no duplica.

## Robustez (v0.48.1+)

POST a Chatwoot con **retry-on-5xx**: hasta 3 intentos con backoff
500ms / 1s. Errores 4xx (excepto 422) NO se reintentan — son
errores permanentes.

## Métricas Prometheus

```
qrsgen_realtime_events_total{feature="history_import", result="ok"}
qrsgen_realtime_events_total{feature="history_import", result="ds_error"}
qrsgen_realtime_events_total{feature="history_import", result="duplicate"}
qrsgen_realtime_events_total{feature="history_import", result="skip_disabled"}
```

PromQL útil:
```promql
# Throughput de import durante un bulk
rate(qrsgen_realtime_events_total{feature="history_import",result="ok"}[1m])

# Tasa de duplicates (debería ser alta en re-runs idempotentes)
rate(qrsgen_realtime_events_total{feature="history_import",result="duplicate"}[5m])
```

## Receta n8n

Workflow típico para backfillear una agenda completa:

```
[HTTP Request: POST /history/import-all] → [If: imported > 0]
                                              ↓ true
                                         [Slack notify: "Histórico
                                          de N msgs en M chats
                                          importado"]
```

## Glosario

**Anchor**: msgID + timestamp que el phone primary usa como
referencia temporal para tirar histórico ANTERIOR. Sin anchor real,
el phone ignora la request en silencio.

**ON_DEMAND**: tipo de `HistorySync` que llega tras un
`BuildHistorySyncRequest`. Otros tipos (`INITIAL_BOOTSTRAP`,
`RECENT`) llegan automáticamente al parear.

**Bulk import**: iteración sobre todos los contactos del inbox
disparando single-chat imports. Sin paralelismo intencionado —
el phone primary procesa una request a la vez.
