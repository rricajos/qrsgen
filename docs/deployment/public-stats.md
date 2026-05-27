# Telemetría pública

qrsgen incluye un endpoint opt-in pensado para que una landing estática
(GitHub Pages, Cloudflare Pages, Netlify…) muestre contadores en vivo:
instancias conectadas, totales de mensajes, etc.

## Endpoint

```
GET /api/public/stats
```

Sin auth. Cuando `PUBLIC_STATS_ENABLED=false` (default) devuelve `403`.

**Response:**

```json
{
  "instances_connected":  4,
  "instances_total":      4,
  "installations_active": 4,
  "installations_total":  37,
  "tenants_total":        2,
  "qrs_scanned_total":    42,
  "messages_in_total":    152340,
  "messages_out_total":   178921,
  "version": "0.24.2",
  "last_updated": "2026-05-26T11:30:00Z"
}
```

**Cache 30s in-memory** (desde v0.24.2): la landing hace polling cada
10s; el primer hit dentro de cada ventana de 30s reconstruye el payload
desde DB, los siguientes ~2 lo sirven cacheados. `last_updated` refleja
el momento en que se computó el payload, no la request actual — un
desfase de hasta 30s es esperado y aceptable para una landing.

Las parejas son **live** vs **histórico**:

- `instances_connected` / `instances_total` — snapshot live: lo que el
  proceso tiene en memoria ahora mismo.
- `installations_active` / `installations_total` — datos persistidos:
  lo que ha existido en DB en cualquier momento.

Campos:

| Campo | Significado |
|---|---|
| `instances_connected` | Cuántas instancias están **ahora mismo** en estado `ready` o `connected` (snapshot live). |
| `instances_total` | Total de instancias **registradas en el proceso** ahora mismo (incluye `qr_pending`, `disconnected`, etc.). Snapshot live. |
| `installations_active` | Instancias en DB con `jid` configurado — han llegado a parearse al menos una vez y siguen registradas. |
| `installations_total` | Histórico acumulado de instancias que han existido en algún momento — incluye las ya borradas via DELETE (sobrevive a borrar porque cuenta entradas distintas en `bridge_audit_log`, que es append-only). |
| `tenants_total` | Filas en `bridge_tenant` ahora mismo (cuántos clientes multi-downstream tienen config propia). Desde v0.24.1. |
| `qrs_scanned_total` | Histórico acumulado de pairings exitosos (= veces que un usuario ha escaneado un QR). Cuenta filas en `bridge_audit_log` con `action='instance.paired'`. Incluye re-pairings de una misma instancia. |
| `messages_in_total` / `messages_out_total` | All-time totals agregados desde `bridge_usage_daily`. |
| `version` | Tag de release del binario. |
| `last_updated` | Timestamp UTC de la respuesta. |

Si quieres ventanas concretas (último mes, último día, etc.) usa los
endpoints autenticados `/api/usage` o `/api/usage/summary`.

## Habilitar

En el `.env` del stack:

```bash
PUBLIC_STATS_ENABLED=true
PUBLIC_STATS_ALLOW_ORIGIN=https://rricajos.github.io
```

`PUBLIC_STATS_ALLOW_ORIGIN` controla el header CORS. Pon el origen exacto
de tu landing (no uses `*` — limita la exposición). Si lo dejas vacío,
el endpoint funciona pero el navegador bloquea las requests cross-origin.

## Exponer a internet via Traefik

Si el VPS ya tiene Traefik (lo más común), añade labels al servicio
qrsgen en el compose:

```yaml
services:
  qrsgen:
    image: qrsgen:${QRSGEN_VERSION}
    # ... (resto de la config)
    deploy:
      labels:
        - "traefik.enable=true"
        - "traefik.http.routers.qrsgen-public.rule=Host(`telemetry.qrsgen.ricajos.dev`) && Path(`/api/public/stats`)"
        - "traefik.http.routers.qrsgen-public.entrypoints=websecure"
        - "traefik.http.routers.qrsgen-public.tls=true"
        - "traefik.http.routers.qrsgen-public.tls.certresolver=letsencrypt"
        - "traefik.http.services.qrsgen-public.loadbalancer.server.port=3100"
        # Rate limit defensivo (10 req/s, burst 20).
        - "traefik.http.middlewares.qrsgen-ratelimit.ratelimit.average=10"
        - "traefik.http.middlewares.qrsgen-ratelimit.ratelimit.burst=20"
        - "traefik.http.routers.qrsgen-public.middlewares=qrsgen-ratelimit"
```

