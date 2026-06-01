# Variables de entorno

## Requeridas

| Variable | Descripción |
|---|---|
| `POSTGRES_HOST` | Host del Postgres (típicamente `postgres` en overlay). |
| `POSTGRES_PASSWORD` | Password del usuario de qrsgen. |
| `DOWNSTREAM_BASE_URL` | URL del sistema downstream (ej: `https://chat.example.com`). |
| `DOWNSTREAM_API_TOKEN` | Token con permisos full sobre la cuenta downstream. |
| `INSTANCE_NAME` | Nombre de la instancia "default" creada al boot (puede coincidir con una existente para no crear nada nuevo). |

## Opcionales (defaults razonables)

| Variable | Default | Notas |
|---|---|---|
| `QRSGEN_VERSION` | `0.46.0` | Tag de imagen Docker (`qrsgen:${QRSGEN_VERSION}`). Última versión: v0.46.0 (history import — backfill 1-30 días de chats al downstream, vía pareo o endpoint admin on-demand). |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_DB` | `bridge` | |
| `POSTGRES_USER` | `postgres` | |
| `DOWNSTREAM_ACCOUNT_ID` | `1` | |
| `DOWNSTREAM_INBOX_ID` | `0` | Inbox fallback cuando una instancia no tiene `inbox_id` configurado. |
| `QRSGEN_API_TOKEN` | (vacío) | Si vacío, **auth desactivada** (modo dev, log WARNING). Genera con `python3 -c "import secrets;print(secrets.token_urlsafe(32))"`. |
| `WEBHOOK_HMAC_SECRET` | (vacío) | Si vacío, el webhook entrante queda abierto. Si set, exige `X-Qrsgen-Signature` HMAC-SHA256. |
| `PUBLIC_STATS_ENABLED` | `false` | Si `true`, habilita `GET /api/public/stats` (sin auth, telemetría agregada). Ver [Telemetría pública](public-stats.md). |
| `PUBLIC_STATS_ALLOW_ORIGIN` | (vacío) | Header `Access-Control-Allow-Origin` para el endpoint público. Ejemplo: `https://rricajos.github.io`. |
| `OUTBOX_ENCRYPTION_KEY` | (vacío) | AES-256 key (32 bytes en base64 estándar) para cifrar payloads del outbox en reposo. Si vacío, payloads se guardan en claro (compat). Ver [Outbox encryption](../security/outbox-encryption.md). Desde v0.27.0. |
| `QRSGEN_GROUP_PREFIX_SENDER` | `true` | Si `true`, mensajes incoming de grupos se postean a downstream con prefijo `` `+<E164> · <~?><Name>` ``\n`<body>` — desde **v0.39.6** el header tiene estructura phone-first: teléfono en E.164 primero, separador middle dot `·` (U+00B7) con espacios a ambos lados, y nombre al final. Toda la línea va envuelta en un **inline code block** (backticks, heredado de v0.39.4) y el teléfono se incluye **siempre** (esté o no guardado el contacto, heredado de v0.39.4). El carácter `~` antes del nombre aparece **solo si el contacto NO está guardado** en la libreta del bot owner (`IsContactSaved == false`); contactos saved (FullName/FirstName) van sin tilde — replica la convención de la UI de WhatsApp (lógica de v0.39.5). v0.39.6 elimina los marcadores `**bold**` y los tabs `\t` heredados de v0.39.2/v0.39.3 porque Chatwoot no procesa bold dentro de inline code y colapsa los tabs a un único espacio dentro del code block. Pon a `false` si tu integración parsea el body raw. Desde v0.29.0 (formato actualizado en v0.30.1, v0.32.0, v0.39.2, v0.39.3, v0.39.4, v0.39.5 y v0.39.6 — v0.39.6 reordena a phone-first con `·` y retira `**bold**` + tabs por comportamiento de render observado en Chatwoot). |
| `QRSGEN_GROUP_HEADER_TTL` | `10m` | Suprime el header de remitente en mensajes consecutivos del mismo participante dentro de un grupo, si caen dentro de este TTL. Replica la convención de WhatsApp (header en el primer msg del burst, nada en los siguientes). `0` desactiva la feature (header siempre). Desde v0.30.0. |
| `QRSGEN_GROUP_HEADER_SEP` | `paragraph` | Separador entre el header `` `+phone · name` `` y el body del mensaje. Default `paragraph` (`\n\n`) por compatibilidad con Chatwoot. Alias soportados: `paragraph`/`p` (`\n\n`), `br` (`<br>` — en Chatwoot renderiza como `<code>br</code>`, no recomendado), `br_self`/`br/` (`<br/>`), `lsep`/`u2028` (Unicode LINE SEPARATOR U+2028), `nl`/`soft` (`\n` — en Chatwoot queda inline), `slash`/`slash_nl` (`\\\n` trailing-backslash hard break), `spaced_br` (` <br> ` con espacios). Cualquier otro valor se usa literal. Permite iterar sin rebuild si tu downstream tiene un renderer markdown distinto. Desde v0.40.1. |
| `QRSGEN_HEADER_TEMPLATE` | `` `$phone · $name` `` | Template del header de sender en group prefix y reactions. Tokens: `$phone` (E.164 con `+`), `$name` (nombre canónico con `~` automático si no saved). Ejemplos: `` `$phone` · **$name** `` (phone en code + bold name), `$phone \| $name` (plano), `[$phone] $name` (con corchetes). El `~` para no-saved está integrado vía `IsContactSaved` — el operador solo elige el wrapper visual. Solo aplica cuando hay AMBOS phone y name; sin uno de ellos cae a formato fijo `` `phone:` `` o `` `~name:` ``. Desde v0.45.0. |
| `QRSGEN_REACTION_HEADER_SEP` | `nl` | Separador entre el header y el verb (`reaccionó con 👍`) en reacciones. Distinto del `QRSGEN_GROUP_HEADER_SEP` porque la reacción es más compacta visualmente. Default `nl` (`\n`) — single line break. Mismos alias que `QRSGEN_GROUP_HEADER_SEP`. Desde v0.45.1. |
| `QRSGEN_HISTORY_IMPORT_ENABLED` | `false` | Si `true`, qrsgen procesa los blobs de `*events.HistorySync` que recibe (al parear instancias o como respuesta on-demand) y postea los msgs históricos al downstream con `created_at` backdated + `source_id=WAID:<id>` para idempotencia. Opt-in por implicaciones de volumen (puede ser cientos de POSTs en una pasada). Desde v0.46.0. |
| `QRSGEN_HISTORY_IMPORT_DAYS` | `7` | Cuántos días hacia atrás importar. Clamped a `[1, 30]`. WhatsApp limita el histórico que devuelve según ajustes del phone — pedir 30 cuando el phone solo guarda 7 da los 7. Desde v0.46.0. |
| `QRSGEN_HISTORY_IMPORT_RATE_PER_SEC` | `5` | Límite de POST/s al downstream durante el import — evita bursts que estresen Chatwoot. Aumentar con cuidado (>20 puede generar rate limits en el backend). Desde v0.46.0. |
| `QRSGEN_AVATAR_SYNC` | `true` | Si `true`, qrsgen descarga la foto de perfil WhatsApp del contacto/grupo al crear el contact en downstream y la sube como avatar via PUT multipart. Fire-and-forget (no bloquea el msg). `false` desactiva la sincronización; los contactos quedan con letter-avatar autogenerado. Desde v0.31.0. |
| `QRSGEN_AVATAR_REFRESH_TTL` | `24h` | Si > 0, contactos existentes se re-chequean cada este TTL para detectar cambios de foto WhatsApp. La comparación usa el `info.ID` (cheap metadata, no descarga); solo se descarga + sube si el ID cambió. `0` desactiva el refresh (solo sync al crear). Desde v0.31.1. |
| `QRSGEN_REACTIONS_SYNC` | `true` | Si `true`, las reacciones (emojis) que los clientes WhatsApp añaden a mensajes se postean al downstream como mensaje incoming. Desde **v0.39.7** el formato se alinea con el prefijo de grupo v0.39.6: `` `+<E164> · <~?>name reaccionó con <emoji>` `` (línea completa en inline code block, phone-first, middle dot `·` como separador, tilde solo si el contacto no está guardado). Reacción retirada (`text=""`): `` `+<E164> · <~?>name quitó su reacción` `` (sin italic — markdown no se procesa dentro de inline code). `false` las ignora silenciosamente. Desde v0.33.0 (formato actualizado en v0.39.7). |
| `QRSGEN_TYPING_SYNC` | `true` | Si `true`, los eventos de typing (composing/paused) que emite WhatsApp se propagan al downstream como `toggle_typing_status` — el agente ve "está escribiendo" en la UI. Throttle interno de 4s para evitar saturar. `false` ignora los eventos. Desde v0.34.0. |
| `QRSGEN_READ_RECEIPTS_SYNC` | `true` | Si `true`, los read receipts WhatsApp (cliente abrió el chat y vio los mensajes del agente) actualizan el `contact_last_seen_at` del conv en el downstream — la UI marca los mensajes como leídos. `false` los ignora. Desde v0.34.1. |
| `QRSGEN_MARK_AS_READ_OUTGOING` | `true` | Si `true`, qrsgen rastrea los WAIDs de mensajes incoming y los marca como leídos en WhatsApp cuando el downstream envía un webhook `conversation_updated` con un nuevo `agent_last_seen_at`. El cliente ve doble check azul. REQUIERE config explícita del downstream para enviar ese evento. Desde v0.39.0. |
| `QRSGEN_RETROACTIVE_NAME_UPDATE` | `true` | Si `true`, qrsgen recuerda mensajes posteados con prefix de grupo "no-saved" (`~Name`) y los reescribe vía PATCH cuando el dueño añade el contacto a su agenda WhatsApp. El histórico en el downstream pasa a mostrar el nombre canónico sin tilde. State in-memory: restart pierde el tracked. Desde v0.40.0. |
| `QRSGEN_RETROACTIVE_CAP_PER_SENDER` | `200` | Cap de mensajes recordados por sender en el tracker de retroactive name update. Al desbordar, los más viejos caen FIFO. >100 da margen para que el update llegue tras horas/días. Desde v0.40.0. |
| `QRSGEN_RETROACTIVE_PERSIST` | `true` | Si `true`, el tracker de retroactive name update persiste sus entries en la tabla `bridge_msg_history` (Postgres). El histórico sobrevive a restart y deploys. `false` → modo in-memory only (v0.40.0): restart pierde tracked. Desde v0.41.0. |
| `QRSGEN_RETROACTIVE_TTL` | `720h` | Cuánto tiempo conservar las entries en DB. Tras este TTL el cron de cleanup (cada 6h) las borra. Default 30 días. Trade-off: más TTL → más capacidad de actualizar mensajes viejos, más espacio en DB. Desde v0.41.0. |
| `DEDUP_ENABLED` | `true` | |
| `DEDUP_WINDOW_MS` | `10000` | Ventana LID-twin dedup. |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `OVERLAY_NETWORK` | `net` | Red docker overlay externa. |
| `PORT` | `3100` | HTTP listener. |

## Glosario

**Variable de entorno**: parámetro de configuración que el proceso lee
al arrancar via `os.Getenv`. qrsgen las parsea con `caarlos0/env`.

**Required vs optional**: una env required hace fallar el boot si está
ausente o vacía. Las optional tienen default.

**Default value**: valor que toma una env si no se pasa. qrsgen usa
defaults razonables para el 90% de casos.

**Backward-compat**: cuando una env nueva opt-in queda desactivada por
default para que despliegues antiguos sigan funcionando sin tocar config.

**Opt-in**: feature que requiere activación explícita (env=true o
similar). Filosofía conservadora — más seguro que opt-out.

**Token / Bearer**: credencial de auth que qrsgen exige en
`Authorization: Bearer ...`. Generar con `secrets.token_urlsafe(32)`.

**HMAC secret**: string usado como clave para firmar/verificar HMACs
del webhook entrante. Debe ser largo y aleatorio.

**DSN** (Data Source Name): cadena de conexión a la base de datos. qrsgen
la construye internamente desde `POSTGRES_HOST/USER/DB/PASSWORD`.
