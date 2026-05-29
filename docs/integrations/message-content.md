# Soporte de contenido de mensajes

A partir de **v0.36.0**, **v0.37.0** y **v0.38.0**, qrsgen extiende
`extractTextContent` y `extractMedia` para que tipos de payload WhatsApp
que antes caían en el path "sin contenido" y se descartaban
silenciosamente ahora se propaguen al downstream con un cuerpo legible
para el agente.

Las tres versiones tocan la misma costura: **qué se incluye en el body
que qrsgen POSTea al downstream a partir de un `events.Message`**.

> **Read-only sobre WhatsApp**: qrsgen serializa los payloads tal y como
> los entrega whatsmeow. No transcodifica media (no ffmpeg en el
> container) ni vota encuestas ni resuelve direcciones a coordenadas. La
> dirección de la propagación es siempre WA → downstream.

## TL;DR

| Feature | Antes | Ahora | Versión |
|---|---|---|---|
| **Location messages** | `extractTextContent` devolvía `""` → mensaje descartado | Body con header + nombre (POI) + dirección + link Google Maps + comentario | v0.36.0 |
| **Polls (encuestas)** | `PollCreationMessage` caía en el path vacío → descartado | Body con pregunta + opciones numeradas + hint de selección | v0.37.0 |
| **Media polish** | Voice notes con filename `audio.opus` y mime `audio/ogg; codecs=opus`; stickers sin mime default | `voice-note.ogg`, mime saneado sin `; codecs=...`, default `image/webp` para stickers | v0.38.0 |

Las tres comparten la misma propiedad: **no hay env var nueva**. El
comportamiento aplica siempre que el payload llegue por el WebSocket.

## Location messages (v0.36.0)

### Qué se propaga

Cuando el cliente WhatsApp comparte una ubicación (botón adjuntar →
ubicación), Meta entrega un `LocationMessage` con `DegreesLatitude` +
`DegreesLongitude` y opcionalmente `Name`, `Address`, `Comment`,
`IsLive`. Antes de v0.36.0, `extractTextContent` no inspeccionaba el
campo y devolvía `""` — el mensaje terminaba en el path "sin texto ni
media" y se descartaba.

A partir de v0.36.0, `formatLocationContent(loc)` en
`internal/bridge/incoming.go` serializa el payload a un body multilínea.

### Formato

**Ubicación plana (solo coords)**:

```
📍 Ubicación compartida
https://maps.google.com/?q=41.385064,2.173404
```

**Ubicación con POI (Name + Address)**:

```
📍 Ubicación compartida
**La Sagrada Familia**
Carrer de Mallorca 401, Barcelona
https://maps.google.com/?q=41.403621,2.174373
```

**Live location**:

```
📍 Ubicación en vivo
https://maps.google.com/?q=41.385064,2.173404
```

**Con comentario del sender**:

```
📍 Ubicación compartida
https://maps.google.com/?q=41.385064,2.173404
_nos vemos aquí_
```

Reglas del formato:

- **Header**: `📍 Ubicación compartida` por defecto. Si `IsLive=true`,
  cambia a `📍 Ubicación en vivo`.
- **Nombre (POI)**: si `loc.GetName()` no está vacío, se inserta como
  segunda línea en `**bold**`.
- **Dirección**: si `loc.GetAddress()` no está vacío, se inserta como
  línea adicional sin formato.
- **Link Maps**: siempre incluido, con `%.6f,%.6f` (precisión ~10cm).
  URL `https://maps.google.com/?q=<lat>,<lng>`.
- **Comentario**: si `loc.GetComment()` no está vacío, se inserta como
  última línea en `_italic_`.

### Coordenadas inválidas

`lat == 0 && lng == 0` → `formatLocationContent` devuelve `""` y el
mensaje vuelve a caer en el path "sin contenido". Caso defensivo:
algunos clientes envían un payload con coords vacías al cancelar el
selector. El downstream no recibe nada para esos eventos.

### Por qué Google Maps y no Apple/OSM

El link `https://maps.google.com/?q=lat,lng` es universal: abre en el
browser por defecto, en la app Google Maps si está instalada, y en la
app Apple Maps si es iOS y el usuario la tiene configurada como handler
de geo intents. Los formatos alternativos (`geo:`, `https://osm.org/?mlat=...`)
son menos consistentes entre clientes.

