# Formato del prefijo de grupo

A partir de **v0.39.4**, el prefijo que qrsgen antepone al body de los
mensajes de grupo tiene un único formato: la línea completa de header
(nombre bold + tab(s) + teléfono) va envuelta en **inline code block**
con backticks. El teléfono se incluye **siempre**, sin importar si el
remitente está guardado en la libreta del número conectado.

> **Read-only sobre WhatsApp**: qrsgen solo lee
> `client.Store.Contacts.GetContact`. No edita la libreta del móvil ni
> escribe en el perfil del usuario. Tampoco propaga renames hechos en
> Chatwoot hacia WA — la dirección de la sincronización es siempre
> WA → downstream.

## TL;DR

| Caso | Formato del prefijo | Versión |
|---|---|---|
| Nombre corto (≤12 runes) | `` `**~Richard**\t\t+34604021705` `` | v0.39.4 |
| Nombre largo (>12 runes) | `` `**~Ivan Madrid Sánchez**\t+34633185248` `` | v0.39.4 |
| Sin nombre, solo teléfono | `+34604021705:` | v0.39.2 |
| Sin nombre y sin teléfono | (body sin tocar) | v0.31.x y posteriores |

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

El comportamiento es **automático**: no hay env var nuevo. Desde
v0.39.4 el formato no depende del estado del contact store: el header
se construye igual para todos los senders.

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
   consultar `IsContactSaved` para decidir el formato del prefijo.
   Saved y unsaved comparten ahora el mismo header.

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

## `IsContactSaved`: sigue existiendo en el resolver

El método `IsContactSaved(jid)` permanece en la interfaz `WAResolver`
y sigue consultando `client.Store.Contacts.GetContact` de whatsmeow.
Lo que cambió en v0.39.4 es que **`applyGroupSenderPrefix` ya no lo
llama** para decidir el formato del prefijo de grupo. Otros callers
(si los hay en el futuro) pueden seguir usándolo.

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

La distinción sigue siendo válida conceptualmente
(**FullName/FirstName los pone el dueño del número conectado**;
**PushName lo pone el sender**), aunque desde v0.39.4 no afecta al
formato del prefijo de grupo.

## Caso especial: LID → PN fallback

Con Multi-Device, el sender de un mensaje de grupo puede llegar como
LID (identificador anónimo, server `lid`) en vez de PN (server
`s.whatsapp.net`). qrsgen sigue resolviendo el LID a su PN vía
`PNForLID` para obtener un `ContactName` y un teléfono presentables.
Lo que ya no consulta es `IsContactSaved` con el resultado del
fallback para decidir si omitir el teléfono — desde v0.39.4 el
teléfono va siempre.

## Grupos como sender

Si el sender del mensaje es a su vez un JID de grupo (caso raro:
forwards, anuncios de canal-grupo), el path no cambia: se renderiza
nombre + tab(s) + el "teléfono" (que sería el ID del grupo). En v0.39.3
y anteriores `IsContactSaved` devolvía `false` por construcción para
groupJIDs; ahora ese check no se hace.

## Verificar que funciona

Tras un mensaje de grupo enviado por un contacto cualquiera (saved o
no), los logs del downstream deberían mostrar el header envuelto en
backticks con teléfono presente:

```bash
docker logs qrsgen 2>&1 | grep "incoming sync" | tail
# → ... content="`**~Jean Paul**\t\t+34622222222`\nhola buenas" ...
# → ... content="`**~Ivan Madrid Sánchez**\t+34633185248`\nbuenas" ...
```

Para inspeccionar directamente el contact store de whatsmeow:

```sql
SELECT their_jid, full_name, first_name, push_name
FROM whatsmeow_contacts
WHERE our_jid = '<jid-de-la-instancia>'
ORDER BY full_name NULLS LAST
LIMIT 20;
```

Sigue siendo útil para diagnosticar por qué un sender llega con
`PushName` en lugar de `FullName` (el nombre que se muestra en el
prefijo depende de esto), aunque ya no condiciona si el teléfono
aparece o no.

## Modos de fallo

| Situación | Resultado |
|---|---|
| Google Contacts sync deshabilitado en el móvil | El contacto aparece con PushName en lugar de FullName/FirstName. Teléfono igual visible (siempre lo es desde v0.39.4). |
| App WhatsApp sin permiso "Acceder a contactos" | Igual: no llega FullName al store, se muestra PushName. |
| Contacto recién añadido (segundos atrás) | Puede tardar minutos en propagarse al store de whatsmeow. Hasta entonces se ve PushName. |
| Sender llega como LID anonymizado | Se resuelve a PN vía `PNForLID` para obtener nombre y teléfono. |
| Sender LID sin posibilidad de resolver a PN | Se muestra el PushName que llegó en el evento; teléfono si está disponible. |

## Caveats

- **El bot owner sigue mandando sobre el nombre mostrado**. Lo que
  cuente como nombre canónico (FullName, FirstName, PushName) depende
  de la libreta del dueño del número conectado. Desde v0.39.4 esto
  solo afecta a qué string aparece tras `**~`; el teléfono va siempre.
- **No write-back**. Si un agente edita el nombre del contacto en
  Chatwoot, ese cambio se queda en Chatwoot. qrsgen no propaga edits al
  contact store de WA ni a la libreta del móvil del bot.
- **Sin opt-out por env var**. No hay flag para volver al formato sin
  code block ni al branching saved/unsaved. Si necesitas el formato
  pre-v0.39.4, considera abrir un issue.
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
libreta. Desde v0.39.4 ya no condicionan si aparece el teléfono en el
prefijo; solo influyen en qué string se muestra como nombre.

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
tiene `FullName` o `FirstName` en el contact store de whatsmeow. Sigue
formando parte de la interfaz desde v0.32.0, pero **desde v0.39.4
`applyGroupSenderPrefix` no lo consulta**: el formato del prefijo de
grupo es el mismo para saved y unsaved.

**Bot owner**: dueño del número de WhatsApp que tiene la sesión
emparejada con qrsgen.

**Separador del prefijo (v0.39.2)**: tab `\t` (U+0009) entre el nombre
bold y el teléfono. Reemplaza al em-space (U+2003) previo.

**Tab count variable (v0.39.3)**: regla que elige 2 tabs si
`utf8.RuneCountInString(name) ≤ 12` y 1 tab si `> 12`. Persigue
alinear visualmente los teléfonos cuando senders con nombres de
distinto largo intercambian mensajes en el mismo grupo.

**Code block wrap del header (v0.39.4)**: envoltorio con backticks de
toda la línea `**~Name**<tabs>+phone`. Chatwoot la renderiza
monoespaciada con fondo sutil; los `**` aparecen literales porque
inline code suprime el formato interno.

**Teléfono siempre presente (v0.39.4)**: revierte el branching
saved/omit-phone introducido en v0.32.0. El header incluye el número
para todos los senders, saved o no.
