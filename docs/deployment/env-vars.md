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
| `QRSGEN_VERSION` | `0.23.0-rc1` | Tag de imagen. |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_DB` | `bridge` | |
| `POSTGRES_USER` | `postgres` | |
| `DOWNSTREAM_ACCOUNT_ID` | `1` | |
| `DOWNSTREAM_INBOX_ID` | `0` | Inbox fallback cuando una instancia no tiene `inbox_id` configurado. |
| `QRSGEN_API_TOKEN` | (vacío) | Si vacío, **auth desactivada** (modo dev, log WARNING). Genera con `python3 -c "import secrets;print(secrets.token_urlsafe(32))"`. |
| `WEBHOOK_HMAC_SECRET` | (vacío) | Si vacío, el webhook entrante queda abierto. Si set, exige `X-Qrsgen-Signature` HMAC-SHA256. |
| `PUBLIC_STATS_ENABLED` | `false` | Si `true`, habilita `GET /api/public/stats` (sin auth, telemetría agregada). Ver [Telemetría pública](public-stats.md). |
| `PUBLIC_STATS_ALLOW_ORIGIN` | (vacío) | Header `Access-Control-Allow-Origin` para el endpoint público. Ejemplo: `https://rricajos.github.io`. |
| `DEDUP_ENABLED` | `true` | |
| `DEDUP_WINDOW_MS` | `10000` | Ventana LID-twin dedup. |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `OVERLAY_NETWORK` | `net` | Red docker overlay externa. |
| `PORT` | `3100` | HTTP listener. |
