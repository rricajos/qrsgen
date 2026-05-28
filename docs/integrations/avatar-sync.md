# Sincronización de avatares WhatsApp

qrsgen mantiene el avatar de cada contacto y grupo en el downstream
sincronizado con la foto de perfil real de WhatsApp. Reemplaza los
letter-avatars autogenerados por Chatwoot (o equivalente) por la foto
que la persona o el admin del grupo tiene configurada en WhatsApp.

> **Read-only sobre WhatsApp**: qrsgen solo lee fotos de perfil
> (`GetProfilePictureInfo` + HTTP GET). Nunca escribe en el perfil del
> usuario. La escritura es solo hacia el downstream.

## TL;DR

| Capa | Cuándo dispara | Coste | Versión |
|---|---|---|---|
| **On-create** | `CreateContact` exitoso en downstream | 1 GET metadata + 1 GET imagen + 1 PUT downstream | v0.31.0 |
| **TTL refresh** | Siguiente mensaje del JID tras TTL | 1 GET metadata (+ descarga solo si cambió ID) | v0.31.1 |
| **Real-time** | `events.Picture` de whatsmeow (usuario cambia su foto) | 1 GET metadata + 1 GET imagen + 1 PUT downstream | v0.31.2 |
| **Bulk re-sync** | Operador llama `POST .../avatars/resync` | Una pasada por todos los contactos del inbox | v0.31.3 |

Todas las capas son **fire-and-forget**: corren en goroutines separadas
y nunca bloquean el flujo del mensaje. Los errores (foto privada,
upload rechazado, etc.) se loguean como `warn` y se ignoran.

## Configuración

| Env var | Default | Descripción |
|---|---|---|
| `QRSGEN_AVATAR_SYNC` | `true` | Master switch. Si `false`, ninguna capa corre — los contactos quedan con su letter-avatar. |
| `QRSGEN_AVATAR_REFRESH_TTL` | `24h` | TTL del tracker para refresh por mensaje. `0` desactiva el refresh (mantiene solo on-create y events.Picture). |

Ambas son opt-out: el comportamiento por defecto sincroniza fotos.

## Capa 1 — sync al crear contacto (v0.31.0)

En `bridge.sync()`, justo después de un `CreateContact` exitoso en
downstream, qrsgen spawnea una goroutine que:

1. Llama a `WAResolver.GetProfilePictureID(ctx, jid)` — metadata only,
   no descarga la imagen.
2. Si devuelve `""` → el JID no tiene foto configurada. Cachea
   `LastID=""` en el tracker y termina.
3. Si devuelve un ID distinto del cacheado → llama
   `GetProfilePicture(ctx, jid)` que hace un HTTP GET a la URL devuelta
   por `client.GetProfilePictureInfo`.
4. Sube vía `Client.UploadContactAvatar(ctx, contactID, data, mime)` —
   PUT multipart a `/api/v1/accounts/X/contacts/Y` con el campo
   `avatar`.
5. Actualiza el tracker con el ID nuevo.

**Aplica tanto a 1-on-1 (foto del usuario) como a grupos (foto del
grupo)**. WhatsApp expone la foto del grupo bajo el mismo método.

## Capa 2 — refresh con TTL (v0.31.1)

El tracker en memoria (`internal/bridge/avatar_tracker.go`) vive en el
proceso qrsgen y mantiene, por instancia y por JID:

- `LastChecked` — timestamp del último chequeo.
- `LastID` — último `info.ID` cacheado (vacío = sin foto).

En cada mensaje incoming, `sync()` también llama `maybeAvatarSync`
para contactos **existentes**. El gating es:

```go
if tracker.ShouldCheck(instance, jid, TTL) {
    go syncAvatar(...)
}
```

`ShouldCheck` es atómico — si `now - LastChecked > TTL`, bumpea el
timestamp y devuelve `true`. Esto evita que un burst de mensajes del
mismo JID spawnee múltiples sync simultáneos (verificado en tests con
20 goroutines concurrentes; solo una ve `true`).

El check **interno** del sync usa el ID:

- `GetProfilePictureID` (metadata, cheap) → comparar con `LastID`.
- Si idénticos → no descarga, solo registra el check.
- Si distintos → descarga + sube + `UpdateID` con el nuevo.

Resultado: para un contacto activo con foto estable, el coste de
mantenerlo sincronizado es **1 GET metadata por TTL** (HTTPS call
sobre el WebSocket whatsmeow, no descarga del bitmap).

## Capa 3 — refresh en tiempo real vía events.Picture (v0.31.2)

