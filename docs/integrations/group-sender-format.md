# Formato adaptativo del prefijo de grupo

A partir de **v0.32.0**, el prefijo que qrsgen antepone al body de los
mensajes de grupo se adapta según si el remitente está guardado en la
libreta de contactos del número conectado o no. Si el agente (humano o
bot) ya sabe quién es por el nombre, el bloque de teléfono se omite —
ya no aporta información. Si solo conocemos al sender por su push name,
se mantiene el código de teléfono para que el agente pueda
identificarlo.

> **Read-only sobre WhatsApp**: qrsgen solo lee
> `client.Store.Contacts.GetContact`. No edita la libreta del móvil ni
> escribe en el perfil del usuario. Tampoco propaga renames hechos en
> Chatwoot hacia WA — la dirección de la sincronización es siempre
> WA → downstream.

## TL;DR

| Estado del contacto | Formato del prefijo | Versión |
|---|---|---|
| **Guardado** (FullName o FirstName en libreta) | `**~Jean Paul**` | v0.32.0 |
| **Solo push name**, nombre corto (≤12 runes) | `**~Richard**\t\t+34604021705` | v0.39.3 |
| **Solo push name**, nombre largo (>12 runes) | `**~Ivan Madrid Sánchez**\t+34633185248` | v0.39.3 |
| Sin nombre, solo teléfono | `+34604021705:` | v0.39.2 |
| Sin nombre y sin teléfono | (body sin tocar) | v0.31.x y posteriores |

> **Separador y formato del teléfono**: desde **v0.39.2** el separador
> entre el nombre bold y el número es un **tab `\t` (U+0009)** en lugar
> del em-space (U+2003) previo, y el teléfono va en **plano** (sin code
> block backticks) — el `+` ya lo identifica visualmente y algunos
> renderers de Chatwoot daban más separación al tab.
>
> Desde **v0.39.3** el número de tabs depende del largo del nombre
> medido con `utf8.RuneCountInString` (los acentos cuentan una sola vez:
> "Sánchez" = 7 runes). Cutoff hardcoded en **12 runes**:
>
> - Nombre **≤ 12 runes** → **2 tabs** (`\t\t`).
> - Nombre **> 12 runes** → **1 tab** (`\t`).
>
> Así los teléfonos quedan alineados visualmente cuando senders con
> nombres de distinto largo se mezclan en el mismo grupo.

El comportamiento es **automático**: no hay env var nuevo. La decisión
depende del estado del contact store de whatsmeow en el momento de la
recepción.

## Antes y después

Mismo grupo, dos remitentes: **Jean Paul** (en la libreta del móvil
conectado) y **Richard** (solo conocido por su push name).

### v0.31.x — siempre se mostraba el teléfono

```
**~Jean Paul** `+34611111111`
hola buenas

**~Richard** `+34604021705`
hola buenas
```

Aunque el agente ya tenía a Jean Paul en agenda, el body seguía
incluyendo el bloque code con el número — ruido visual y duplica info
que el panel del downstream ya muestra al lado de la conversación.

### v0.32.0 — adaptativo (separador em-space + code block)

```
**~Jean Paul**
hola buenas

**~Richard** `+34604021705`
hola buenas
```

- Jean Paul: solo nombre. El agente ya sabe quién es; no necesita ver
  el teléfono en cada mensaje.
- Richard: nombre + teléfono. Como el bot owner no tiene a Richard en
  agenda, el código de teléfono permite identificarlo o decidir si
  guardarlo.

### v0.39.2 — tab `\t` + teléfono en plano

Mismo branching saved/unsaved, pero el separador entre nombre y
teléfono pasa a ser un **tab (U+0009)** y el número se renderiza en
**plano** (sin backticks):

```
**~Jean Paul**
hola buenas

**~Richard**\t+34604021705
hola buenas
```

El caso degenerado "solo teléfono" también pierde los backticks:
`+34604021705:` en lugar de `` `+34604021705`: ``.

### v0.39.3 — tab count variable según largo del nombre

Para alinear visualmente los teléfonos cuando senders con nombres de
distinto largo intercambian mensajes en el mismo grupo, el número de
tabs ahora depende de `utf8.RuneCountInString(name)`:

```
**~Richard**\t\t+34604021705
hola buenas

**~Anon**\t\t+34611111111
hola buenas

**~Jean Paul**\t\t+34622222222
hola buenas

**~La Casa Agency**\t+34655555555
buenas

**~Ivan Madrid Sánchez**\t+34633185248
buenas
```

Cutoff hardcoded en **12 runes**:

| Nombre | Runes | Tabs |
|---|---|---|
| `Richard` | 7 | 2 |
| `Anon` | 4 | 2 |
| `Jean Paul` | 9 | 2 |
| `La Casa Agency` | 14 | 1 |
| `Ivan Madrid Sánchez` | 19 | 1 |