qrsgen no parametriza el provider — no hay env var para cambiar el
formato del link. Si tu agente prefiere OSM, lo más sencillo es
post-procesar el body en el downstream.

### Live locations

WhatsApp live locations envían múltiples `LocationMessage` con
`IsLive=true` y coordenadas distintas a lo largo de la duración del
sharing (15min, 1h, 8h según elija el sender). **qrsgen no agrega**:
cada update llega como un mensaje incoming independiente al downstream.

El agente verá una secuencia de mensajes en la conversación, cada uno
con `📍 Ubicación en vivo` y el link a las coords del snapshot
correspondiente. No hay UI nativa en Chatwoot para "esta ubicación se
está moviendo" — qrsgen no la inventa.

Para conversaciones con sharing prolongado y muchos updates, esto
ensucia el thread. Worst case: una live location de 8h puede generar
varios cientos de mensajes. No hay throttle ni dedup específico — el
único filtro es el dedup genérico de `bridge_dedup` (ventana de 10s por
content hash), que probablemente no aplique porque cada snapshot tiene
coords distintas.

## Polls (v0.37.0)

### Qué se propaga

WhatsApp encuestas (creadas con botón adjuntar → encuesta) llegan como
`PollCreationMessage` o `PollCreationMessageV3` según la versión del
cliente. Ambas tienen el mismo shape relevante: `Name` (pregunta),
`Options[]` (opciones de texto), `SelectableOptionsCount` (cuántas
puede elegir el votante).

Antes de v0.37.0 estos eventos caían en el path "sin texto ni media" y
se descartaban. A partir de v0.37.0, `formatPollContent(poll)` los
serializa a un body legible.

### Formato

**Encuesta single-select** (`SelectableOptionsCount=1`):

```
🗳️ **Encuesta:** ¿Día para el meeting?
1. Lunes
2. Martes
3. Miércoles
_(elige 1 opción)_
```

**Encuesta multi-select** (`SelectableOptionsCount > 1`):

```
🗳️ **Encuesta:** ¿Qué te traemos para comer?
1. Pizza
2. Sushi
3. Ensalada
4. Bocadillos
_(elige hasta 2 opciones)_
```

**Encuesta unlimited** (`SelectableOptionsCount=0`):

```
🗳️ **Encuesta:** ¿Qué opinas de la nueva oficina?
1. Me gusta
2. Indiferente
3. No me convence
```

Reglas:

- **Header**: `🗳️ **Encuesta:** <pregunta>` siempre como primera línea.
- **Opciones**: numeradas 1-based, una por línea (`1. Lunes`).
- **Hint de modo**: línea final en `_italic_`:
  - `_(elige 1 opción)_` para single-select.
  - `_(elige hasta N opciones)_` para multi-select.
  - Sin línea de hint para unlimited (`max=0`).

### Casos de descarte silenciosos

- **Sin pregunta** (`poll.GetName() == ""`): devuelve `""`. Sin el
  contexto de la pregunta el agente vería opciones flotantes sin
  sentido.
- **Sin opciones** (`len(options) == 0`): devuelve `""`. No hay
  encuesta funcional sin opciones.

En ambos casos el mensaje cae en el path "sin contenido" y se descarta.

### v1 vs v3

`extractTextContent` chequea ambos:

```go
if poll := msg.Message.GetPollCreationMessage(); poll != nil {
    return formatPollContent(poll)
}
if poll := msg.Message.GetPollCreationMessageV3(); poll != nil {
    return formatPollContent(poll)
}
```

`PollCreationMessageV3` es el shape moderno (clientes WhatsApp recientes
lo prefieren), `PollCreationMessage` el legacy. Ambos llegan al mismo
`formatPollContent` porque el campo set relevante es idéntico (`Name`,
`Options[].OptionName`, `SelectableOptionsCount`).

### Por qué los votos (PollUpdateMessage) NO se propagan

Cuando un participante vota, WhatsApp envía un `PollUpdateMessage` con
los hashes encriptados de las opciones elegidas. Aggregar votos
requeriría:

1. Mantener mapping `option_hash → option_name` por encuesta.
2. Trackear quién ha votado qué (los hashes incluyen el sender JID).
3. Renderizar resultados acumulados de alguna forma legible.