whatsmeow emite `*events.Picture` cuando un usuario o admin de grupo
cambia su foto. qrsgen subscribe vía:

```go
mgr.SetPictureHandler(func(instance string, jid types.JID, pictureID string, removed bool, r wameow.WAResolver) {
    incoming.HandlePictureChange(ctx, instance, jid, pictureID, removed, r)
})
```

`HandlePictureChange`:

1. Resuelve el `downstream.Client` y `Router` por instancia.
2. Busca el contact con `FindContact(jid)`. Si no existe, sale (el
   primer mensaje del JID disparará el sync inicial vía capa 1).
3. Resetea `LastID` en el tracker (forzar re-descarga aunque el ID
   coincida por algún caché stale).
4. Spawnea `syncAvatar` que descarga el nuevo bitmap y lo sube.

Resultado: cambio de foto en el móvil → avatar en Chatwoot actualizado
en ~1s sin esperar al siguiente mensaje, sin esperar al TTL del
tracker.

`Manager.SetPictureHandler` propaga el handler a todas las `Conn`
(existentes en el momento de la llamada + futuras creadas en
`startLocked`). Llamar antes de `Bootstrap` para capturar las
instancias auto-reconnect.

## Capa 4 — bulk re-sync para backfill (v0.31.3)

Las capas 1-3 cubren contactos creados o que reciben mensajes después
de adoptar la feature. Para contactos viejos (creados antes de v0.31.x
o inactivos), hay un endpoint que itera todo el inbox.

### Endpoint

```
POST /api/instances/{name}/avatars/resync
Authorization: Bearer $QRSGEN_API_TOKEN
```

### Comportamiento

1. Resuelve el `inbox_id` de la instancia.
2. Llama `Client.ListContactsByInbox(ctx, inboxID, page)` paginando
   desde page 1.
3. Por cada contacto cuyo `identifier` parsea como JID válido:
   - Resetea `LastID` en el tracker (bypass del check ID-idéntico).
   - Spawnea `syncAvatar`.
4. Cap defensivo: máximo **200 páginas** (~3000 contactos) para no
   colgar el proceso si el inbox tiene datos absurdos.

### Ejemplo

```bash
curl -X POST \
  -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  http://qrsgen:3100/api/instances/whatsapp-main/avatars/resync
```

### Respuesta

```json
{
  "instance": "whatsapp-main",
  "scanned": 234,
  "skipped": 5,
  "queued": 229,
  "pages": 16
}
```

| Campo | Significado |
|---|---|
| `scanned` | Contactos totales devueltos por el downstream. |
| `skipped` | Contactos cuyo `identifier` no parsea como JID válido (placeholders, sintéticos). |
| `queued` | Goroutines `syncAvatar` lanzadas. |
| `pages` | Páginas iteradas hasta encontrar la última (o el cap). |

`queued` es contadores de spawn, no de éxito real — los fallos
individuales se loguean pero no se cuentan en la respuesta. Para
auditar el resultado mira los logs (`avatar synced` info / `avatar
sync: ... failed` warn).

### Cuándo usarlo

- Tras la primera adopción de v0.31.x sobre una instancia con muchos
  contactos pre-existentes.
- Si limpiaste avatares manualmente en el downstream y quieres
  rehidratarlos.
- Tras un cambio de `inbox_id` que dejó contactos huérfanos sin
  sync histórico.

No tiene sentido llamarlo periódicamente — las capas 2 y 3 lo cubren
de forma incremental sin la pasada bulk.

## Modos de fallo

Todos los errores se loguean como `warn` y se ignoran. **Nunca
bloquean** la entrega del mensaje ni el flujo principal de `sync()`.

| Situación | Log | Efecto en downstream |
|---|---|---|
| JID sin foto | (silencio — caso esperado) | Letter-avatar sigue visible. Tracker cachea `LastID=""`. |
| Foto privada / account restringida | `avatar sync: get id failed` o `download failed` | Letter-avatar sigue visible. Tracker bumpeó timestamp — reintento en el próximo TTL. |
| HTTP GET de la URL Meta falla | `avatar sync: download failed` | Sin cambios. Reintento en próximo TTL. |
| `UploadContactAvatar` falla (4xx/5xx Chatwoot) | `avatar sync: upload to downstream failed` | Avatar previo (si existía) intacto. Tracker NO actualiza `LastID` — reintento en próximo TTL. |
| `events.Picture` para JID no existente en downstream | (silencio en `HandlePictureChange` → return early) | Sin cambios. Se sincronizará vía capa 1 al primer mensaje. |
| Bulk re-sync sobre inbox vacío | (silencio) | Respuesta `{scanned: 0, ...}`. |