Apuntas el DNS `telemetry.qrsgen.ricajos.dev` a la IP del VPS y Traefik
hace lo demás (certbot Let's Encrypt + path routing). El resto de la API
qrsgen sigue solo accesible desde la overlay LAN — Traefik solo expone
**ese path concreto**.

### Sin Traefik

Si usas otro reverse proxy (nginx, Caddy, etc.), el principio es el mismo:

- Solo enrutas el path `/api/public/stats` al backend `qrsgen:3100`.
- TLS terminado en el proxy.
- Rate limit defensivo (10 req/s suficiente).
- CORS lo emite qrsgen mismo via `PUBLIC_STATS_ALLOW_ORIGIN` — el proxy
  no necesita tocarlo.

## Frontend en la landing

La landing de qrsgen incluye un widget JS que hace polling cada 10s al
endpoint y actualiza cuatro cards (QRs conectados, instalaciones totales,
mensajes in/out totales). Configurable via `data-endpoint` en el bloque
HTML — ver
[`docs/index.md`](https://github.com/rricajos/qrsgen/blob/main/docs/index.md)
para el snippet.

El JS guarda en `localStorage` si el visitante quiere ver la telemetría
o no — por defecto OFF (opt-in del cliente). Un botón "Activar / Pausar"
controla el polling.

## Verificación

Tras habilitar y exponer:

```bash
# Desde la LAN (debe funcionar):
curl -sS http://qrsgen:3100/api/public/stats

# Desde internet (debe funcionar también):
curl -sS https://telemetry.qrsgen.ricajos.dev/api/public/stats

# CORS preflight:
curl -i -X OPTIONS https://telemetry.qrsgen.ricajos.dev/api/public/stats \
  -H "Origin: https://rricajos.github.io"
# Debe devolver Access-Control-Allow-Origin: https://rricajos.github.io

# Cuando PUBLIC_STATS_ENABLED=false:
curl -sS http://qrsgen:3100/api/public/stats
# {"error":"public stats disabled"}  HTTP 403
```

## Consideraciones de privacidad y seguridad

- El endpoint expone solo **contadores agregados**. Ningún JID, número
  de teléfono, contenido de mensaje ni metadato individual.
- Un atacante puede observar el **ritmo de actividad** (delta de
  `messages_*_total` entre polls). Si esto es información sensible para
  tu caso de uso, no actives el endpoint público.
- Apaga `PUBLIC_STATS_ENABLED` instantáneamente para revocar la
  exposición: el siguiente request al endpoint devuelve 403.
- Rate limit en Traefik (10 req/s) evita scraping abusivo. El payload es
  ~200 bytes — barato pero no infinito.

## Glosario

**Endpoint público**: HTTP endpoint accesible desde internet (no solo
desde la overlay LAN). qrsgen tiene uno opt-in para telemetría.

**Opt-in**: feature desactivada por default. Hay que activarla
explícitamente (env var = true).

**CORS** (Cross-Origin Resource Sharing): mecanismo HTTP que permite a
una página web hacer requests a otro dominio. qrsgen emite el header
`Access-Control-Allow-Origin` cuando se le configura.

**Origin**: el `scheme://host:port` desde donde se sirve una página web.
El navegador lo manda en cada request cross-origin para que el servidor
decida si autorizarla.

**Preflight (CORS)**: petición OPTIONS que el navegador hace antes de
una request cross-origin para preguntar si el servidor la acepta. qrsgen
responde con headers Access-Control-* apropiados.

**Reverse proxy**: servidor (Traefik, nginx, Caddy) que recibe requests
externas y las enruta a backends internos. Termina TLS y aplica
middlewares.

**Traefik labels**: anotaciones en el compose que Traefik lee
automáticamente para descubrir qué containers exponer y bajo qué reglas.

**certresolver**: configuración de Traefik que pide certificados TLS a
Let's Encrypt automáticamente para cada nuevo Host.

**Host rule**: regla de routing de Traefik que matchea por hostname
(`Host(\`telemetry.qrsgen.ricajos.dev\`)`).

**Path rule**: regla que matchea por path URL
(`Path(\`/api/public/stats\`)`). qrsgen lo combina con la Host rule
para exponer solo ese endpoint, no toda la API.

**Rate limit (Traefik middleware)**: middleware que limita requests por
segundo por cliente. Defensa contra scraping y DDoS ligeros.
