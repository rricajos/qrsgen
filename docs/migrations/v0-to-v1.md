# Migration guide: v0.28.x → v1.0

Guía consolidada para adoptar v1.0 desde una versión estable
antigua de la línea v0.28.x. La línea v0.40 → v0.53 introdujo
features importantes — esta guía agrupa lo que cambia, lo que
hay que activar, y lo que rompe (poco).

## TL;DR

- **Schema migra automático al boot** — no requiere ALTER manuales.
- **Cambios visuales en mensajes** de Chatwoot (group prefix con
  ~tilde, blockquote en replies, reactions split). Tus parsers en
  downstream necesitan revisión si dependen del formato.
- **Endpoints nuevos** opt-in: history import, group admin,
  retroactive reconcile, jobs async.
- **Backward-compat** en API HTTP y schema Postgres. Todos los
  endpoints v0.28.x siguen funcionando.

## Pre-requisitos

- Backup Postgres de `bridge` database. Las nuevas tablas no
  destruyen nada pero un backup vale.
- Versión mínima Postgres: 14+ (validado contra 16).
- `QRSGEN_API_TOKEN` ya en uso para auth — si no, configurarlo
  antes (los nuevos endpoints son admin-only).

## Schema migrations automáticas

Al boot, qrsgen ejecuta `EnsureXxxSchema` que crea/altera tablas
de forma idempotente. Las nuevas:

| Tabla | Versión | Propósito |
|---|---|---|
| `bridge_msg_history` | v0.41.0 | Retroactive name update (msgID↔WAID, body, name_used) |
| `bridge_chat_anchor` | v0.49.0 | Anchor por chat para history import on-demand |

ALTER TABLE idempotentes (ADD COLUMN IF NOT EXISTS):

| Tabla | Columnas añadidas | Versión |
|---|---|---|
| `bridge_msg_history` | `waid TEXT`, `has_prefix BOOLEAN` | v0.44.0 |

No hay DROP COLUMN ni cambios destructivos. Rows pre-existentes
mantienen comportamiento idéntico (`has_prefix=true` default,
`waid=''`).

## Env vars nuevas (todas opt-in con defaults conservadores)

### Activadas por default (recomendado mantener)

```bash
QRSGEN_RETROACTIVE_NAME_UPDATE=true
QRSGEN_RETROACTIVE_PERSIST=true
QRSGEN_RETROACTIVE_TTL=720h         # 30 días
QRSGEN_RETROACTIVE_CAP_PER_SENDER=200
QRSGEN_MARK_AS_READ_OUTGOING=true   # requiere webhook conversation_updated
QRSGEN_READ_RECEIPTS_SYNC=true
QRSGEN_TYPING_SYNC=true
QRSGEN_REACTIONS_SYNC=true
QRSGEN_AVATAR_SYNC=true
QRSGEN_AVATAR_REFRESH_TTL=24h
QRSGEN_GROUP_PREFIX_SENDER=true
```

### Opt-in (desactivadas, activar según necesidad)

```bash
QRSGEN_HISTORY_IMPORT_ENABLED=false  # set true para backfill
QRSGEN_HISTORY_IMPORT_DAYS=7         # 1..30
QRSGEN_HISTORY_IMPORT_RATE_PER_SEC=5
QRSGEN_GROUP_EVENTS_ENABLED=false    # set true para activity msgs de cambios de grupo
```

### Templates (default ok)

```bash
QRSGEN_HEADER_TEMPLATE="`$phone · $name`"  # group prefix + reactions
QRSGEN_GROUP_HEADER_SEP=paragraph          # \n\n
QRSGEN_REACTION_HEADER_SEP=nl              # \n
QRSGEN_MENTION_TEMPLATE="@$name"           # v0.53.0 — @LID resolution
QRSGEN_REACTION_AS_REPLY=true              # v0.53.2 — reaction como quote-reply
```

## Cambios visuales en mensajes posteados

Si tu downstream tiene scripts que parsean el formato de los
mensajes incoming en Chatwoot, considera estos cambios:

### Group prefix

**Antes** (v0.28.x): `+34604021705 - Pepito\nhola buenas`