`utf8.RuneCountInString` cuenta runes (no bytes), así que los acentos
suman una sola unidad ("Sánchez" = 7 runes, no 8).

## La cadena "saved"

Para que qrsgen considere un JID como guardado, el nombre debe llegar
hasta el contact store interno de whatsmeow. La ruta es:

```
Google Contacts (cuenta del móvil)
        │ sync periódico
        ▼
Libreta de contactos del Android/iOS
        │ permisos concedidos a WhatsApp
        ▼
App WhatsApp del número conectado
        │ envío de la libreta al backend Meta
        ▼
whatsmeow store (`client.Store.Contacts`)
        │ leído por qrsgen
        ▼
IsContactSaved(jid) → true
```

qrsgen **no** se integra con Google Contacts. Solo lee lo que whatsmeow
ya tiene cacheado del backend de WhatsApp. Si en algún punto de la
cadena falla la sync (cuenta Google sin contactos sincronizados, app
WhatsApp con permiso de contactos denegado, etc.), el JID aparecerá
como no guardado aunque tú lo veas perfectamente identificado en tu
móvil personal.

## Qué cuenta como "guardado"

`IsContactSaved` consulta `info.Found && (info.FullName != "" || info.FirstName != "")`:

| Campo whatsmeow | ¿Cuenta como saved? | Origen |
|---|---|---|
| `FullName` | **Sí** | Nombre completo en la libreta del bot owner |
| `FirstName` | **Sí** | Nombre corto en la libreta del bot owner |
| `PushName` | **No** | Auto-asignado por el propio sender en su WhatsApp |
| `BusinessName` | **No** | Display name de cuentas WA Business |

La distinción clave: **FullName/FirstName los pone el dueño del número
conectado**; **PushName lo pone el sender**. El primero es una decisión
del bot owner de "conozco a esta persona"; el segundo es self-reported
y no aporta confianza.

## Caso especial: LID → PN fallback

Con Multi-Device, el sender de un mensaje de grupo puede llegar como
LID (identificador anónimo, server `lid`) en vez de PN (server
`s.whatsapp.net`). qrsgen primero pregunta `IsContactSaved(lid)`; si el
LID no está en la libreta o no tiene nombre, **resuelve el LID a su PN**
vía `PNForLID` y vuelve a preguntar `IsContactSaved(pn)` y
`ContactName(pn)`.

Cubre el caso real: tienes un contacto guardado por su número en la
libreta, pero WhatsApp lo entrega anonymizado al grupo. Sin el
fallback, todos los contactos guardados aparecerían como "solo push
name" cuando mandan mensajes de grupo desde un cliente Multi-Device.

## Grupos como sender: siempre unsaved

Si el sender del mensaje es a su vez un JID de grupo (caso raro:
forwards, anuncios de canal-grupo), `IsContactSaved` devuelve `false`
por construcción. Los grupos no tienen `FullName`/`FirstName` en el
contact store — esos campos solo aplican a personas. Tampoco tiene
sentido lógico: "guardar un grupo en la agenda" no es una operación
que el usuario realice en el móvil.

Consecuencia práctica: si por algún flujo extraño llega un mensaje cuyo
sender es un groupJID, el prefix usa el path no-saved (incluye el
"teléfono" — que en realidad sería el ID de grupo).

## Verificar que funciona

Tras un mensaje de grupo enviado por un contacto guardado en el móvil
conectado, los logs del downstream deberían mostrar el body sin
teléfono:

```bash
# El body que qrsgen POSTea al downstream (visible en Chatwoot
# como contenido del mensaje):
docker logs qrsgen 2>&1 | grep "incoming sync" | tail
# → ... content="**~Jean Paul**\nhola buenas" ...
```

Si en cambio el sender está fuera de la agenda, verás nombre + tab(s) +
teléfono en plano (1 ó 2 tabs según el largo del nombre desde v0.39.3):

```
... content="**~Richard**\t\t+34604021705\nhola buenas" ...
... content="**~Ivan Madrid Sánchez**\t+34633185248\nhola buenas" ...
```

Para inspeccionar directamente el contact store de whatsmeow (qué
contactos están guardados según el último sync con Meta), consulta la
tabla `whatsmeow_contacts` en la DB:

```sql
SELECT their_jid, full_name, first_name, push_name
FROM whatsmeow_contacts
WHERE our_jid = '<jid-de-la-instancia>'
ORDER BY full_name NULLS LAST
LIMIT 20;
```

Un JID con `full_name` o `first_name` no nulo es lo que qrsgen
considera "guardado".

## Cómo hacer que un contacto sea "saved"

