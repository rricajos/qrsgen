# Formato del prefijo de grupo

A partir de **v0.39.5**, el prefijo que qrsgen antepone al body de los
mensajes de grupo siempre va envuelto en un **inline code block**
(backticks) con la línea de header (nombre bold + tab(s) + teléfono),
y el teléfono se incluye **siempre**. El único punto en el que el
formato cambia según el estado del contact store es el **tilde `~`
delante del nombre**: aparece solo cuando el remitente **no está
guardado** en la libreta del número conectado. Esto replica la
convención de la propia UI de WhatsApp.

> **Read-only sobre WhatsApp**: qrsgen solo lee
> `client.Store.Contacts.GetContact`. No edita la libreta del móvil ni
> escribe en el perfil del usuario. Tampoco propaga renames hechos en
> Chatwoot hacia WA — la dirección de la sincronización es siempre
> WA → downstream.

## TL;DR

| Caso | Formato del prefijo | Versión |
|---|---|---|
| Saved, nombre corto (≤12 runes) | `` `**Jean Paul**\t\t+34604021705` `` | v0.39.5 |
| Saved, nombre largo (>12 runes) | `` `**Ivan Madrid Sánchez**\t+34633185248` `` | v0.39.5 |
| No saved, nombre corto | `` `**~Marcelo Lopez**\t+34663504782` `` | v0.39.5 |
| No saved, nombre largo | `` `**~Anon Pseudonym User**\t+34611111111` `` | v0.39.5 |
| Sin nombre, solo teléfono | `+34604021705:` | v0.39.2 |
| Sin nombre y sin teléfono | (body sin tocar) | v0.31.x y posteriores |

> **Tilde `~` solo para no saved (v0.39.5)**: el carácter `~` antes del
> nombre indica que el remitente **no está en la libreta del bot owner**
> y por tanto el nombre mostrado viene del `PushName` (auto-asignado
> por el propio sender). Para contactos saved (FullName o FirstName en
> el contact store) el nombre va plano, sin tilde. Replica la
> convención de la app WA: en tu chat de grupo, los nombres con `~`
> son los que tu libreta no conoce. Entre v0.39.4 y antes el `~` se
> ponía siempre, lo que perdía esa señal.
>
> **Code block wrap (v0.39.4)**: toda la línea de header va dentro de
> un par de backticks. Los `**` dejan de procesarse como bold markdown
> (inline code suprime el formato interno) y aparecen como caracteres
> literales, pero a cambio Chatwoot renderiza toda la línea con fuente
> monoespaciada y fondo sutil — el contraste visual con el body sigue
> siendo claro y los teléfonos quedan alineados al carácter.
>
> **Separador y formato del teléfono**: desde **v0.39.2** el separador
> entre el nombre bold y el número es un **tab `\t` (U+0009)** en lugar
> del em-space (U+2003) previo. Desde **v0.39.3** el número de tabs
> depende del largo del nombre medido con `utf8.RuneCountInString`
> (los acentos cuentan una sola vez: "Sánchez" = 7 runes). Cutoff
> hardcoded en **12 runes**:
>
> - Nombre **≤ 12 runes** → **2 tabs** (`\t\t`).
> - Nombre **> 12 runes** → **1 tab** (`\t`).
>
> Así los teléfonos quedan alineados visualmente cuando senders con
> nombres de distinto largo se mezclan en el mismo grupo.
>
> **Nota sobre runes y el tilde**: el cálculo del número de tabs se
> hace sobre el nombre **sin** el `~`. El tilde se prepende después de
> decidir la cantidad de tabs, así que añadirlo o quitarlo no
> desalinea los teléfonos.

El comportamiento es **automático**: no hay env var nuevo. Desde
v0.39.5 el único bit que depende del contact store es la presencia
del `~`; la presencia del teléfono **no** depende del estado saved
(siempre va, hereda v0.39.4).

## Histórico de versiones

### v0.31.x — formato fijo con teléfono en code block

```
**~Jean Paul** `+34611111111`
hola buenas

**~Richard** `+34604021705`
hola buenas
```

El bloque de teléfono iba en code block; el nombre quedaba como bold
markdown plano. Siempre se mostraba el número.

### v0.32.0 — adaptativo según saved/unsaved (revertido en v0.39.4)

A partir de v0.32.0 y hasta v0.39.3, qrsgen consultaba
`IsContactSaved(jid)` y, cuando el remitente estaba en la libreta del
número conectado, **omitía el teléfono** del header — el agente solo
veía `**~Jean Paul**`. Para senders no guardados se mantenía
`**~Richard** ...+34604021705`.