El blocker real no es la complejidad técnica sino el **destino**:
Chatwoot no tiene widget nativo de polls. Aunque qrsgen aggregara los
votos, los renderizaría como otro mensaje texto con un conteo manual,
sin UI de barra de progreso ni tracking de "quién falta por votar". El
valor para el agente es marginal y el ruido en el thread es real (un
mensaje por cada voto, o uno acumulado que se reescribe — ninguna
opción limpia con la API actual de Chatwoot).

Decisión consciente: propagar **solo la creación** de la encuesta para
que el agente sepa que existe y de qué va; los votos se gestionan en el
cliente WhatsApp como hasta ahora.

## Media polish (v0.38.0)

### Alcance

v0.38.0 mejora la compatibilidad de voice notes y stickers en los
reproductores HTML5 de los browsers (incluida la UI de Chatwoot)
**sin** transcodificar contenido. Los bytes binarios que qrsgen sube al
downstream son exactamente los que llegan por WhatsApp; lo que cambia
es el `Content-Type` y el `filename` con los que se anuncian.

### Voice notes (PTT=true)

Antes:

- `filename = "audio.opus"` — la extensión `.opus` no la reconocen
  algunos browsers para audio inline.
- `mimetype = "audio/ogg; codecs=opus"` — el parámetro `codecs=` rompe
  el matching en algunos reproductores que esperan solo `audio/ogg`.

Ahora:

- `filename = "voice-note.ogg"` — la extensión `.ogg` está soportada en
  Chrome/Firefox/Safari modernos y dispara el decoder Opus
  automáticamente.
- `mimetype = "audio/ogg"` — tras pasar por `sanitizeMime`.

El detector es `am.GetPTT()` en `extractMedia` — solo voice notes
(grabaciones del botón micrófono) usan el filename `voice-note`. Audio
adjuntado vía botón "audio" sigue usando filename derivado del mime
(`filenameFromMime(...)`).

### Audio no-PTT

Mismo `sanitizeMime` aplicado al `Content-Type`. Además, si WhatsApp
devuelve mime vacío (caso raro pero observado en clientes antiguos),
qrsgen rellena con `audio/ogg` como default razonable (la mayor parte
del audio de WA es Opus en contenedor OGG).

### Stickers

Antes: si WA no devolvía mime, el `Content-Type` se subía vacío y los
browsers no sabían cómo renderizar la imagen.

Ahora: default `image/webp` si `sm.GetMimetype()` es vacío. WebP es el
formato real de los stickers (WhatsApp los codifica así) y está
soportado en navegadores modernos.

Adicionalmente, el filename usa `sticker.webp` (vía `filenameFromMime`)
en lugar de quedarse con extensión genérica.

### Helper `sanitizeMime`

```go
func sanitizeMime(mime string) string {
    if i := strings.Index(mime, ";"); i >= 0 {
        return strings.TrimSpace(mime[:i])
    }
    return mime
}
```

Trim simple del primer `;` y todo lo que sigue. Convierte:

- `audio/ogg; codecs=opus` → `audio/ogg`
- `audio/ogg;codecs=opus` → `audio/ogg`
- `image/webp; charset=binary` → `image/webp`
- `image/jpeg` → `image/jpeg` (sin cambios).

El parámetro `codecs=` es válido en HTTP RFC 7231 pero algunos
reproductores HTML5 no hacen el match correcto cuando lo encuentran en
el `<source type="...">` o en el `Content-Type` de un blob URL. Quitarlo
maximiza compatibilidad.

### Qué NO se incluye

v0.38.0 es **explícitamente solo metadata polish**:

- **No transcodifica WebP a PNG** para stickers. Si tu downstream o
  navegador no soporta WebP (~5% de uso global en 2026), el sticker
  aparece roto. Solución sería ffmpeg + libwebp en el container —
  rompe la imagen distroless y añade ~30MB.
- **No transcodifica Opus a AAC/MP3** para voice notes. Si tu agente
  usa un browser con problemas para Opus, el audio no se reproduce
  inline pero el download funciona. Solución sería ffmpeg + libopus —
  mismo trade-off.
- **No genera waveform** ni metadata extendida (duración, sample rate).
  El downstream las extrae del binario si las necesita.