1. Abre la libreta del móvil conectado (el que tiene la sesión WA
   Multi-Device emparejada).
2. Añade el contacto con su nombre real (o edítalo si ya estaba pero
   solo con número).
3. Asegúrate de que la cuenta Google del móvil sincroniza contactos
   (Ajustes → Cuentas → Google → Sincronizar contactos).
4. Espera unos minutos a que la app WhatsApp del móvil propague el
   cambio al backend Meta.
5. El próximo mensaje de grupo del contacto debería llegar al downstream
   sin el bloque de teléfono.

No es necesario reiniciar qrsgen ni la sesión: whatsmeow refresca el
contact store en background.

## Modos de fallo

| Situación | Resultado |
|---|---|
| Google Contacts sync deshabilitado en el móvil | Contacto no llega a la app WA → aparece como no guardado. |
| App WhatsApp sin permiso "Acceder a contactos" | Igual: no llega al backend, no llega al store. |
| Contacto recién añadido (segundos atrás) | Puede tardar minutos en propagarse al store de whatsmeow. Primer mensaje aún saldrá con teléfono. |
| Contacto guardado solo con apodo/emoji | Cuenta como saved si está en `FullName` o `FirstName`. qrsgen no juzga la calidad del nombre. |
| Sender llega como LID anonymizado | Si el PN resuelto via `PNForLID` sí está guardado, se considera saved. |
| Sender LID sin posibilidad de resolver a PN | Path no-saved (se incluye el bloque de teléfono si hay phoneDigits). |

## Caveats

- **El bot owner manda**. Lo que cuente como "saved" es lo que el dueño
  del número conectado tiene en la libreta. Si quien opera el sistema
  downstream (los agentes) no ve algunos contactos por nombre, el
  problema está en la libreta del móvil del bot, no en qrsgen.
- **No write-back**. Si un agente edita el nombre del contacto en
  Chatwoot, ese cambio se queda en Chatwoot. qrsgen no propaga edits al
  contact store de WA ni a la libreta del móvil del bot.
- **Cambio asimétrico de comportamiento**. Un contacto que pasa de
  no-saved a saved (lo añades a la libreta) deja de mostrar el teléfono
  en los siguientes mensajes. No hay reescritura retroactiva de los
  bodies ya entregados al downstream.
- **Sin opt-out por env var**. Si necesitas el formato pre-v0.32.0
  (siempre con teléfono) por razones de tooling downstream, fuerza
  todos los contactos como no guardados — no hay flag para
  deshabilitar la branching saved/unsaved. Considera abrir un issue.

## Glosario

**Contact store**: cache que whatsmeow mantiene en
`client.Store.Contacts` con los contactos que el backend Meta expone
para el número conectado. Se nutre del sync entre la libreta del móvil
y WhatsApp.

**FullName / FirstName**: campos del contact store de whatsmeow que
representan el nombre que el dueño del número conectado puso en su
libreta. Si están vacíos, el contacto no está "guardado" desde la
perspectiva del bot owner.

**PushName**: nombre que el propio sender configura en su WhatsApp
("Tu nombre" en ajustes). Llega en cada mensaje. NO cuenta como
"guardado" porque no representa una decisión del bot owner.

**LID** (Linked Identifier): identificador anónimo que WhatsApp asigna
a un cliente Multi-Device. Sirve para enrutar sin exponer el PN real
del sender en grupos. Server `lid` en lugar de `s.whatsapp.net`.

**PN** (Phone Number JID): el JID estándar `<E164>@s.whatsapp.net`.
Es lo que coincide con la entrada de libreta y con el contact store.

**`PNForLID`**: método de resolución que qrsgen usa para mapear un LID
al PN equivalente. Cuando whatsmeow ya conoce la relación (porque el
sender envió alguna vez como PN), devuelve el match.

**Bot owner**: dueño del número de WhatsApp que tiene la sesión
emparejada con qrsgen. Es quien decide qué contactos quedan
"guardados" añadiéndolos a la libreta de su móvil.

**Adaptativo (prefijo)**: convención de v0.32.0 donde el formato del
prefix depende del estado del contacto. Antes era un único formato
fijo con teléfono siempre presente.

**Separador del prefijo (v0.39.2)**: tab `\t` (U+0009) entre el nombre
bold y el teléfono en el path no-saved. Reemplaza al em-space (U+2003)
previo. El teléfono va en plano (sin code block backticks) — el `+`
basta para identificarlo visualmente.

**Tab count variable (v0.39.3)**: regla que elige 2 tabs si
`utf8.RuneCountInString(name) ≤ 12` y 1 tab si `> 12`. Persigue
alinear visualmente los teléfonos cuando senders con nombres de
distinto largo intercambian mensajes en el mismo grupo. Cutoff
hardcoded en 12 runes.