La idea era reducir ruido visual cuando el agente ya tenía la persona
identificada por nombre. En la práctica, perder el teléfono para
contactos guardados complicaba el cross-reference en setups
multi-plataforma (mismo número con varias entradas, integración con
CRMs externos). **v0.39.4 revierte esta rama**: el teléfono se muestra
siempre.

### v0.39.2 — tab `\t` + teléfono en plano

El separador entre nombre y teléfono pasa a ser **tab (U+0009)** y el
número pierde los backticks (se renderiza en plano). El branching
saved/unsaved seguía vigente en esta versión.

### v0.39.3 — tab count variable según largo del nombre

El número de tabs pasa a depender de `utf8.RuneCountInString(name)`:
**2 tabs** si `≤ 12` runes, **1 tab** si `> 12`. Persigue alinear
visualmente los teléfonos en grupos con senders de nombres mixtos.
El branching saved/unsaved seguía vigente.

### v0.39.4 — code block wrap + teléfono siempre

Dos cambios sobre v0.39.3:

1. **Header envuelto en inline code block**. Toda la línea
   `**~Name**\t\t+phone` queda entre backticks. Chatwoot la pinta
   monoespaciada con fondo sutil; los `**` aparecen literales pero el
   tratamiento visual del code block aporta la jerarquía.
2. **Teléfono siempre presente**. `applyGroupSenderPrefix` deja de
   consultar `IsContactSaved` para decidir si omite el teléfono.
   Saved y unsaved comparten el teléfono en el header.

En esta versión el tilde `~` se prepende **siempre** al nombre, sin
mirar el estado del contact store (en v0.39.5 esa parte se revisa).

```
`**~Richard**\t\t+34604021705`
hola buenas

`**~Anon**\t\t+34611111111`
hola buenas

`**~Jean Paul**\t\t+34622222222`
hola buenas

`**~La Casa Agency**\t+34655555555`
buenas

`**~Ivan Madrid Sánchez**\t+34633185248`
buenas
```

Tabla de runes y tabs (sin cambios respecto a v0.39.3):

| Nombre | Runes | Tabs |
|---|---|---|
| `Richard` | 7 | 2 |
| `Anon` | 4 | 2 |
| `Jean Paul` | 9 | 2 |
| `La Casa Agency` | 14 | 1 |
| `Ivan Madrid Sánchez` | 19 | 1 |

`utf8.RuneCountInString` cuenta runes (no bytes), así que los acentos
suman una sola unidad ("Sánchez" = 7 runes, no 8).

### v0.39.5 — tilde `~` solo para no saved

Único cambio sobre v0.39.4: `applyGroupSenderPrefix` reintroduce la
llamada a `saved := r.IsContactSaved(...)` que se había retirado en
v0.39.4, pero la usa **solo para decidir si prepende el `~`** al
nombre — no para decidir si incluye el teléfono.

- **Saved** (`IsContactSaved(jid) == true`, es decir hay `FullName` o
  `FirstName` en el contact store): el nombre va plano, sin `~`.
- **No saved** (solo `PushName`, o resolver no encuentra el contacto):
  el nombre se prefija con `~`.

Esto replica la convención de la propia UI de WhatsApp: en un grupo,
el cliente WA muestra `~` delante de los miembros que tu libreta del
móvil no conoce y deja sin tilde a los que sí. El bit lleva la misma
semántica al downstream (Chatwoot) para que el agente sepa, de un
vistazo, si el nombre que ve viene de la libreta del bot owner o lo
puso el propio sender.

El teléfono **sigue presente siempre** (hereda v0.39.4). Las tabs y
el code block wrap no cambian.

```
`**Jean Paul**\t\t+34604021705`     ← saved (FullName en libreta)
hola buenas

`**~Marcelo Lopez**\t+34663504782`  ← no saved (solo PushName)
hola buenas

`**~Anon**\t\t+34611111111`         ← no saved
hola buenas

`**Ivan Madrid Sánchez**\t+34633185248`  ← saved
buenas
```

**Implementación**:

- `applyGroupSenderPrefix` reintroduce el branch `saved :=
  r.IsContactSaved(jid)`. Si `saved`, se omite el tilde; si no,
  `name = "~" + name`.
- **LID → PN fallback**: si el sender llega como LID y `IsContactSaved`
  devuelve `false` (o no hay nombre), qrsgen resuelve a PN vía
  `PNForLID` y re-chequea `IsContactSaved` con el PN. Cubre el caso de
  contactos guardados que se intercambian con su LID anonimizado.
- **PushName nunca cuenta como saved**: aunque whatsmeow tenga
  PushName cacheado, `IsContactSaved` exige `FullName` o `FirstName`
  (los pone el bot owner, no el sender). Senders con solo PushName
  conservan el `~`.

### Resumen del bit saved/unsaved a través de versiones