## Caveats y limitaciones

- **Tracker in-memory**. Restart del proceso = tracker vacío. En el
  worst case, los primeros mensajes tras restart provocan una ronda
  extra de chequeos `GetProfilePictureID` (1 metadata call por JID
  activo dentro del TTL). No re-descargan la imagen si el ID no
  cambió. Es deliberado — evita migración DB para una optimización
  marginal.
- **`info.ID` no es un hash universal**. Es un identificador opaco
  que WhatsApp asigna a cada versión de la foto. Cambios de foto sí
  generan ID nuevos; reposiciones triviales también. No es viable
  comparar fotos por contenido.
- **Sin garantías de orden**. Si llegan dos `events.Picture` para el
  mismo JID en ráfaga (raro pero posible), ambas goroutines de sync
  pueden correr en paralelo. La última `UploadContactAvatar` que
  termine gana. Suficiente para el caso real (el usuario cambia la
  foto, la sincronizamos a su nuevo valor).
- **Sin retry exponencial**. Si Chatwoot devuelve 5xx, no hay backoff
  — el próximo intento es en el siguiente TTL (o cuando el usuario
  cambie de foto). Para garantías más fuertes, llama
  `/avatars/resync` manualmente.
- **Rate limits del downstream**. El bulk re-sync puede generar
  muchas requests concurrentes a Chatwoot. No hay throttle interno
  (las goroutines spawnean libremente). Si Chatwoot lo rate-limit-ea,
  los uploads fallarán con warn — vuelve a llamar pasada una pausa.
- **Grupos**: el avatar del grupo viene del lado WhatsApp. Si tu
  downstream usa `identifier=<groupJID>` con `@g.us`, el sync funciona
  igual que con 1-on-1.
- **No elimina avatares**. Si `events.Picture` llega con `removed=true`
  y `pictureID=""`, qrsgen cachea `LastID=""` pero **no** borra el
  avatar previo en downstream — queda el último conocido. Decisión
  consciente: borrar avatar via Chatwoot API requiere otro endpoint
  y el caso "usuario quitó la foto" es raro vs. ruido.

## Verificar que funciona

Tras enviar un mensaje desde un número con foto:

```bash
# Logs del container
docker logs qrsgen 2>&1 | grep "avatar synced"
# → time=... msg="avatar synced" contact_id=42 size=18432 mime=image/jpeg avatar_id=... jid=34600000000@s.whatsapp.net
```

En Chatwoot, abre el contacto → el avatar circular ya debería mostrar
la foto en lugar de las iniciales.

Si no aparece nada:

1. ¿Logs muestran `avatar sync: ... failed`? → mira el campo `err`.
2. ¿`QRSGEN_AVATAR_SYNC=false`? → master switch off.
3. ¿El JID tiene foto pública en WhatsApp? Algunos perfiles la tienen
   restringida a contactos. qrsgen ve lo que WhatsApp expone al número
   conectado.

## Glosario

**Letter-avatar**: avatar por defecto generado por el downstream (ej.
Chatwoot) con las iniciales del nombre cuando no hay imagen subida.
El avatar sync lo reemplaza por la foto real de WA.

**`info.ID`**: identificador opaco que WhatsApp asigna a cada versión
de la foto de perfil. Cambia cada vez que el usuario sube una foto
nueva. qrsgen lo usa para detectar cambios sin descargar el bitmap.

**Tracker**: estructura in-memory (per-instancia, per-JID) que
recuerda el último `info.ID` cacheado y cuándo se chequeó por última
vez. Define la cadencia del refresh con TTL.

**Fire-and-forget**: patrón donde una operación se lanza en goroutine
sin esperar su resultado. Los errores se loguean pero no se propagan
al caller. Apropiado cuando el éxito del sync no es crítico para el
flow principal (la entrega del mensaje).

**Bulk re-sync**: pasada completa sobre todos los contactos de un
inbox para forzar el sync del avatar. Útil como backfill puntual,
no como mecanismo periódico.

**`events.Picture`**: evento que whatsmeow emite cuando alguien
(usuario o admin de grupo) cambia su foto de perfil. qrsgen lo
subscribe para sync en tiempo real.

**`GetProfilePictureID` vs `GetProfilePicture`**: el primero devuelve
solo el ID (metadata), el segundo descarga la imagen completa.
qrsgen prefiere el primero para decidir si hace falta el segundo.