**Ahora**: `` `+34604021705 · Pepito`\n\nhola buenas ``
o con tilde si no saved: `` `+34604021705 · ~Pepito` ``

Code block (backticks), middle dot `·` como separador, tilde `~`
para no-saved. Configurable vía `QRSGEN_HEADER_TEMPLATE`.

### Reactions

**Antes**: `**~Pepito** reaccionó con 👍`

**Ahora**:
```
`+34604021705 · ~Pepito`
reaccionó con 👍
```

Header en línea separada (separador `\n`, configurable).

### Quote/reply context (NUEVO)

Cuando alguien responde a un msg en WhatsApp:

```
`+34604021705 · Ricajos`

> `↪ +34600000099 · ~Pepito`
> hola, qué tal?

respuesta del usuario
```

Blockquote con header `↪ +phone · name` + texto citado.

### Group events (con `GROUP_EVENTS_ENABLED=true`)

Activity msgs en la conv del grupo:
- `📝 **Pepito** cambió el nombre del grupo a _X_`
- `➕ **Pepito** añadió a Ana, ~Bea`
- `🔒 **Pepito** restringió la edición del grupo a admins`
- etc.

## Endpoints nuevos

Todos bajo `/api/instances/:name/...` y protegidos por
`QRSGEN_API_TOKEN`. Consulta cada doc para detalles:

### Retroactive name update (v0.40.0+)

- `POST /retroactive/reconcile` — bulk reconcile de la agenda
  (ver `bridge_msg_history` + `Incoming.HandleContactUpdate` en el código fuente)

### History import (v0.46.0+)

- `POST /history/import?chat=<jid>` — single chat
- `POST /history/import-all` — bulk síncrono (bloquea hasta terminar)
- `POST /history/import-all-async` — bulk async, devuelve `job_id`
  ([docs](../integrations/history-import.md))

### Group admin (v0.48.0 + v0.50.0)

- `GET /groups/:jid` — info
- `POST /groups/:jid/name` — rename
- `POST /groups/:jid/topic` — descripción
- `POST /groups/:jid/locked` — toggle admin-only edit
- `POST /groups/:jid/announce` — toggle admin-only msgs
- `POST /groups/:jid/participants` — add/remove/promote/demote
- `POST /groups` — crear
- `DELETE /groups/:jid` — leave
  ([docs](../api/groups.md))

### Jobs (v0.52.0)

- `GET /jobs/:id` — estado + resultado de un async job
- `GET /jobs` — lista todos los jobs vivos

## v0.53.x — mejoras de UX en mensajes posteados

A partir de v0.53.0 se incorporaron tres mejoras que cambian cómo se
ven mensajes con menciones y reacciones en Chatwoot. Si vienes de
v0.52.x el upgrade es transparente (no requiere env nuevas), pero
los formatos pueden parsearse distinto en integraciones downstream.

### LID mentions resueltas con phone fallback (v0.53.0 + v0.53.1)

**Problema previo**: `@140832...` (LID anónimo de WhatsApp) aparecía
crudo en Chatwoot — no sabes a qué número o nombre corresponde.

**Ahora**: qrsgen resuelve el LID en este orden:

1. **Pushname** del contacto si está en el store local.
2. **Saved name** (agenda) si el dueño del bot lo añadió.
3. **Phone** (E.164 con `+`) si la mapping LID→PN existe en el store.
4. **RedactedPhone** (último dígito enmascarado) si el usuario tiene
   privacy LID activada.
5. **LID crudo** como fallback final.

Configurable vía `QRSGEN_MENTION_TEMPLATE` (default `@$name`).