| Versión | `~` se pone | Teléfono se muestra |
|---|---|---|
| v0.31.x | siempre | siempre |
| v0.32.0–v0.39.3 | siempre | solo si no saved |
| v0.39.4 | siempre | siempre |
| **v0.39.5** | **solo si no saved** | **siempre** |

## `IsContactSaved`: vuelve a consultarse, pero solo para el tilde

El método `IsContactSaved(jid)` siempre estuvo en la interfaz
`WAResolver` y consulta `client.Store.Contacts.GetContact` de
whatsmeow. Su uso en `applyGroupSenderPrefix` cambió varias veces:

- **v0.32.0–v0.39.3**: se consultaba para decidir si **omitir el
  teléfono** para senders guardados.
- **v0.39.4**: `applyGroupSenderPrefix` dejó de consultarlo; el
  prefijo era idéntico para saved y unsaved.
- **v0.39.5**: vuelve a consultarse, pero solo para decidir si
  **prepender el tilde `~`** al nombre. El teléfono sigue
  incluyéndose siempre (no se revierte ese punto de v0.39.4).

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
ya tiene cacheado del backend de WhatsApp.

`IsContactSaved` consulta `info.Found && (info.FullName != "" || info.FirstName != "")`:

| Campo whatsmeow | ¿Cuenta como saved? | Origen |
|---|---|---|
| `FullName` | **Sí** | Nombre completo en la libreta del bot owner |
| `FirstName` | **Sí** | Nombre corto en la libreta del bot owner |
| `PushName` | **No** | Auto-asignado por el propio sender en su WhatsApp |
| `BusinessName` | **No** | Display name de cuentas WA Business |

La distinción es la que decide el tilde desde v0.39.5: si el sender
tiene `FullName` o `FirstName`, el nombre va plano; si solo hay
`PushName` (o nada), se prepende `~`.

## Caso especial: LID → PN fallback

Con Multi-Device, el sender de un mensaje de grupo puede llegar como
LID (identificador anónimo, server `lid`) en vez de PN (server
`s.whatsapp.net`). qrsgen sigue resolviendo el LID a su PN vía
`PNForLID` para obtener un `ContactName` y un teléfono presentables.
Desde **v0.39.5** el fallback además vuelve a re-chequear
`IsContactSaved` con el PN resuelto: si el contacto está en la libreta
bajo su PN canónico, se le quita el tilde aunque el evento haya
llegado por LID. Sin esa segunda comprobación, todos los senders
guardados que mandaran via LID aparecerían como no saved.

El teléfono se sigue incluyendo siempre (heredado de v0.39.4),
independientemente del resultado de `IsContactSaved`.

## Grupos como sender

Si el sender del mensaje es a su vez un JID de grupo (caso raro:
forwards, anuncios de canal-grupo), el path no cambia: se renderiza
nombre + tab(s) + el "teléfono" (que sería el ID del grupo).
`IsContactSaved` devuelve `false` por construcción para groupJIDs, así
que desde v0.39.5 estos se rinderizan con `~` por defecto.

## Verificar que funciona

Tras mensajes de grupo enviados por un contacto saved y otro no saved,
los logs del downstream deberían mostrar ambos formatos:

```bash
docker logs qrsgen 2>&1 | grep "incoming sync" | tail
# → ... content="`**Jean Paul**\t\t+34622222222`\nhola buenas" ...        (saved)
# → ... content="`**~Marcelo Lopez**\t+34663504782`\nhola buenas" ...     (no saved)
# → ... content="`**Ivan Madrid Sánchez**\t+34633185248`\nbuenas" ...     (saved)
```

Si todos los senders aparecen con `~` (o ninguno) revisa que la
libreta del móvil del bot esté sincronizada con WhatsApp y que el
contact store de whatsmeow refleje los nombres (sección siguiente).

Para inspeccionar directamente el contact store de whatsmeow:

```sql
SELECT their_jid, full_name, first_name, push_name
FROM whatsmeow_contacts
WHERE our_jid = '<jid-de-la-instancia>'
ORDER BY full_name NULLS LAST
LIMIT 20;
```

Sigue siendo útil para diagnosticar por qué un sender llega con
`PushName` en lugar de `FullName` (esto decide tanto qué string se
muestra como nombre **como** si lleva el tilde `~` desde v0.39.5). El
teléfono va siempre, independientemente.

## Modos de fallo

| Situación | Resultado |
|---|---|
| Google Contacts sync deshabilitado en el móvil | El contacto aparece con PushName en lugar de FullName/FirstName, y por tanto con `~` delante (desde v0.39.5). Teléfono igual visible. |
| App WhatsApp sin permiso "Acceder a contactos" | Igual: no llega FullName al store, se muestra PushName + `~`. |
| Contacto recién añadido (segundos atrás) | Puede tardar minutos en propagarse al store de whatsmeow. Hasta entonces se ve PushName con `~`; al refrescarse el store el `~` desaparece. |
| Sender llega como LID anonymizado | Se resuelve a PN vía `PNForLID` para obtener nombre y teléfono; desde v0.39.5 se re-chequea `IsContactSaved` con el PN para acertar el bit del tilde. |
| Sender LID sin posibilidad de resolver a PN | Se muestra el PushName que llegó en el evento con `~`; teléfono si está disponible. |

