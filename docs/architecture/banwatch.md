# Detección de ban-risk (BanWatcher)

`internal/banwatch` mantiene un ring buffer por instancia con los últimos
~10 minutos de eventos `(timestamp, jid, success)`. Tres señales:

| Señal | Default threshold | Por qué importa |
|---|---|---|
| **velocity** | >30 msgs / 1 min | Patrón típico de blast spam |
| **diversity** | >20 JIDs únicos / 5 min | Outreach masivo a nuevos contactos |
| **delivery_ratio** | <0.7 sobre 10 samples / 10 min | WhatsApp rechaza envíos → near-ban |

## Score + level

El watcher combina las tres señales en un score 0-1 y un level
cualitativo:

| Score | Level |
|---|---|
| `>= 0.7` | `high` |
| `>= 0.4` | `moderate` |
| `> 0.1`  | `low` |
| `<= 0.1` | `ok` |

## Emisión de alertas

Cuando una señal cruza su threshold, el watcher emite el evento
lifecycle `ban_risk` **una vez** (rising edge). Vuelve a emitir si la
alerta se limpia y dispara de nuevo. Esto evita inundar al integrador con
notificaciones repetidas mientras un patrón está en curso.

`GET /api/instances/:name/ban-risk` devuelve el snapshot completo —
velocity actual, diversity, delivery ratio, alerts activas, score y level.

## Uso típico

Pensado para que tu orquestador **reduzca el ritmo / pause envíos antes
de que WhatsApp tome medidas** (strike / temporary ban). Ejemplo:

- Recibes `ban_risk` con `level=high` y `alert=high_velocity`.
- Pausas los workflows automáticos masivos.
- Avisas al supervisor humano para que confirme antes de retomar.

## Configuración

`banwatch.Config` permite ajustar todos los thresholds. Por defecto se
usa `DefaultConfig()` — tunable por código si tu volumen es muy distinto
del default (e.g. helpdesks de alto volumen pueden subir `VelocityThreshold`
a 60 sin saturar).