Cuando un grupo se "abre" por primera vez tras el restart, qrsgen
hace `GetGroupInfo` on-demand para poblar el LID store (cache de 1h
por grupo). Implementado en [`internal/bridge/mentions.go`](https://github.com/rricajos/qrsgen).

### Reacciones como quote-reply (v0.53.2)

**Antes**: la reacción aparecía como msg separado, sin contexto
visual del msg target. El agente no sabía a qué se reaccionó.

**Ahora**: qrsgen postea la reacción con
`content_attributes.in_reply_to` apuntando al msg original →
Chatwoot renderiza la reacción como blockquote nativo dentro del
bubble. Default ON. Toggle vía `QRSGEN_REACTION_AS_REPLY=false`
si tu downstream no es Chatwoot.

### Quote-reply outgoing (v0.52.1)

El tracker `bridge_msg_history` ahora también registra mensajes
salientes (`fromMe=true`), no solo incoming. Esto permite que
cuando alguien hace quote-reply en WhatsApp a un mensaje que TÚ
escribiste desde Chatwoot, qrsgen pueda resolver el `in_reply_to`
en el siguiente incoming.

## v0.53.3 — hardening pre-v1.0

Cero cambios de API, tres fixes operativos earned del soak real:

- **Goroutines de history sync ahora se cancelan** al borrar la
  instancia. Antes seguían POSTeando minutos tras `DELETE`, generando
  404s sin valor en logs.
- **`msgHistoryTracker.DropInstance`** llamada automáticamente desde
  `Manager.Delete` vía nuevo callback `SetInstanceDeleteHandler`.
  Libera memoria + filas DB de instancias borradas.
- **Filtro de logs upstream**: el WARN benigno
  `Failed to delete history sync media from server` queda suprimido
  (cientos de líneas por sync, sin valor accionable).

## Orden recomendado de adopción

Si vienes de v0.28.x y quieres adoptar v1.0 sin riesgos:

1. **Backup Postgres** (`pg_dump bridge > bridge_v0.28.sql`).
2. **Deploy v1.0 sin tocar env vars opt-in** — schema migra
   automáticamente al boot. Los formatos visuales cambian pero
   los flujos básicos (incoming/outgoing) funcionan idéntico.
3. **Verificar 24h** — sin warnings/errors recurrentes en logs,
   sin spikes anómalos en métricas.
4. **Activar history import** (`HISTORY_IMPORT_ENABLED=true`,
   `DAYS=7`) si quieres backfill de N días.
5. **Activar group events** (`GROUP_EVENTS_ENABLED=true`) si
   quieres ver cambios de grupos como activity msgs.
6. **Probar group admin endpoints** con un grupo donde el bot
   sea admin (rename como test seguro).

## Rollback

Si necesitas volver a v0.28.x:

1. `docker service update --image qrsgen:0.28.x qrsgen_qrsgen`
2. Las tablas nuevas (`bridge_msg_history`, `bridge_chat_anchor`)
   quedan huérfanas — no rompen v0.28.x, pero se pueden borrar
   manualmente si quieres:
   ```sql
   DROP TABLE IF EXISTS bridge_msg_history;
   DROP TABLE IF EXISTS bridge_chat_anchor;
   ```
3. Las columnas añadidas a tablas existentes (`waid`, `has_prefix`
   en `bridge_msg_history`) tampoco rompen v0.28.x — quedan sin
   uso.

Rollback es seguro pero pierdes el state del retroactive update,
history import tracking y chat anchors. Los mensajes ya posteados
en Chatwoot permanecen.

## Métricas Prometheus nuevas

Mantén tu Grafana / alerting al día con estos labels:

```
qrsgen_realtime_events_total{feature="retroactive_name", result=...}
qrsgen_realtime_events_total{feature="history_import", result=...}
qrsgen_realtime_events_total{feature="group_event", result=...}
qrsgen_realtime_events_total{feature="reaction|typing|avatar|read_receipt", ...}
```

Results comunes: `ok`, `ds_error`, `wa_error`, `wa_miss`,
`skip_disabled`, `skip_fullsync`, `skip_empty_name`, `duplicate`.

PromQL útil para detectar regresiones tras el upgrade:

```promql
# Tasa de errores downstream agrupada por feature
sum by (feature) (rate(qrsgen_realtime_events_total{result="ds_error"}[5m]))

# Volumen de retroactive updates (espera spikes al activar)
sum by (instance) (rate(qrsgen_realtime_events_total{feature="retroactive_name",result="ok"}[5m]))
```

## Soporte

- CHANGELOG.md tiene los detalles de cada release individual.
- Issues: https://github.com/rricajos/qrsgen/issues
- Docs operacionales: [`docs/operations/`](../operations/index.md)