## Caveats

- **El bot owner sigue mandando sobre el nombre mostrado y el tilde**.
  Lo que cuente como nombre canónico (FullName, FirstName, PushName)
  depende de la libreta del dueño del número conectado. Desde v0.39.5
  eso también decide si aparece el `~`; el teléfono va siempre.
- **No write-back**. Si un agente edita el nombre del contacto en
  Chatwoot, ese cambio se queda en Chatwoot. qrsgen no propaga edits al
  contact store de WA ni a la libreta del móvil del bot.
- **Sin opt-out por env var**. No hay flag para volver al formato sin
  code block, al branching saved/omit-phone, ni al `~` siempre de
  v0.39.4. Si necesitas un formato anterior, considera abrir un issue.
- **Render del code block depende del downstream**. Chatwoot pinta
  inline code monoespaciado con fondo gris claro. Otros downstreams
  pueden renderizarlo de forma distinta o ignorar los backticks.

## Glosario

**Contact store**: cache que whatsmeow mantiene en
`client.Store.Contacts` con los contactos que el backend Meta expone
para el número conectado. Se nutre del sync entre la libreta del móvil
y WhatsApp.

**FullName / FirstName**: campos del contact store de whatsmeow que
representan el nombre que el dueño del número conectado puso en su
libreta. Desde v0.39.5 condicionan si el nombre lleva tilde `~`
delante (sin tilde si hay FullName/FirstName, con tilde si solo hay
PushName). No condicionan la presencia del teléfono — siempre va
desde v0.39.4.

**PushName**: nombre que el propio sender configura en su WhatsApp
("Tu nombre" en ajustes). Llega en cada mensaje. Se usa como fallback
de display si no hay FullName/FirstName.

**LID** (Linked Identifier): identificador anónimo que WhatsApp asigna
a un cliente Multi-Device. Sirve para enrutar sin exponer el PN real
del sender en grupos. Server `lid` en lugar de `s.whatsapp.net`.

**PN** (Phone Number JID): el JID estándar `<E164>@s.whatsapp.net`.
Es lo que coincide con la entrada de libreta y con el contact store.

**`PNForLID`**: método de resolución que qrsgen usa para mapear un LID
al PN equivalente. Cuando whatsmeow ya conoce la relación (porque el
sender envió alguna vez como PN), devuelve el match.

**`IsContactSaved`**: método del `WAResolver` que indica si un JID
tiene `FullName` o `FirstName` en el contact store de whatsmeow.
Forma parte de la interfaz desde v0.32.0. En v0.39.4
`applyGroupSenderPrefix` dejó de consultarlo; **desde v0.39.5 vuelve a
consultarse**, pero solo para decidir si prepende el tilde `~` al
nombre (el teléfono sigue incluyéndose siempre, no se revierte en
v0.39.5).

**Bot owner**: dueño del número de WhatsApp que tiene la sesión
emparejada con qrsgen.

**Separador del prefijo (v0.39.2)**: tab `\t` (U+0009) entre el nombre
bold y el teléfono. Reemplaza al em-space (U+2003) previo.

**Tab count variable (v0.39.3)**: regla que elige 2 tabs si
`utf8.RuneCountInString(name) ≤ 12` y 1 tab si `> 12`. Persigue
alinear visualmente los teléfonos cuando senders con nombres de
distinto largo intercambian mensajes en el mismo grupo.

**Code block wrap del header (v0.39.4)**: envoltorio con backticks de
toda la línea `**Name**<tabs>+phone` (o `**~Name**<tabs>+phone` para
no saved desde v0.39.5). Chatwoot la renderiza monoespaciada con
fondo sutil; los `**` aparecen literales porque inline code suprime el
formato interno.

**Teléfono siempre presente (v0.39.4)**: revierte el branching
saved/omit-phone introducido en v0.32.0. El header incluye el número
para todos los senders, saved o no. Se mantiene tal cual en v0.39.5.

**Tilde `~` solo para no saved (v0.39.5)**: el `~` antes del nombre
aparece únicamente cuando `IsContactSaved(jid) == false` (no hay
FullName ni FirstName en el contact store; el nombre viene del
PushName del sender). Replica la convención de la propia UI de
WhatsApp y sirve al agente del downstream como señal visual de qué
nombre viene de la libreta del bot owner y cuál se lo puso el propio
sender.