Estas conversiones quedaron fuera de v0.38.0 deliberadamente: añadir
ffmpeg al container distroless es un cambio de arquitectura que merece
su propio diseño (qué codecs, qué calidades, cómo manejar errores de
encode, cómo medir el overhead de CPU por mensaje). Candidato a una
v0.40.x si la demanda lo justifica.

## Modos de fallo

| Situación | Resultado |
|---|---|
| Location con `lat==0 && lng==0` | Body vacío → mensaje descartado (path sin contenido). |
| Poll sin pregunta (`Name == ""`) | Body vacío → descartado. |
| Poll sin opciones (`len(Options) == 0`) | Body vacío → descartado. |
| Sticker con mime vacío | `Content-Type` rellenado con `image/webp`. |
| Voice note con mime vacío | `Content-Type` rellenado con `audio/ogg`. |
| Audio con mime `audio/ogg; codecs=opus` | Saneado a `audio/ogg`. |
| `PollUpdateMessage` (voto en encuesta) | Ignorado por construcción — no se procesa. |
| Live location prolongada | Cada snapshot llega como mensaje independiente. No hay agregación. |

## Verificar que funciona

Cuando un cliente comparte una ubicación, los logs deberían mostrar el
POST normal de un mensaje incoming con el body formateado:

```bash
docker logs qrsgen 2>&1 | grep "incoming msg" | tail -5
```

En el panel del downstream (Chatwoot), el mensaje aparece con el
contenido multilínea y el link de Google Maps clickable.

Para encuestas, el body se renderiza con el header `🗳️ **Encuesta:**`
y las opciones numeradas. Para voice notes, el reproductor HTML5
debería mostrar el control de play inline (no fallback al `Download`).

Si no aparece nada:

1. ¿La versión del binario es ≥ v0.36.0/v0.37.0/v0.38.0? Pre-versiones
   descartan estos payloads silenciosamente.
2. ¿El sender envió coords reales o `0,0` (cancelación)? Verifica con
   `tcpdump` o logs de whatsmeow en debug.
3. ¿La encuesta tiene pregunta y opciones? Algunos clientes permiten
   crear encuestas en draft sin nombre — esas no se propagan.

## Glosario

**`extractTextContent`**: función en `internal/bridge/incoming.go` que
inspecciona un `events.Message` y devuelve el body de texto que qrsgen
POSTea al downstream. Antes de v0.36.0/v0.37.0 solo cubría conversation
y extended text; ahora también location y polls.

**`extractMedia`**: función paralela a `extractTextContent` que
inspecciona los tipos media (image, audio, video, document, sticker).
v0.38.0 añade el `sanitizeMime` + defaults razonables para audio y
sticker.

**`LocationMessage`**: tipo de payload en `events.Message` cuando el
cliente comparte ubicación. Contiene `DegreesLatitude`,
`DegreesLongitude`, y opcionalmente `Name` (POI), `Address`, `Comment`,
`IsLive`. qrsgen lo serializa a un body multilínea con link a Google
Maps desde v0.36.0.

**Live location**: ubicación que el cliente WA comparte de forma
continua durante un periodo (15min / 1h / 8h). WhatsApp envía
múltiples `LocationMessage` con `IsLive=true` y coords actualizadas.
qrsgen no agrega: cada snapshot llega como mensaje independiente al
downstream.

**`PollCreationMessage` / `PollCreationMessageV3`**: dos versiones del
payload de encuesta en WhatsApp. Ambos tienen `Name` (pregunta),
`Options[]`, `SelectableOptionsCount`. qrsgen propaga ambos al mismo
`formatPollContent` desde v0.37.0.

**`PollUpdateMessage`**: payload de voto en una encuesta. **No se
propaga** porque Chatwoot no tiene UI nativa de polls que pueda
reflejar los votos aggregados de forma legible.

**`sanitizeMime`**: helper introducido en v0.38.0 que quita el
parámetro de codec del `Content-Type` (`audio/ogg; codecs=opus` →
`audio/ogg`). Maximiza compatibilidad con reproductores HTML5.

**Voice note (PTT)**: grabación de audio hecha desde el botón
micrófono de WhatsApp (push-to-talk). Detectado vía `am.GetPTT()` en
`extractMedia`. Desde v0.38.0 se sube con filename `voice-note.ogg`
para mejorar el rendering inline.
