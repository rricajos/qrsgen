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
| `QRSGEN_VERSION` | `0.39.4` | Tag de imagen Docker (`qrsgen:${QRSGEN_VERSION}`). |
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
| `QRSGEN_GROUP_PREFIX_SENDER` | `true` | Si `true`, mensajes incoming de grupos se postean a downstream con prefijo `**~<Name>**<tabs>+<E164>\n<body>` (nombre bold + tab(s) `\t` U+0009 + teléfono en plano sin code block) para distinguir participantes en una misma conv. El número de tabs es variable: **2** si `utf8.RuneCountInString(name) ≤ 12`, **1** si `> 12` — alinea los teléfonos visualmente. Si el contacto está guardado en la libreta del bot owner (v0.32.0), el teléfono se omite y solo queda `**~<Name>**`. Pon a `false` si tu integración parsea el body raw. Desde v0.29.0 (formato actualizado en v0.30.1, v0.32.0, v0.39.2 y v0.39.3). |
| `QRSGEN_GROUP_HEADER_TTL` | `10m` | Suprime el header de remitente en mensajes consecutivos del mismo participante dentro de un grupo, si caen dentro de este TTL. Replica la convención de WhatsApp (header en el primer msg del burst, nada en los siguientes). `0` desactiva la feature (header siempre). Desde v0.30.0. |
| `QRSGEN_AVATAR_SYNC` | `true` | Si `true`, qrsgen descarga la foto de perfil WhatsApp del contacto/grupo al crear el contact en downstream y la sube como avatar via PUT multipart. Fire-and-forget (no bloquea el msg). `false` desactiva la sincronización; los contactos quedan con letter-avatar autogenerado. Desde v0.31.0. |
| `QRSGEN_AVATAR_REFRESH_TTL` | `24h` | Si > 0, contactos existentes se re-chequean cada este TTL para detectar cambios de foto WhatsApp. La comparación usa el `info.ID` (cheap metadata, no descarga); solo se descarga + sube si el ID cambió. `0` desactiva el refresh (solo sync al crear). Desde v0.31.1. |
| `QRSGEN_REACTIONS_SYNC` | `true` | Si `true`, las reacciones (emojis) que los clientes WhatsApp añaden a mensajes se postean al downstream como mensaje incoming con formato `**~Name** reaccionó con 👍`. `false` las ignora silenciosamente. Desde v0.33.0. |
| `QRSGEN_TYPING_SYNC` | `true` | Si `true`, los eventos de typing (composing/paused) que emite WhatsApp se propagan al downstream como `toggle_typing_status` — el agente ve "está escribiendo" en la UI. Throttle interno de 4s para evitar saturar. `false` ignora los eventos. Desde v0.34.0. |
| `QRSGEN_READ_RECEIPTS_SYNC` | `true` | Si `true`, los read receipts WhatsApp (cliente abrió el chat y vio los mensajes del agente) actualizan el `contact_last_seen_at` del conv en el downstream — la UI marca los mensajes como leídos. `false` los ignora. Desde v0.34.1. |
| `QRSGEN_MARK_AS_READ_OUTGOING` | `true` | Si `true`, qrsgen rastrea los WAIDs de mensajes incoming y los marca como leídos en WhatsApp cuando el downstream envía un webhook `conversation_updated` con un nuevo `agent_last_seen_at`. El cliente ve doble check azul. REQUIERE config explícita del downstream para enviar ese evento. Desde v0.39.0. |
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
