# Changelog

Todos los cambios notables se documentan aquí. Sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) y [SemVer](https://semver.org/).

## [Unreleased]

<!--
Roadmap hacia v1.0.0 (no apurar):

Antes de tagear v1.0.0, queremos asegurar:

1. **Tests E2E reales en dertochip** de:
   - [ ] Group rename → events.GroupInfo → activity msg en Chatwoot
   - [ ] Add/remove participants → activity msg
   - [ ] History import single + bulk con cobertura amplia
   - [ ] Identity change real (esperar a que un contacto cambie de
     device — opt para mock si no llega naturalmente)
   - [ ] Quote/reply bidireccional con varios tipos de media

2. **Integration tests adicionales**:
   - [ ] Endpoints group admin (httptest stub para validar
     payload + autorización)
   - [ ] History import edge cases (timeout del peer, conv
     no existe, msg malformado)
   - [ ] Group events con nil sender / fromFullSync

3. **Robustez**:
   - [ ] Revisión de concurrencia en el goroutines de retroactive
     update + history import (sin races)
   - [ ] Error handling consistente: ¿qué pasa si Chatwoot devuelve
     5xx en medio de un bulk import?
   - [ ] Edge cases descubiertos en producción

4. **Documentación**:
   - [x] `docs/integrations/history-import.md` (escrita; ON_DEMAND, bulk, retry, métricas)
   - [x] `docs/integrations/group-admin.md` (endpoints + permisos + recetas)
   - [x] `docs/api/groups.md` (endpoint reference completo)
   - [x] Migration guide `docs/migrations/v0-to-v1.md` consolidado (incluye v0.53.x)

5. **Soak time**:
   - [ ] N días en producción (dertochip + keysoluciones) sin
     incidencias post v0.48.x
   - [ ] Métricas Prometheus revisadas — sin spikes anómalos
   - [ ] Logs sin warnings/errors recurrentes

v1.0.0 cuando los items críticos estén marcados. No hay prisa.
-->

## [0.62.0] - 2026-06-01

Benchmarks de hot paths del bridge.

### Added

- **`internal/bridge/benchmarks_test.go`** con 4 benchmarks:
  - `BenchmarkRenderSenderHeader_Default` — template default,
    saved=true: ~915ns/op, 2 allocs/op.
  - `BenchmarkRenderSenderHeader_CustomTemplate` — template custom
    con tokens variados: ~945ns/op, 2 allocs/op.
  - `BenchmarkRenderSenderHeader_UnsavedTilde` — branch del `~`:
    ~775ns/op, 2 allocs/op.
  - `BenchmarkResolveMentions_NoMentions` — caso común sin @-mentions:
    ~14ns/op, 0 allocs/op (early-return funciona).

### Notes

Run con:

```bash
go test -bench=. -benchmem -run=^$ ./internal/bridge/...
```

Los benchmarks no se ejecutan en `go test ./...` normal — sólo con
`-bench`. Pensados para detectar regresiones al cambiar el parser de
templates o el resolver de mentions en futuros refactors.

Cifras actuales: ~1µs por mensaje incoming para el render del header.
A 1000 msgs/s eso es 1ms total — negligible vs el ~10-50ms del POST
a Chatwoot. No hay hot spot que justifique optimización ahora.

**pprof live**: no expuesto en este release. Activarlo requiere una
env como `QRSGEN_PROFILE_ENABLED=true` que monte `/debug/pprof/*` —
pendiente del próximo minor por temas de security (no exponer pprof
por default).

## [0.61.0] - 2026-06-01

OpenAPI 3.0 spec inicial.

### Added

- **`docs/api/openapi.yaml`** (300+ líneas): spec OpenAPI 3.0.3
  cubriendo los endpoints estables más usados (12 paths, 7 schemas).
  Pensado para:
  - Generar clientes en otros lenguajes (Postman / `openapi-generator`).
  - Documentación interactiva (Swagger UI / Redoc).
  - Validación contractual entre frontend/CI y el server.

### Endpoints cubiertos

- meta: `/api/health`, `/api/version`
- instances: list, create, get, delete, qr (binario PNG), refresh-qr, logout
- groups: GET info de grupo
- history: import on-demand con `days` param
- messages: edit (v0.60.0)
- tenants: list
- jobs: GET status

### Notes

NO cubre todavía: tenant CRUD completo, retroactive endpoints,
group admin (rename/participants/topic/locked/announce), bulk
endpoints, history bulk variants. Se añadirán incrementalmente en
próximos minors. El skeleton actual es suficiente para que un
integrator pueda implementar onboarding básico.

Validable con `swagger-cli validate docs/api/openapi.yaml` o
visualizable en https://editor.swagger.io.

## [0.60.0] - 2026-06-01

Edit message support — soportar la operación de editar el contenido
de un mensaje saliente ya entregado. Hasta este release la memoria
operacional decía "edit msg not supported"; ahora flipped.

### Added

- **`wameow.Conn.EditMessage(ctx, remoteJid, waid, newContent)`**:
  primitiva que envuelve `whatsmeow.BuildEdit` + `SendMessage`.
  Devuelve el WAID (no cambia entre ediciones — siempre el original).
- **`POST /api/instances/:name/messages/:waid/edit`** (nuevo endpoint):
  Body `{"chat":"<jid>", "content":"new text"}`. Edita el mensaje
  identificado por `waid`. Respuesta 200 `{"waid":"<same>", "edited":true}`.
- **`cmd/server/routes_messages.go`**: nuevo archivo para esta familia
  de endpoints. Futuros candidatos: delete (revoke), forward,
  react-on-behalf.

### Restricciones de WhatsApp

- Solo se puede editar mensajes salientes (fromMe).
- Hay una ventana temporal (~15 min) tras la cual el server rechaza.
- El cliente del destinatario debe estar online para aplicar el
  cambio; offline lo verá editado al reconectarse.

### Notes

- **No tests live**: probar requiere una instancia paireada + un
  mensaje saliente dentro de los últimos 15 min. La primitiva está
  unit-test-able a través de la API pero los breakages reales
  vendrían de la integración con whatsmeow upstream, no del wiring.
- **No webhook trigger todavía**: Chatwoot api_channel no expone un
  evento "message_updated" reliable, así que de momento el flujo
  edit→qrsgen→whatsapp es purely explicit-API. Future work podría
  detectar updates de Chatwoot y autocallar este endpoint.

## [0.59.0] - 2026-06-01

Downstream resilience: respeto al header `Retry-After` que envía
Chatwoot en respuestas 429.

### Added

- **`downstream.RateLimitError`**: nuevo error tipado que llevan las
  respuestas 429 del downstream. Campos `RetryAfter time.Duration` y
  `Body string`. Los callers pueden hacer `errors.As(err, &rl)` para
  detectarlo y respetar el server vía exponential backoff.
- **`parseRetryAfter`**: helper interno que interpreta el header
  según RFC 7231. Soporta ambos formatos:
  - `<delta-seconds>` (entero) — caso típico de Chatwoot.
  - `<HTTP-date>` (RFC 1123).
- **5 tests** (`retry_after_test.go`):
  - parseRetryAfter con segundos válidos, inválidos, negativos.
  - parseRetryAfter con HTTP-date futuro y pasado.
  - Client.request devuelve `*RateLimitError` en 429.
  - 429 sin header → `RetryAfter = 0`.
  - 500 NO es RateLimitError (sólo 429 específicamente).

### Notes

Cambio NO-breaking: callers existentes que solo hacen `err != nil`
siguen funcionando. Los nuevos que quieran ser más inteligentes
pueden hacer `errors.As`.

**Circuit breaker deferido**: el patrón requiere estado compartido
(contador de fallos consecutivos, ventana temporal, half-open
transitions) que es no trivial para producción. Sin un escenario
real de outage prolongado, añadirlo es especulativo. Se queda para
un release futuro si emerge la necesidad operativa.

## [0.58.0] - 2026-06-01

Cobertura extra del `msgHistoryTracker.DropInstance` (introducido en
v0.53.3) en el suite de tests integration.

### Added

- **`TestIntegration_MsgHistory_DropInstancePersistedClean`**: inserta
  3 entries para `instA` + 2 para `instB`, llama `DropInstance(instA)`,
  verifica que (1) `RowsAffected = 3`, (2) `instA` queda en 0 rows en
  DB, (3) `instB` queda intacta con 2 rows. Sigue el mismo patrón del
  suite existente (`INTEGRATION_PG_DSN` env-gated, sin build tag).

### Notes

Inicialmente planteé un helper `internal/testdb` con aislamiento de
schemas y un build tag `integration` propio. Descartado porque el repo
ya tiene infrastructure de integration tests con env-var-gate
(`INTEGRATION_PG_DSN`) — añadir paralelo lo confundiría. Future tests
deben seguir el patrón existente.

## [0.57.0] - 2026-06-01

Security pass: tests adicionales del webhook HMAC middleware.
Sin cambios de comportamiento, sólo cobertura de paths que antes
estaban sin probar.

### Added

- **4 tests nuevos en `cmd/server/hmac_test.go`**:
  - `TestWebhookHMAC_PerTenantOverridesGlobal` — el lookup del
    tenant tiene precedencia sobre el `WEBHOOK_HMAC_SECRET` global.
    Cubre la rama de resolución v0.26.0 que estaba sin test.
  - `TestWebhookHMAC_TenantEmpty_FallsBackToGlobal` — cuando el
    tenant no tiene secret propio, se usa el global.
  - `TestWebhookHMAC_BothEmpty_AllowsPassthrough` — backward-compat
    sin auth cuando ningún secret está configurado.
  - `TestWebhookHMAC_EmptyBodyValidSig` — edge case: POST con body
    vacío con firma válida del string vacío. Pasa.
- Nuevo helper `runWebhookMWWithLookup` que acepta el `tenantHMACSecretLookup`
  funcional para tests del path per-tenant.

### Notes

Total HMAC tests: 6 → 10. Cubre los 3 niveles de la resolución de
secret (tenant → global → no-auth) + edge cases (empty body, missing
header, malformed signature, wrong prefix).

**Audit retention diferido**: la tabla `bridge_audit_log` tiene
triggers que rechazan UPDATE/DELETE por diseño (tamper-evidence).
Implementar un cron de retención requeriría o (a) dropear los
triggers (defeats the purpose) o (b) particionar la tabla por mes y
DROP partitions antiguas — diseño más grande, postpuesto.

## [0.56.0] - 2026-06-01

Continuación del split, ahora sobre `internal/bridge/incoming.go`.
Sin cambios de comportamiento. Mismo patrón que el split de
`cmd/server/main.go` (v0.54.2/3): mover bloques cohesivos a archivos
hermanos en el mismo paquete.

### Changed

- **`incoming.go`**: 2155 → 1840 líneas (-15%).
- **`internal/bridge/incoming_reactions.go`** (nuevo, 127 líneas):
  todo el código de `handleReaction` (procesa ReactionMessage,
  postea quote-reply visual vía `content_attributes.in_reply_to`
  desde v0.53.2).
- **`internal/bridge/incoming_retroactive.go`** (nuevo, 215 líneas):
  `HandleContactUpdate`, `applyRetroactiveUpdates`, `ReconcileResult`
  type y `ReconcileSavedContacts`. Toda la lógica del retroactive
  name update (v0.40.0+) queda agrupada.

### Notes

Queda pendiente para próximos minors mover `HandleReceipt`,
`HandleChatPresence`, `HandlePictureChange` y `Resync*Avatars` a sus
propios archivos — son ~300 líneas adicionales fáciles. No las
toqué en este release para mantener el diff manejable.

## [0.55.0] - 2026-06-01

### Added

- **`GET /api/version`**: devuelve `{version, commit, build_date, go_version}`.
  Pensado para diagnóstico (qué SHA está corriendo en producción) y
  health-check de despliegues post-deploy. Sin DB hit, sin auth-cost
  significativo. Endpoint detrás de la auth global como el resto.
- **Build metadata** vía ldflags: `main.commit` y `main.buildDate` se
  inyectan desde GoReleaser (`-X main.commit={{.ShortCommit}}`,
  `-X main.buildDate={{.Date}}`). En builds locales con `go build`
  ambos quedan "unknown" — lo que es esperado.

### Notes

`/api/health` ya estaba rico desde antes (DB ping, instance counts,
outbox pending). No tocado en este release. Si quieres añadir el
estado del backdate worker requeriría exponer estado interno del
goroutine — postpuesto a v0.57+ si emerge necesidad operativa.

## [0.54.4] - 2026-06-01

Issue #9 parte 2: parámetro `days` per-request en el endpoint
on-demand de history import.

### Added

- `POST /api/instances/:name/history/import?days=N` — nuevo query
  param opcional que acota la antigüedad máxima de los msgs
  importados sin tocar la config global del proceso. Clamp `[1, 30]`
  consistente con `QRSGEN_HISTORY_IMPORT_DAYS`. 0/ausente = usar
  el default global.
- **`Incoming.ImportHistoryOnDemandWithMaxAge(...)`** — variante de
  `ImportHistoryOnDemand` con un `maxAge time.Duration` extra. El
  wrapper original `ImportHistoryOnDemand` se mantiene para
  compatibilidad (delega con maxAge=0).
- **`runHistoryImport(ctx, instance, data, r, maxAgeOverride)`** —
  firma extendida con override per-request. maxAgeOverride=0 usa el
  default de `historyCfg.maxAge`.
- Tests en `history_import_test.go` para los 3 casos de override
  (zero/explícito/sub-día).

### Notes

Cierra issue #9 completo. La parte 1 (backdate worker) se shipped
en v0.54.0; la parte 2 (days param) en este release. El endpoint
ahora es lo suficientemente expresivo para importar selectivamente
sin pre-configurar el proceso entero.

Ejemplo de uso:
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "https://qrsgen/api/instances/ATC/history/import?chat=34600000000@s.whatsapp.net&days=3&count=200"
```

## [0.54.3] - 2026-06-01

Continuación del split de `cmd/server/main.go`. Sin cambios de
comportamiento; sólo reorganización de archivos.

### Changed

- **Tenants, jobs, history, usage, bulk** extraídos cada uno a su
  propio archivo `cmd/server/routes_*.go`. `main.go` pasa de 1592
  (v0.54.2) a 1273 líneas (-20% acumulado desde v0.54.2 baseline,
  -29% desde v0.54.1 original).
- Cada `register*Routes(api, deps...)` recibe sólo las dependencias
  que necesita — sin Server struct (overkill por ahora), sin globales.
  El compilador valida en tiempo de build que ninguna ruta se "olvide".

### Added

- `cmd/server/routes_tenants.go` (5 endpoints, ~134 líneas)
- `cmd/server/routes_jobs.go` (2 endpoints, ~30 líneas)
- `cmd/server/routes_history.go` (3 endpoints, ~161 líneas)
- `cmd/server/routes_usage_bulk.go` (4 endpoints, ~82 líneas)

### Notes

Lo que queda en main.go (1273 líneas) es mayoritariamente: setup
inicial (config, pool, mgr, bridge), middleware, endpoints de
health/instances/QR/webhook (que están más acoplados a estado
local del proceso), defers de shutdown, y los adapters internos
(senderAdapter, spamguardAdapter). Más extracciones serían ROI
decreciente y posiblemente perjudicial (forzar un Server struct
sólo por hacer split).

## [0.54.2] - 2026-06-01

Polish técnico pre-v1.0.0. Sin features, sin cambios de comportamiento;
sólo limpieza de deuda detectada en la audit del día.

### Changed

- **Doc comments en 35 símbolos exportados** previamente sin describir.
  Mejora godoc y reduce el "what does this do?" mental load. Incluye
  `Config`, `Load`, `Manager`, `Client`, todos los `Send*` adapters de
  `senderAdapter`/`spamguardAdapter`, y el filtro `filteredWALog`.
- **Bump `go.mau.fi/whatsmeow`** de `8d3700152a69` a `9ff5508a26c2`
  (1 día más reciente del upstream). Arrastra `libsignal v0.2.1→v0.2.2`
  y `edwards25519 v1.1.1→v1.2.0`. Tests verdes, sin breaks.

### Added

- **`cmd/server/routes_groups.go`** — extracción de los 8 endpoints
  de admin de grupos (`/instances/:name/groups/*`) a su propio archivo.
  Reduce `main.go` de 1792 a 1592 líneas (-11%). Lógica idéntica:
  `registerGroupRoutes(api, mgr)` reemplaza el bloque inline.
- **Tests `internal/outbox/outbox_test.go`**: cubre `DefaultConfig`,
  `SetEncryptionKey` (incluido el path de validación de longitud) y
  los helpers `truncate` + `previewFromPayload`. Coverage del paquete
  pasa de 14.0% → 22.2%.
- **Tests `internal/manager/manager_test.go`** añade
  `TestInBootstrapWindow` y `TestOwnerTag_*` (con `stubResolver`).
  Cubre los 3 casos del bootstrap window + ambos paths del wrapper
  con/sin resolver. Coverage 6.4% → 7.4%.

### Notes

Tests totales: 12/12 paquetes verdes, 0 vet warnings, lint CI ✓.
Tamaño qrsgen tras el split: 32 archivos Go + 36 archivos de test.
La auditoría que originó este release está documentada en commit
message del fix anterior del mismo día (`fix(lifecycle)` en
integration repo).

## [0.54.1] - 2026-06-01

Fix puntual al worker introducido en v0.54.0. Descubierto al intentar
activar el backdate en dertochip por primera vez.

### Fixed

- **Backdate SQL casting**: Chatwoot define `messages.content_attributes`
  como `json` (no `jsonb`), así que los operadores `?` y `->>` no
  funcionan directamente. v0.54.0 fallaba en cada tick con
  `ERROR: operator does not exist: json ?` antes de actualizar nada.
  Añadidos casts explícitos a `jsonb` en ambos lugares de la query.
- Comentario en `tick()` documenta la decisión para que el próximo
  reader entienda por qué el cast es necesario.

Sin v0.54.0 desplegado en producción con `CHATWOOT_DB_URL` set la
feature se reduce a un log INFO de arranque, así que el bug nunca
llegó a impactar a usuarios reales — pero impedía completar la
validación end-to-end del worker.

## [0.54.0] - 2026-06-01

Feature: **backdate worker** que corrige los `created_at` de los msgs
importados en la DB de Chatwoot. Resuelve el dolor real visto en el
soak de v0.53.3 — los msgs históricos aparecían todos con timestamp
"now" porque Chatwoot ignora silenciosamente el `created_at` que
qrsgen envía vía `api_access_token` (solo super-admin puede backdatear
por API). qrsgen lleva mandando `external_created_at` en cada POST
desde v0.46.0; este worker recoge ese dato y lo aplica vía UPDATE
directo al campo `created_at`.

### Cómo funciona

- Opt-in vía `CHATWOOT_DB_URL=postgres://user:pass@host:port/chatwoot`.
- Sin esa env la feature queda OFF y el comportamiento es idéntico a v0.53.x.
- Con la env, qrsgen abre una segunda conexión pgxpool a la DB de
  Chatwoot y arranca un goroutine que cada N segundos hace:
  ```sql
  UPDATE messages SET created_at = to_timestamp((content_attributes->>'external_created_at')::bigint)
  WHERE id IN (SELECT id FROM messages
               WHERE content_attributes ? 'external_created_at'
                 AND created_at > to_timestamp(...) + interval 'N seconds'
               ORDER BY id DESC LIMIT M)
  ```
- Idempotente: el WHERE filtra rows ya backdated, sucesivos ticks no rehacen trabajo.
- Tolerance de 5s default — evita loops por jitter de reloj.

### Added

- `internal/bridge/backdate.go` — `Backdater` con `Run(ctx)` bloqueante.
- `internal/config/config.go`:
  - `ChatwootDBURL` (`CHATWOOT_DB_URL`) — DSN Postgres de Chatwoot. Empty = off.
  - `BackdateInterval` (`QRSGEN_BACKDATE_INTERVAL`, default `30s`).
  - `BackdateBatchSize` (`QRSGEN_BACKDATE_BATCH_SIZE`, default `500`).
  - `BackdateToleranceSec` (`QRSGEN_BACKDATE_TOLERANCE_SEC`, default `5`).
- Métrica `qrsgen_realtime_events_total{feature="backdate", result="ok|error", instance="all"}`.
- Tests del constructor: defaults, valores custom, valores negativos.

### Notes

Acopla qrsgen al schema de la tabla `messages` en Chatwoot (concretamente
a `content_attributes`, `created_at`). Si Chatwoot cambia su schema
internamente, esta feature puede romperse en silencio (ticks devuelven
0 rows o error). El tradeoff es aceptable porque la integración con
Chatwoot ya existe vía API en otros lugares.

Pendiente para v0.55+ (issue #9, parte 2): añadir parámetro `days=N`
al endpoint `POST /api/instances/:name/history/import` para acotar
la ventana sin tocar la config global. Hoy se controla vía
`QRSGEN_HISTORY_IMPORT_DAYS` que aplica a todo el instance.

## [0.53.3] - 2026-06-01

Hardening pre-v1.0.0 a partir de observaciones reales durante el soak:
tres fixes pequeños de calidad operativa, sin features nuevas.

### Fixed

- **Goroutine zombi de `HistorySync` tras `Delete`**: el handler de
  `*events.HistorySync` se lanzaba con `context.Background()`, lo que
  significa que al borrar o desconectar la instancia los goroutines en
  vuelo seguían iterando msgs y POSTeando al downstream durante minutos,
  generando 404s ("Resource could not be found") porque el inbox ya
  no existía. Añadido un `lifecycleCtx` per-`Conn` que se cancela en
  `Disconnect()`; el bucle de `runHistoryImport` ya respeta `ctx.Err()`
  y ahora sale limpio.
- **Leak de `msgHistoryTracker` per-instance**: el `data map[string][]trackedMsg`
  nunca reclamaba las keys `"instance|sender"` tras un `Manager.Delete`,
  y la tabla `bridge_msg_history` retenía rows huérfanas. Añadido
  `msgHistoryTracker.DropInstance(ctx, name)` + un nuevo hook
  `Manager.SetInstanceDeleteHandler` que se wirea en `main.go` y limpia
  memoria + DB al borrar.
- **Ruido en logs del cliente whatsmeow**: cada chat durante history sync
  emite `WARN Failed to delete history sync media from server: 400` —
  no es accionable y satura los logs (cientos de líneas por sesión).
  Añadido `filteredWALog` que envuelve el `waLog.Logger` y suprime
  exclusivamente este pattern; el resto pasa sin tocar.

### Added

- `internal/wameow/filtered_log.go` + tests.
- `msgHistoryTracker.DropInstance` + tests.
- `Manager.SetInstanceDeleteHandler` / `onInstanceDelete` callback.
- `Incoming.DropInstanceTracking(ctx, name)` wrapper.

### Notes

Cero cambios de API pública para clientes API HTTP. La feature solo
afecta el comportamiento interno tras `DELETE /api/instances/:name`.

## [0.53.2] - 2026-06-01

Feature: **reacciones como quote-reply visual del msg target**.
Chatwoot mostraba la reacción como un msg suelto sin contexto del
msg al que se reaccionó. Ahora qrsgen postea la reacción con
`content_attributes.in_reply_to` apuntando al msg target → Chatwoot
renderiza visualmente la reacción dentro del bubble del msg original.

### Antes
```
+34611887663 · ~Agustina
reaccionó con 👍
```
(suelto en la conv — el agente no sabe a qué se reaccionó)

### Ahora (default)
La reacción aparece como **quote-reply** del msg target. Chatwoot
muestra el msg original arriba y la reacción debajo, visualmente
enlazados.

### Added

- **`PostMessageReq.InReplyTo int`**: cuando > 0, qrsgen POSTea
  `content_attributes: {in_reply_to: <id>}` para que Chatwoot
  renderice quote nativo.
- **`msgHistoryTracker.FindByWAID(ctx, instance, waid)`**: lookup
  del trackedMsg por su WAID. Memory first (linear scan), DB
  fallback. Devuelve el `Chatwoot msg_id` para el `in_reply_to`.
- **`Incoming.SetReactionAsReply(bool)`** + field `reactionAsReply`
  default `true`.
- En `handleReaction`: si `reactionAsReply=true` y el WAID target
  está tracked, incluye `InReplyTo`. Si no se encuentra el WAID
  target (msg pre-v0.44.0 o no recibido por qrsgen), degrada al
  formato standalone (current).

### Config

- **`QRSGEN_REACTION_AS_REPLY`** (default `true`): activa la feature.
  Desactivar si tu downstream no renderiza `content_attributes.
  in_reply_to` bien o si prefieres el formato suelto.

### Tests

- `TestMsgHistoryTracker_FindByWAID` cubriendo found / miss /
  empty waid / per-instance isolation.

### Migration notes

- Sin breaking changes. Feature on por default — solo cambia el
  POST body cuando el WAID está tracked. Reacciones a msgs no
  tracked (pre-v0.44.0) mantienen comportamiento previo.
- Si tu downstream NO renderiza `in_reply_to` (otros, no Chatwoot),
  el campo se ignora y la reacción aparece como msg suelto igual
  que antes.

## [0.53.1] - 2026-06-01

UX tweak post v0.53.0: cuando una @-mención apunta a un **LID sin
PN mapeado localmente** (caso común para usuarios con privacy
enabled o que aparecieron por primera vez en un grupo), v0.53.0
dejaba el JID raw `@148855681191942`. v0.53.1 lo resuelve dos
maneras:

### Fix 1: GetGroupInfo on-demand para popular el LID store

Cuando llega una mención LID sin resolución vía `PNForLID` y el
chat es un grupo, qrsgen hace una llamada `GetGroupInfo(group)`
cuyo side effect es popular el `LIDs` store de whatsmeow con las
mappings de los participantes. La próxima vez (y muchas veces
inmediatamente en el mismo msg) el LID se resuelve a phone.

Con cache TTL **1h por grupo** para evitar spammear el server WA
si llegan muchas menciones LID seguidas.

### Fix 2: RedactedPhone fallback

WhatsApp expone `ContactInfo.RedactedPhone` (`+1∙∙∙∙∙∙∙∙80`) para
LIDs que se ven en grupos cuando el usuario opta por privacidad.
Si `PNForLID` no resuelve (después del refresh on-demand), qrsgen
ahora usa este redacted phone — sigue siendo más legible que un
LID raw.

### Cadena final de fallback (renderMention)

1. `Saved name` (FullName/FirstName de la agenda)
2. `PushName / BusinessName` (con `~` automático si no saved)
3. `+phone` resuelto vía PNForLID
4. `+phone redactado` (v0.53.1, privacy mode)
5. Texto raw `@<lid>` si todo lo demás falla

### Para usernames

Próximamente WhatsApp expondrá usernames como tal en la API. Cuando
whatsmeow añada el campo `Username` a `ContactInfo`, qrsgen lo
integrará en la cadena de fallback (probablemente entre 2 y 3).
Por ahora WA empaqueta el username en `PushName` cuando aplica, así
que el comportamiento actual ya lo cubre transparentemente.

### Added

- **`WAResolver.RedactedPhone(jid) string`** + impl en `Conn`
  consulta `ContactInfo.RedactedPhone`.
- **`WAResolver.RefreshGroupLIDs(ctx, group) error`** + impl en
  `Conn` (wraps `GetGroupInfo`).
- **`lidRefreshTracker`**: cache TTL 1h por grupo para evitar
  refrescos repetidos.
- **`maybeRefreshLIDs`** invocado desde `handleMessage` antes de
  resolver menciones.

### Tests

- `TestResolveMentions_LIDFallsBackToRedactedPhone`
- `TestResolveMentions_LIDNoFallbackStaysRaw`
- `fakeResolver.RedactedPhone` + `RefreshGroupLIDs` stubs +
  `redactedPhones map` field.

### Migration notes

- Sin breaking changes funcionales.
- `WAResolver` interface gana 2 métodos. Si tienes implementaciones
  custom, añade stubs.

## [0.53.0] - 2026-06-01

Feature: **resolución de @-menciones inline**. Hasta ahora, cuando
un usuario en WhatsApp mencionaba a otro participante (`@Ivan`
seleccionado del picker), el body llegaba al downstream como
`@148855681191942` (el JID raw — típicamente un LID
incomprensible). El cliente WA receptor une el body con
`ContextInfo.MentionedJID` para mostrar el nombre real; qrsgen
hace lo mismo ahora.

### Antes
```
@148855681191942 buenos días
```

### Ahora (default `@$name`)
```
@~Ivan Madrid buenos días
```

Aplica al cuerpo del msg Y a captions de media. Hereda toda la
cadena de resolución de qrsgen:
- `resolveJIDNameSaved` (fix v0.39.9): si LID con PN saved, usa
  nombre canónico del PN.
- Tilde `~` automática si el contacto NO está saved (consistente
  con group prefix v0.39.5).
- Fallback a phone E.164 si no hay nombre resoluble.

### Added

- **`resolveMentions(text, mentionedJIDs, r, template) string`**:
  helper que sustituye los `@<jid_user>` inline por
  `@<nombre resuelto>` usando `ContextInfo.MentionedJID`.
- **`renderMention(jid, r, template) string`**: builder del
  reemplazo individual respetando el template configurable.
- **`Incoming.SetMentionTemplate(template)`** + field
  `mentionTemplate`. Default `@$name`.
- **`MentionTemplateDefault`** = `@$name` const exportada.

### Config

- **`QRSGEN_MENTION_TEMPLATE`** (default `@$name`). Vacío
  desactiva la feature (text raw). Tokens:
  - `$name` — nombre canónico con `~` automático si no saved.
  - `$phone` — E.164 con `+` (si resoluble).
  Ejemplos:
  - `@$name` (default) → `@~Ivan` o `@Ivan Saved`
  - `@$name ($phone)` → `@~Ivan (+34611...)`
  - `**$name**` → `**~Ivan**` (bold sin `@`)

### Tests

- 8 unit tests en `mentions_test.go`:
  - `PNSaved`, `PNUnsavedTilde`, `LIDWithSavedPN`
  - `NoResolverFallsBackToPhone`, `EmptyTemplateDisables`
  - `MultipleMentionsAllResolved`, `TokenNotInTextIsNoOp`
  - `CustomTemplate`

### Migration notes

- Sin breaking changes. Feature ON por default. Si tu downstream
  tiene scripts que dependían del `@<jid_user>` raw, setear
  `QRSGEN_MENTION_TEMPLATE=""` recupera el comportamiento
  pre-v0.53.0.
- Las menciones desde `ContextInfo.MentionedJID` solo llegan
  cuando el sender usa el @-picker de WhatsApp. Texto literal
  como `@usuario_random` no es una mención formal y se queda
  como está.

## [0.52.1] - 2026-06-01

Bug fix de UX: el reply-to outgoing fallaba silenciosamente cuando
el agente quote-replea a un msg outgoing del propio agente.

### Problema observado en producción

Agente en Chatwoot hace quote-reply a un msg outgoing previo (suyo
mismo). El mensaje llega a WhatsApp como SendText plano sin la
cita visible. Sin logs visibles del fallback.

Causa: `msg_history` solo trackea incoming (`!fromMe` en
handleMessage). Los msgs outgoing nunca se registraban → el
lookup `FindByChatwootMsgID` no encontraba anchor para reply.

### Fixed

- **`Outgoing.trackOutgoing`**: tras un SendText/SendMedia
  exitoso, registra el msg en `msg_history` con su WAID + body.
  Key del tracker: chatJID (`remoteJid`) — mismo schema que
  incoming en 1:1 y sintético en grupos. FindByChatwootMsgID
  hace lookup linear por msgID, así que la key es organizativa.
- **Log subido de Debug a Info** en `resolveReplyContext` cuando
  el lookup falla:
  ```
  "reply-to: trackedMsg not found, sending plain (no quote in WA)"
  ```
  Con hint sobre la causa probable (msg pre-v0.44.0 o instance
  recién reseteada).

### Después del fix

- Quote-reply a outgoing → ahora encuentra anchor → reply nativo
  con ContextInfo poblado.
- Quote-reply a incoming pre-v0.44.0 → sigue fallando (no hay
  WAID en la row) pero ahora log claro en producción.
- Quote-reply a incoming post-v0.44.0 → funciona como antes.

### Limitación residual

Msgs outgoing posteados antes de v0.52.1 quedan sin trackear (no
hay backfill retroactivo). Solo los nuevos outgoing aplican.

### Migration notes

Sin breaking changes. El tracker capacity per-sender (`200` default,
`QRSGEN_RETROACTIVE_CAP_PER_SENDER`) ahora cuenta también outgoing
para esa key — en chats activos puede llenarse antes de los 200
incoming previos esperados. Subir el cap si afecta tu uso:
`QRSGEN_RETROACTIVE_CAP_PER_SENDER=500`.

## [0.52.0] - 2026-06-01

Feature: **async job pattern** para bulk operations + **migration
guide consolidado** v0.28.x → v1.0. Última minor antes del soak
period.

### Added

- **`bridge.JobStore`**: in-memory job tracker con TTL 24h.
  - `Create(type, instance) → Job` con UUID
  - `Start/Complete/Fail` para transiciones de status
  - `Get(id)` / `List()` snapshots
  - `RunAsync(job, fn)` ejecuta `fn` en goroutine y maneja status
  - Cleanup loop cada 1h purga jobs `completed|failed` > TTL

- **`POST /api/instances/:n/history/import-all-async`**: variante
  async del bulk history import. Devuelve `202 Accepted` con
  `{job_id, status}`. Cliente sondea `GET /jobs/:id` para
  progreso. Pensado para inboxes grandes (>100 contactos).

- **`GET /api/jobs/:id`** + **`GET /api/jobs`**: query de un job
  individual y listado completo respectivamente.

### Documentation

- **`docs/migrations/v0-to-v1.md`**: guía consolidada de
  migración v0.28.x → v1.0:
  - Schema migrations automáticas (sin ALTER manual)
  - Env vars nuevas (defaults conservadores)
  - Cambios visuales en mensajes (group prefix, reactions, quotes)
  - Endpoints nuevos por categoría
  - Orden recomendado de adopción (5 pasos)
  - Rollback path safe
  - Métricas Prometheus + PromQL útil

### Tests

- 6 unit tests en `jobs_test.go`:
  - `CreateAndGet`, `StartCompleteFail`, `FailWithError`
  - `RunAsyncCompletes`, `RunAsyncFailsOnError`
  - `ListReturnsAll`

### Migration notes

- Sin breaking changes. `import-all` (síncrono) sigue funcionando.
  `import-all-async` es opt-in por preferencia del cliente.
- Jobs se mantienen 24h tras completion/fail, luego se purgan.
- Sin persistencia de jobs — si qrsgen reinicia durante un job,
  el cliente recibe 404 al sondear y debe reintentar.

## [0.51.0] - 2026-06-01

Feature: **reply outgoing de media** — cuando el agente quote-replea
desde Chatwoot con un adjunto (imagen/video/audio/documento), el
reply se propaga a WhatsApp como reply nativo con ContextInfo
poblado. Cierra el gap documentado en v0.44.0.

### Added

- **`Sender.SendMediaReply`** interface método. Idéntico a SendMedia
  + `quotedWAID, quotedSenderJID, quotedText`.
- **`wameow.Conn.SendMediaReply`** implementation que usa
  `buildMediaMessageWithReply`.
- **`buildMediaMessageWithReply`** helper en wameow/helpers.go que
  construye el media message + popula `ContextInfo` en el field
  apropiado del tipo concreto (`ImageMessage.ContextInfo`,
  `AudioMessage.ContextInfo`, etc.).
- **`senderAdapter.SendMediaReply`** wire en `cmd/server/main.go`.

### Changed

- **`outgoing.HandleFor` con attachments + in_reply_to**: el PRIMER
  adjunto va como `SendMediaReply` (con quote nativo). Los
  subsiguientes (cuando hay >1 adjunto en un mismo msg) van como
  SendMedia plano — evita duplicar el preview del quote en cada
  adjunto.

### Tests

- `fakeSender.SendMediaReply` + `recordingSender.SendMediaReply`
  stubs en los tests existentes. Comportamiento delegado a
  SendMedia (los tests específicos de reply-to media son una
  iteración futura cuando haya patrones reales).

### Migration notes

- Sin breaking changes. Si tu Chatwoot no usa `in_reply_to` en los
  webhooks (configuración default no lo manda), el flujo es
  idéntico a v0.50.x.

## [0.50.0] - 2026-06-01

Feature: **group admin completeness** — 5 endpoints adicionales que
cierran el conjunto operativo. Junto a los 3 de v0.48.0 da control
total sobre grupos vía HTTP sin tocar el phone.

### Added

- **`POST /api/instances/:n/groups/:jid/topic`** — cambia el topic
  (descripción). Body `{topic: "X"}`. Vacío = quitar descripción.
- **`POST /api/instances/:n/groups/:jid/locked`** — toggle "solo
  admins editan info". Body `{locked: bool}`.
- **`POST /api/instances/:n/groups/:jid/announce`** — toggle modo
  anuncio (solo admins envían msgs). Body `{announce: bool}`.
- **`POST /api/instances/:n/groups`** — crea grupo nuevo. Body
  `{name: "X", participants: [...]}`. max 25 chars name.
  Response 201 con el JID generado.
- **`DELETE /api/instances/:n/groups/:jid`** — bot abandona el grupo.

- **`wameow.Conn` métodos nuevos**:
  - `SetGroupTopic(ctx, jid, topic)`
  - `SetGroupLocked(ctx, jid, bool)`
  - `SetGroupAnnounce(ctx, jid, bool)`
  - `CreateGroup(ctx, name, participants) (jidStr, error)`
  - `LeaveGroup(ctx, jid)`

### Conjunto completo (v0.48.0 + v0.50.0)

| Operation | Endpoint |
|---|---|
| Read info | `GET /groups/:jid` |
| Rename | `POST /groups/:jid/name` |
| Topic | `POST /groups/:jid/topic` |
| Locked | `POST /groups/:jid/locked` |
| Announce | `POST /groups/:jid/announce` |
| Add/remove/promote/demote | `POST /groups/:jid/participants` |
| Create | `POST /groups` |
| Leave | `DELETE /groups/:jid` |

Total 8 endpoints. Falta solo `ephemeral` que es uso menos común.

### Side effects

Con `QRSGEN_GROUP_EVENTS_ENABLED=true`, cada operación de escritura
dispara el `*events.GroupInfo` correspondiente que se renderiza como
activity msg en la conv del grupo en Chatwoot (mismos mensajes que
en v0.47.0).

### Migration notes

- Sin breaking changes.
- `CreateGroup` requiere ≥1 participant en el body — el bot se
  añade implícitamente.
- `name` máx 25 chars — WhatsApp rechaza con 406 si más largo.

## [0.49.0] - 2026-06-01

Feature: **`bridge_chat_anchor` tracker** — sube cobertura del bulk
history import del ~2% al ~100% de chats activos.

### Problema

El history import on-demand (v0.46.0+) necesita un msgID real
existente en el chat como **anchor** para que WhatsApp tire
histórico anterior a él. La fuente de anchor en v0.46.x era el
`msg_history` tracker, que solo registra msgs con group prefix o
mapeo Chatwoot↔WAID. Si un chat tiene actividad pero no caía en
ese criterio, no había anchor → `no_anchor` en el bulk JSON.

En producción dertochip: 171 contactos, solo 2-3 con anchor → bulk
cobertura ~2%.

### Solución

Nuevo tracker indexado **por chat** (no por sender):
`bridge_chat_anchor` con `(instance, chat_jid, waid, ts)`. Registra
TODOS los incoming sin condición. Cubre 1:1 + grupos + cualquier
tráfico.

### Added

- **Tabla `bridge_chat_anchor`** + `EnsureChatAnchorSchema` standalone.
- **`chatAnchorTracker`** in-memory + write-through Postgres async
  (mismo patrón que `msg_history` y `spamguard`):
  - `Record(instance, chatJID, waid, ts)` — update conditional si
    el ts nuevo es más reciente que el existente.
  - `Find(ctx, instance, chatJID) (waid, ts, ok)` — memory first,
    DB fallback con hidratación.
  - `Warmup(ctx, keep)` — carga entries < 30 días al boot.
  - `CleanupOld(ctx, keep)` — borra > 30 días.
- **`Incoming.handleMessage` registra el anchor** de cada incoming
  (no fromMe). Sin condición de prefix → cobertura 100% de chats
  con actividad.
- **`ImportHistoryOnDemand` usa chat_anchor como fuente preferida**,
  cae a `msg_history.FindLastForChat` solo si chat_anchor no tiene
  entry (compat con instalaciones que aún no hayan acumulado anchors).
- **Cleanup cron** en `cmd/server/main.go` cada 12h.

### Tests

- 5 unit tests en `chat_anchor_test.go`:
  - `RecordAndFind`, `FindMissReturnsFalse`
  - `OnlyUpdatesIfNewer` (record más viejo no sobrescribe)
  - `EmptyWAIDOrZeroTSIgnored` (input validation)
  - `PerInstanceIsolation`

### Migration notes

- Schema migra automáticamente al boot vía `EnsureChatAnchorSchema`.
- Sin breaking changes. Bulk import ya funcionaba — esta release
  solo sube la cobertura.
- **Adopt period**: el chat_anchor solo se popula al recibir msgs
  incoming después de adoptar v0.49.0. Bulk import sobre instancias
  recién actualizadas tendrá baja cobertura los primeros días
  hasta que llegue tráfico de los chats activos. Es lo esperado.

## [0.48.1] - 2026-06-01

Hardening + docs sin nuevas features. Primer paso del roadmap
hacia v1.0.0 (conservador).

### Fixed

- **History import: retry-on-5xx**: `postHistoryMsg` ahora reintenta
  hasta 3 veces con backoff 500ms/1s ante errores 5xx o de red.
  Errores 4xx (excepto 422 dup) son permanentes — no retry.
  Antes: 1 hiccup transitorio de Chatwoot durante un bulk de 1000
  msgs abortaba ese msg sin retry → `errors=1` en el JSON.

### Added (docs)

- **`docs/integrations/history-import.md`**: guía completa de uso,
  endpoints, limitaciones, métricas, receta n8n, glosario.
- **`docs/integrations/group-admin.md`**: cuándo usarlo, requisitos
  (bot debe ser admin), receta n8n, testing local.
- **`docs/api/groups.md`**: reference completa de los 3 endpoints
  (path/body/responses/errors).

### Tests

- `TestIsPermanentClientError_4xxNoRetry` — 11 cases cubriendo 400,
  401, 403, 404, 409, 429 (permanentes) y 500, 502, 503, 504,
  timeout, nil (retry-able).

## [0.48.0] - 2026-06-01

Feature: **group admin endpoints** — qrsgen ahora puede gestionar
grupos de WhatsApp (rename, add/remove/promote/demote miembros)
vía HTTP. Cierra el ciclo con v0.47.0: la operación dispara un
`*events.GroupInfo` que se renderiza como activity msg en Chatwoot.
Permite testear v0.47.0 self-contained y abre la puerta a paneles
de admin sin tocar el phone.

Scope deliberadamente acotado (3 endpoints) para estabilidad
pre-release v1.0.0. Topic, locked, announce, ephemeral, create,
leave quedan para iteraciones posteriores cuando haya demanda real.

### Added

- **`wameow.Conn.GroupInfo(ctx, jid)`**: round-trip al server WA.
  Devuelve `GroupInfo` JSON-friendly con subject, topic, settings y
  participants (cada uno con `phone_number` resuelto si es LID).
- **`wameow.Conn.SetGroupName(ctx, jid, name)`**: rename del grupo.
  Requiere que el bot sea admin.
- **`wameow.Conn.UpdateGroupParticipants(ctx, jid, action, jids)`**:
  add/remove/promote/demote. Validación del action en el wrapper.

- **Endpoints admin** en `cmd/server/main.go`:
  - `GET    /api/instances/:n/groups/:jid` → info JSON
  - `POST   /api/instances/:n/groups/:jid/name` → `{name}`
  - `POST   /api/instances/:n/groups/:jid/participants` →
    `{action, jids[]}`

Auth heredada de `QRSGEN_API_TOKEN`. Errors mapeados:
- 400 jid inválido / body malformado / action no soportado
- 404 instance not found
- 500 error del peer WA (ej. bot no es admin)

### Tests

- Build + suite full pasan. Test E2E manual:
  ```bash
  curl -X POST -H "Authorization: Bearer $TOKEN" \
    -d '{"name":"Grupo Renombrado"}' \
    "$BASE/api/instances/ATC/groups/120363111@g.us/name"
  ```
  → genera evento, qrsgen postea
  `📝 **~Bot** cambió el nombre del grupo a _Grupo Renombrado_`
  como activity msg en la conv del grupo en Chatwoot.

### Limitaciones

- **Bot debe ser admin** del grupo para cualquier operación de
  escritura (rename, participants). WhatsApp rechaza si no — el
  endpoint devuelve 500 con el mensaje del peer.
- **Topic, locked, announce, ephemeral, create, leave** NO están
  expuestos en esta release. Si los necesitas, abre issue.

### Migration notes

- Sin breaking changes. Endpoints nuevos protegidos por el mismo
  middleware de auth que el resto de `/api/instances/*`.

## [0.47.0] - 2026-06-01

Feature: **group events como activity msgs en Chatwoot**. Cuando
WhatsApp emite `*events.GroupInfo` (cambio nombre/topic/miembros/
lock/announce/ephemeral), `*events.JoinedGroup` (bot añadido a un
grupo nuevo) o `*events.IdentityChange` (código de seguridad
cambia), qrsgen postea un activity msg en la conv de Chatwoot
correspondiente. El agente ve la mismo contexto que vería en su
phone WA.

Incluye también el fix de polish del bulk history import del v0.46.x
(reporting separado de "no anchor" vs errors reales).

### Added

- **`Incoming.HandleGroupInfo / HandleJoinedGroup / HandleIdentityChange`**
  + `SetGroupEventsEnabled` setter. Procesan los eventos y postean
  activity msgs vía `findContactByIdentifier` + `FindOpenConversation` +
  `PostMessage`. Si la conv aún no existe (grupo sin actividad previa),
  no-op silencioso.
- **`wameow.GroupInfoHandler`** + `JoinedGroupHandler` +
  `IdentityChangeHandler` types. Subscripciones en `Conn` + setters +
  propagación en `Manager`.
- **Formato de activity msgs**:
  - `📝 **Pepito** cambió el nombre del grupo a _X_`
  - `📝 **Pepito** cambió la descripción del grupo: _X_` / `quitó la descripción`
  - `🔒 **Pepito** restringió la edición del grupo a admins` / `🔓 ... permitió a todos`
  - `📢 **Pepito** activó modo anuncio` / `desactivó modo anuncio`
  - `⏱️ **Pepito** activó mensajes temporales (Ns)` / `desactivó`
  - `➕ **Pepito** añadió a Ana, ~Bea`
  - `➖ Ana, ~Bea salieron/fueron expulsados del grupo`
  - `⭐ **Pepito** promovió a admin: ...` / `quitó admin a: ...`
  - `**Te añadieron a este grupo**\n_Grupo: <subject>_` (joined)
  - `🔐 **El código de seguridad de Pepito cambió.** Toca para más información.`

- **Métricas**: `qrsgen_realtime_events_total{feature="group_event"}`
  con results `ok` / `ds_error`.

### Config

- **`QRSGEN_GROUP_EVENTS_ENABLED`** (default `false`): opt-in.
  Activar implica POSTs adicionales a Chatwoot por cada cambio de
  metadata del grupo / join / identity. Sin actividad en grupos
  es prácticamente cero overhead.

### Changed (v0.46.3 polish del bulk import)

- **`BulkImportResult.NoAnchor` field nuevo** separado de `Errors`.
  Chats sin msg tracked en `msg_history` (no aplica al feature
  porque WA ON_DEMAND necesita anchor real) ahora cuentan como
  `no_anchor`, no como `errors`. El JSON del endpoint
  `/history/import-all` distingue ambos casos:
  ```json
  {
    "scanned": 171,
    "imported": 3,
    "skipped": 7,
    "no_anchor": 161,  // <-- nuevo, antes "errors"
    "errors": 0,        // ahora solo timeouts/fallos reales
    "total_posted": 45
  }
  ```

### Tests

- 8 unit tests en `group_events_test.go`:
  - `BuildGroupInfoLines_*` (5 cases: name change, topic set/unset,
    join/leave/promote/demote, locked/announce/ephemeral, unknown actor)
  - `IdentityFromJID_*` (3 cases: saved sin tilde, unsaved con tilde,
    fallback phone)

### Migration notes

- Sin breaking changes. Feature off por default. Activar con
  `QRSGEN_GROUP_EVENTS_ENABLED=true` para empezar a propagar los
  eventos.
- Si tu downstream tiene rate limits agresivos, ten en cuenta que
  grupos muy activos pueden generar varios activity msgs por hora
  (cambios de nombre, miembros, etc.).
- v0.46.x `BulkImportResult.errors` field semantics cambia:
  ahora cuenta SOLO timeouts/fallos reales, no chats sin anchor.
  Parsers que sumen `errors` para detectar "hubo problemas"
  necesitan también considerar el caso "no_anchor=N" (que es
  esperado, no error).

## [0.46.2] - 2026-06-01

Bug fix de v0.46.0/v0.46.1: el on-demand history sync request fallaba
con timeout porque enviaba un `lastKnownMessageInfo` dummy (msgID="0",
ts=now). El phone primary necesita un msgID **real existente** en el
chat para tirar histórico anterior a él. Sin anchor real, ignoraba
la request → timeout 30s.

### Fixed

- **`Incoming.ImportHistoryOnDemand` resuelve anchor desde
  `msg_history` tracker**: nuevo método
  `msgHistoryTracker.FindLastForChat(ctx, instance, chatJID)` busca el
  msg tracked más reciente del chat (in-memory + fallback DB).
- **Si no hay anchor**, devuelve error claro en lugar de timeout
  silencioso:
  ```
  no message anchor for chat <jid> — qrsgen needs at least one tracked
  incoming msg from this chat to request more history; wait for an
  incoming or send a test msg first
  ```

### Limitación

- **El feature requiere que qrsgen haya recibido AL MENOS UN msg
  incoming del chat** para tener un anchor (vía msg_history tracker
  v0.40+). Para chats sin actividad reciente desde el deploy de
  v0.41.0+, no hay anchor → import on-demand no funciona en esos chats
  hasta que llegue un msg incoming.
- Esto es una limitación del protocolo WhatsApp ON_DEMAND, no de
  qrsgen — el phone primary necesita un anchor temporal real.
- **Workaround**: para chats sin actividad reciente, envíate un msg
  desde el otro extremo (puede ser un emoji corto) y luego dispara
  el import. El tracker captura el msg, el anchor queda registrado, y
  la próxima import on-demand funciona.

### Requirement

- `QRSGEN_RETROACTIVE_NAME_UPDATE=true` (default) — sin el tracker
  no hay anchor lookup posible. El endpoint devuelve error explícito.

## [0.46.1] - 2026-06-01

UX add-on de v0.46.0: bulk history import sin necesidad de desconectar
la instancia.

### Added

- **`Incoming.BulkImportHistory(ctx, instance, inboxID, count, timeout, r)`**:
  itera todos los contactos del inbox vía `ListContactsByInbox` y
  dispara `ImportHistoryOnDemand` secuencialmente por cada uno.
  Funciona sobre instancia ya conectada — NO requiere desconectar
  ni re-parear. Devuelve `BulkImportResult` con stats agregadas
  (`pages, scanned, imported, skipped, errors, total_posted,
  total_skipped, total_errors`).
- **Endpoint admin `POST /api/instances/:name/history/import-all`**:
  - Query: `count_per_chat=N` (default 50), `timeout_per_chat=N`
    (default 30s).
  - Bloquea hasta terminar — para inboxes grandes puede tardar
    minutos.

### Notes

- Secuencial por diseño: procesa un chat tras otro para no estresar
  al phone primary (que sirve las requests on-demand) ni al
  downstream (rate-limit existente del v0.46.0).
- Ya estaba implícito en v0.46.0 que el endpoint single-chat
  funciona sin desconectar — el bulk simplemente itera los chats
  de la inbox.
- Para chats que aún no tienen contacto en Chatwoot, este endpoint
  no los descubre — usar el endpoint single-chat o esperar al
  primer msg incoming.

### Migration notes

- Sin breaking changes. Endpoint nuevo bajo
  `/api/instances/:name/history/import-all` — protegido por
  middleware de auth si `QRSGEN_API_TOKEN` está set.

## [0.46.0] - 2026-06-01

Feature: **history import** — backfill de mensajes históricos de
WhatsApp a Chatwoot. Configurable 1-30 días, vía evento pasivo
(pareo) o endpoint admin on-demand.

### Added

- **`*events.HistorySync` subscription** en `wameow.Conn`. Nuevo
  type `wameow.HistorySyncHandler` y `Conn.SetHistorySyncHandler` +
  `Manager.SetHistorySyncHandler` (propagación a instancias).
- **`Incoming.HandleHistorySync(ctx, instance, data, r)`**: procesa
  el blob recibido. Itera conversations + messages, filtra por
  edad (`days` config), ordena cronológicamente, postea cada msg
  al downstream con `created_at` backdated y `source_id=WAID:<id>`
  para idempotencia.
- **`Incoming.ImportHistoryOnDemand(ctx, instance, chat, count, timeout, r)`**:
  orquesta on-demand history sync para un chat. Llama
  `WAResolver.RequestHistorySync` (que internamente usa
  `Client.BuildHistorySyncRequest` + `SendPeerMessage`), espera el
  `*events.HistorySync` con type `ON_DEMAND` vía latch sincronizado,
  y procesa el payload. Devuelve `HistoryImportResult` con stats.
- **`WAResolver.RequestHistorySync`** + impl en `Conn`. Toma el
  last-known msg info + count y envía petición a la primary device
  del usuario.
- **Endpoint admin `POST /api/instances/:name/history/import`**:
  - Query: `chat=<jid>` (requerido), `count=N` (default 50, max
    200), `timeout_sec=N` (default 30).
  - Devuelve `HistoryImportResult` JSON
    (`{instance, conversations, messages_seen, messages_kept,
    posted, skipped, errors, oldest_ts, newest_ts}`).
- **`PostMessageReq.CreatedAt`** (opcional): cuando set, qrsgen
  postea `created_at` + `external_created_at` como Unix epoch para
  compat entre versiones Chatwoot. Para flujo normal (zero value),
  comportamiento sin cambios.
- **Métricas Prometheus** vía `qrsgen_realtime_events_total`:
  - `feature="history_import"` con results: `ok`, `ds_error`,
    `skip_disabled`, `duplicate` (source_id ya importado).

### Config

- **`QRSGEN_HISTORY_IMPORT_ENABLED`** (default `false`): opt-in.
- **`QRSGEN_HISTORY_IMPORT_DAYS`** (default `7`): rango 1-30.
- **`QRSGEN_HISTORY_IMPORT_RATE_PER_SEC`** (default `5`):
  throttle de POST/s al downstream para no estresar Chatwoot.

### Notes

- **Media files NO se importan** en esta fase. Para
  imagen/video/audio/document/sticker sin caption, qrsgen postea
  un placeholder (`🖼️ [imagen — no importada]`, etc.). Con
  caption, postea el caption con el emoji prefix. La descarga +
  re-upload de media files se considera para una fase futura por
  el coste de bandwidth + complejidad.
- **WhatsApp limita la profundidad del histórico** según ajustes
  del phone (típicamente 30/90/180 días). Si pides 30 pero el
  phone solo guarda menos, qrsgen recibe lo que haya.
- **Idempotencia**: re-correr el import sobre los mismos msgs no
  duplica — Chatwoot rechaza POSTs con `source_id` ya existente
  (422), qrsgen lo cuenta como `duplicate` y sigue.
- **Pareo**: si la feature está activa al parear una instancia
  nueva, el HistorySync que WhatsApp empuja automáticamente se
  procesa y los msgs aparecen en Chatwoot. Para instancias ya
  pareadas, usar el endpoint on-demand.

### Tests

- 9 unit tests en `history_import_test.go`:
  - `ExtractHistoryText_*` (7 cases: text, ext text, image with/
    without caption, audio PTT, document title, nil).
  - `EnableHistoryImport_DaysClamped` (clamp a [1, 30]).
  - `EnableHistoryImport_RateDefaultWhenInvalid` (default 5).

### Migration notes

- Sin breaking changes. Feature opt-in. Sin `ENABLED=true` el
  comportamiento es idéntico a v0.45.x.
- `PostMessageReq` gana un campo nuevo `CreatedAt` pero los
  callers existentes pasan zero value → flujo sin cambios.
- Endpoint nuevo bajo `/api/instances/:name/history/import` —
  protegido por el middleware de auth si `QRSGEN_API_TOKEN` está set.

## [0.45.1] - 2026-06-01

UX tweak post-v0.45.0: reactions tienen ahora un separador propio
configurable, distinto del usado en group msgs. Default cambia a
`\n` (single newline) — más compacto que el `\n\n` (paragraph)
heredado de group prefix.

Antes (v0.45.0 con default heredado):
```
`+34611887663 · ~Agustina Sant Martí Real Estate`

reaccionó con 👍
```

Ahora (v0.45.1 default):
```
`+34611887663 · ~Agustina Sant Martí Real Estate`
reaccionó con 👍
```

La reacción es visualmente más atómica que un msg con cuerpo
arbitrario — un solo line break basta para identificar el verb sin
inflar la conv.

### Added

- **`QRSGEN_REACTION_HEADER_SEP`** env var (default `nl` = `\n`).
  Mismos alias que `QRSGEN_GROUP_HEADER_SEP` (`paragraph`/`p`, `br`,
  `br_self`, `lsep`, `nl`/`soft`, `slash`, `spaced_br`).
- **`Incoming.SetReactionSep(sep)`** + `Incoming.reactionSep` field.
  `main.go` la cablea desde `cfg.ReactionHeaderSep`.

### Changed

- **`handleReaction` usa `i.reactionSep`** en lugar de `i.headerSep`.

### Migration notes

- Default cambia visualmente para reactions sin necesidad de tocar
  env vars. Si quieres recuperar el `\n\n` de v0.45.0, setear
  `QRSGEN_REACTION_HEADER_SEP=paragraph`.

## [0.45.0] - 2026-05-29

Feature: **template configurable del header de sender + reactions
estandarizadas**. Tres cambios relacionados:

1. **Nuevo env var `QRSGEN_HEADER_TEMPLATE`** con tokens `$phone` y
   `$name`. Default `` `$phone · $name` `` (igual que antes). Permite
   al operador elegir el wrapper (code block, bold, plano, etc.) sin
   rebuild. El `~` para no-saved sigue siendo automático — solo el
   wrapper visual es configurable.
2. **Reactions reusan el mismo header template** y se separan en
   header + body en líneas distintas (mismo layout que group msgs),
   en lugar de pegarlo todo en un solo code block.
3. **Refactor**: `renderGroupSenderPrefix` ahora delega en
   `renderSenderHeader(si, template)` — helper reutilizable.

### Format change

**Reactions antes:**
```
`+34611887663 · ~Agustina Sant Martí reaccionó con 👍`
```

**Reactions ahora (default template):**
```
`+34611887663 · ~Agustina Sant Martí Real Estate`
reaccionó con 👍
```

### Examples (env)

- `QRSGEN_HEADER_TEMPLATE='` `$phone · $name` `'` → default (en YAML
  el backtick va literal con comillas simples).
- `QRSGEN_HEADER_TEMPLATE='` `$phone` · **$name** `'` → phone en
  code, nombre en bold.
- `QRSGEN_HEADER_TEMPLATE='$phone | $name'` → plano sin markdown.
- `QRSGEN_HEADER_TEMPLATE='[$phone] $name'` → con corchetes.

El `$name` ya viene con `~` si el contacto NO está saved, así que el
template solo decide el envoltorio.

### Added

- **`QRSGEN_HEADER_TEMPLATE` env var** + `Config.HeaderTemplate`.
- **`Incoming.SetHeaderTemplate(template)`** (vacío = default).
- **`Incoming.headerTemplate` field** — usado por handleMessage,
  handleReaction y applyRetroactiveUpdates.
- **`renderSenderHeader(si, template)`** helper exportable
  (package-internal).
- **`GroupHeaderTemplateDefault`** constante con el formato actual
  (`` `$phone · $name` ``).

### Changed

- **`renderGroupSenderPrefix(si)`** ahora es un wrapper que delega
  en `renderSenderHeader(si, GroupHeaderTemplateDefault)`. Sin
  cambio de comportamiento para callers existentes.
- **`handleReaction` reusa el header template + split layout**:
  - Antes: `` `+phone · ~name reaccionó con emoji` `` (una sola línea
    en code block).
  - Ahora: `<header>\n\n<verb>` donde header usa template y separador
    es el configurable `QRSGEN_GROUP_HEADER_SEP`.

### Tests

- 8 unit tests nuevos en `header_template_test.go`:
  - DefaultTemplate, DefaultUnsavedTilde
  - CustomBoldNameTemplate, CustomPlainTemplate
  - OnlyNameFallback, OnlyPhoneFallback
  - NoIdentificationReturnsFalse
  - TildeAppliedToNameToken

### Migration notes

- **Sin breaking changes** en API ni schema. Default template
  preserva el formato v0.44.x del group prefix.
- **Reactions cambian visualmente**: ahora el verb sale en línea
  aparte. Parsers regex que matcheen el formato single-line viejo
  `` ^`\+\d+ · ~?.* reaccionó con .*`$ `` necesitan migrar al
  patrón header + separador + verb.
- Si tu downstream NO tolera el separador entre header y body
  (improbable), setear `QRSGEN_HEADER_TEMPLATE='\`$phone · $name reaccionó con \`'`
  recupera el formato single-line (solo grupos lo necesitarían, ya
  que reactions ya no se pueden meter inline porque el verb se ha
  externalizado del template).

## [0.44.4] - 2026-05-29

Redesign del blockquote del quote/reply context (v0.42.0..v0.44.3)
para alinearlo con el estilo del group prefix (v0.39.5+):

Antes (v0.44.3):
```
`+34604021705 · Ricajos`
> _↩️ respondiendo a ~Pepito:_ texto citado
reply del usuario
```

Ahora (v0.44.4):
```
`+34604021705 · Ricajos`
> `↪ +34600000099 · ~Pepito`
> texto citado
reply del usuario
```

Cambios concretos:
- Header del citado pasa de italic con emoji ↩️ a **code block con
  flecha unicode `↪` (U+21AA)** + phone + middle dot + name. Mismo
  patrón visual que el group prefix → consistencia.
- La flecha unicode sin variation selector sale como glyph plano
  (no emoji-style) — funciona uniforme en cualquier fuente.
- Header en su propia línea del blockquote (en lugar de pegado al
  texto). Chatwoot da espacio cómodo entre header y citado.
- Texto "respondiendo a X" eliminado — la flecha + identidad ya
  comunica que es un reply.
- En 1:1 (sin Participant) el header se omite: contexto del author
  es trivial (el otro extremo del chat), el blockquote por sí solo
  ya indica que es un mensaje citado.

### Added

- **`buildQuoteHeader(ci, r)`**: helper que arma el header
  (code block con flecha + phone + name) reutilizando
  `resolveJIDNameSaved`. Devuelve "" si no hay Participant resoluble.

### Changed

- **`formatQuotedBlock`**: layout reformateado. Header en su línea,
  cada línea del citado prefijada con `> `. Texto "respondiendo a"
  removido.

### Tests

- 10 cases en `TestFormatQuotedBlock` (reescritos para el nuevo
  layout):
  - `GroupReplyUnsavedAuthor` / `GroupReplySavedAuthor`
  - `NoQuoteReturnsEmpty`
  - `NoResolverFallsBackToPhoneOnly`
  - `OneOnOneNoHeader` (1:1 omite header)
  - `TruncatesLongQuote`
  - `MultilineQuoted`
  - `LIDAuthorWithSavedPN` (hereda fix v0.39.9)
  - `ImageQuoteUsesPlaceholder` / `AudioPTTPlaceholder`

### Migration notes

- Parsers regex sobre el blockquote del quote necesitan actualizarse
  del patrón v0.44.3 (`> _↩️ respondiendo a (.*):_ (.*)`)
  al patrón v0.44.4 (header en línea separada):
  ```
  > `↪ (\+\d+)( · )?(~?)(.*)`
  > (.*)
  ```

## [0.44.3] - 2026-05-29

UX consistency: el author del blockquote en quote/reply context
ahora respeta la misma convención que el group prefix (v0.39.5):
`~Name` si el contacto NO está guardado en la agenda del bot owner,
`Name` a secas si sí lo está. Antes el quote usaba `ContactName`
sin chequear `IsContactSaved`, así que un PushName auto-asignado
salía sin tilde aparentando ser un contacto saved.

### Changed

- **`formatQuotedBlock` aplica `~` cuando author no saved**:
  - Antes: `> _↩️ respondiendo a Pepito:_ texto`
  - Ahora: `> _↩️ respondiendo a ~Pepito:_ texto` (si no saved)
  - Saved: `> _↩️ respondiendo a Pepito Saved:_ texto`

### Refactor

- **`resolveJIDNameSaved(jid, r)` extraído**: helper que toma un
  JID directo (no un `*events.Message`) y devuelve `(name, saved)`.
  Aplica el fix v0.39.9 (LID→PN saved usa nombre canónico).
- **`resolveSenderInfo` reutiliza `resolveJIDNameSaved`**: misma
  lógica que antes pero centralizada — DRY. Sin cambio de
  comportamiento.

### Tests

- 10/10 cases en `TestFormatQuotedBlock` (8 actualizados + 2
  nuevos):
  - `SavedAuthorWithoutTilde` (saved → sin `~`).
  - `LIDAuthorWithSavedPN` (LID con PushName pero PN saved → usa
    canonical sin `~`, hereda fix v0.39.9).
  - Los 8 existentes actualizados con `~` donde corresponde.

### Migration notes

- Sin breaking changes. Parsers regex sobre el blockquote del
  quote necesitan tolerar el `~` opcional:
  `> _↩️ respondiendo a (~)?(.*):_ (.*)`.

## [0.44.2] - 2026-05-29

UX tweak sobre el quote/reply context (v0.42.0). Chatwoot renderiza
con gap visual entre líneas `>` dentro de un blockquote y el `\n\n`
entre el blockquote y el reply body daba demasiado aire. Ahora:

1. Header del quote y primera línea del texto citado salen en la
   **misma línea** del blockquote → quedan pegados visualmente.
2. Separador entre blockquote y reply baja de `\n\n` a `\n` → reply
   directamente debajo del citado sin paragraph gap.

### Changed

- **`formatQuotedBlock` line layout**:
  - Antes: `> _↩️ respondiendo a X:_\n> texto citado`
  - Ahora: `> _↩️ respondiendo a X:_ texto citado`
- **Separador quote→body en `handleMessage`**: `quoted + "\n\n" + content`
  → `quoted + "\n" + content`.
- Multiline citado: la primera línea va pegada al header; las
  siguientes mantienen `\n> ` prefix.

### Tests

- Actualizados los 4 cases afectados (`PlainTextReply`,
  `NoResolverFallsBackToPhone`, `NoParticipantNoName`,
  `MultilineQuotedText`). Los 8 cases de `TestFormatQuotedBlock`
  pasan con el nuevo layout.

### Migration notes

- Sin breaking changes. Parsers regex sobre el formato del quote
  necesitan actualizarse de `^> _↩️ respondiendo a (.*):_\n> (.*)$`
  a `^> _↩️ respondiendo a (.*):_ (.*)$`.

## [0.44.1] - 2026-05-29

Bug fix: el bot reply no reseteaba el `groupTracker` de burst, así que
tras una intervención del agente en un grupo, el siguiente msg del
usuario aparecía sin header (heredaba la supresión del burst anterior).
Bug presente desde v0.30.0.

### Fixed

- **`groupTracker` ahora rastrea los sends del bot a grupos**:
  whatsmeow NO emite `*events.Message` para envíos del mismo
  cliente, así que el flujo de `Incoming.Handle` nunca veía los
  msgs del bot. El groupTracker se actualiza solo desde
  `Incoming.Handle`, así que `_bot` no se registraba como último
  sender → siguiente msg del usuario seguía dentro del burst
  visual del usuario.

  Fix:
  - `Incoming.MarkBotSentInGroup(instance, chatJID)` expone el
    reset al package.
  - `Outgoing.EnableReplyToOutgoing(in)` ahora también guarda la
    referencia a `Incoming` (no solo el `msgHistory`).
  - Tras un `SendText`/`SendMedia` exitoso a un grupo (`@g.us`),
    `Outgoing.markBotInGroup(instance, remoteJid)` llama
    `MarkBotSentInGroup`. El groupTracker registra `_bot` como
    último sender, rompiendo el burst del usuario.

### Notes (no fix)

- **Firma en outgoing**: confirmado que `qrsgen.Outgoing` envía
  `p.Content` literal sin añadir nada. Si los msgs del agente
  aparecen con firma, viene de la feature "Personal Message
  Signature" de Chatwoot (account/agent settings). Para
  desactivar: ajustes del agente → quitar firma. No es un
  comportamiento de qrsgen.

### Tests

- 3 nuevos en `bot_burst_reset_test.go`:
  - `ResetsBurst` (caso happy: bot reply rompe el burst).
  - `NoOpWithoutTracker` (TTL=0 → método silencioso, no panic).
  - `DifferentChatNoCrossEffect` (bot en chat B no afecta chat A).

### Migration notes

- Sin breaking changes. Si no usas `EnableReplyToOutgoing` (la
  feature retroactive name update desactivada), el bug persiste
  porque el reset depende de esa wiring. El default true del
  `QRSGEN_RETROACTIVE_NAME_UPDATE` activa todo automáticamente.

## [0.44.0] - 2026-05-29

Feature: **reply-to outgoing**. Cuando el agente hace quote-reply en
el composer de Chatwoot, qrsgen propaga el mensaje como reply nativo
de WhatsApp en lugar de texto suelto. El cliente WA receptor ve el
quoted preview tappable que enlaza al mensaje original.

### Added

- **Sender.SendTextReply(ctx, instance, remoteJid, content, quotedWAID,
  quotedSenderJID, quotedText)**: nuevo método en la interface
  `bridge.Sender`. Implementado en `wameow.Conn.SendTextReply` (popula
  `ContextInfo` con `StanzaId/Participant/QuotedMessage`) y en
  `senderAdapter` del `main`.
- **`helpers.replyTextMessage`**: builder de `*waE2E.Message` con
  ExtendedTextMessage + ContextInfo poblado. Conv1:1 y grupos.
- **`msgHistoryTracker.FindByChatwootMsgID(ctx, instance, msgID)`**:
  lookup por Chatwoot msgID (memory first → DB fallback). Devuelve
  `(trackedMsg, senderJID, ok)`.
- **`Incoming.replyToTracker()` + `Outgoing.EnableReplyToOutgoing(in)`**:
  conecta el `msg_history` compartido (mismo patrón que `EnableMarkAsRead`).
  Sin EnableReplyToOutgoing, los webhooks con `in_reply_to` se procesan
  como texto suelto (backward-compat).
- **`Outgoing.resolveReplyContext`**: parsea
  `content_attributes.in_reply_to` del webhook, resuelve via tracker,
  y devuelve el contexto para SendTextReply.
- **`wameow.WAResolver.GetSavedContacts`**: extendido en v0.43.0,
  necesario para el reconcile + el tracker compartido.

### Changed

- **`trackedMsg` ahora incluye `waid` + `hasPrefix`**. v0.40.0-v0.43.0
  solo trackeaba msgs con prefix de grupo; v0.44.0 trackea TODOS los
  incoming (también 1:1) para que el lookup por msgID funcione.
  `hasPrefix=false` excluye de retroactive PATCH loop (1:1 no llevan
  header reescribible).
- **`handleMessage` registra todos los incoming no-fromMe**: no solo
  los con prefix. `hasPrefix` discrimina el comportamiento downstream.
- **Schema migration**: `ALTER TABLE bridge_msg_history ADD COLUMN
  waid TEXT NOT NULL DEFAULT ''` y `ADD COLUMN has_prefix BOOLEAN
  NOT NULL DEFAULT TRUE`. Rows pre-v0.44.0 quedan con `has_prefix=TRUE`
  (eran todas prefix rows) y `waid=''` (no se puede recuperar
  retroactivamente — degradan a SendText pelado en reply-to outgoing).
- **`Webhook.ContentAttributes`**: nuevo campo `*struct{InReplyTo int}`
  parseado del payload Chatwoot.

### Tests

- 5 unit tests nuevos en `internal/bridge/reply_to_outgoing_test.go`:
  - `SendsAsReplyWhenTracked`
  - `FallsBackToPlainTextWhenNotFound`
  - `NoInReplyToUsesSendText`
  - `DisabledFallsBackToPlainText`
  - `EmptyWAIDFallsBack` (rows pre-v0.44.0)
- Integration tests v0.41.0 (5/5) re-verificados contra Postgres real
  con el schema migrado.

### Migration notes

- Schema cambia automáticamente al boot vía `EnsureMsgHistorySchema`
  (ADD COLUMN IF NOT EXISTS). Cero downtime — el ALTER es metadata-only
  en Postgres con DEFAULT no-NULL en columnas TEXT/BOOLEAN.
- Mensajes incoming pre-v0.44.0 ya tracked: `has_prefix=TRUE` mantiene
  el retroactive update funcionando; `waid=''` deshabilita el reply-to
  para esos mensajes (caen a SendText, mismo comportamiento que <v0.44.0).
- Sin breaking changes en la API pública del paquete bridge ni de los
  endpoints HTTP. Backward-compat: si no se llama
  `outgoing.EnableReplyToOutgoing`, todo se comporta como v0.43.x.

## [0.43.0] - 2026-05-29

Feature: **extender retroactive name update a 1:1 + bulk reconcile**.
v0.40.x ya reescribía los headers de mensajes de grupo cuando el dueño
añadía un contacto a la agenda. v0.43.0:

1. **Renombra también el contact en Chatwoot** (no solo el content
   de los msgs). Aplica al caso 1:1 (donde no hay prefix de grupo
   pero el contact name sí es visible al agente).
2. **Endpoint admin bulk reconcile**: itera el contact store local
   de whatsmeow y dispara HandleContactUpdate por cada saved.
   Útil para bootstrap inicial tras adoptar el feature por primera
   vez o tras un restart de v0.40.x sin persistence.

### Added

- **`downstream.Client.UpdateContactName(ctx, contactID, name)`**:
  PUT `/contacts/{id}` con `{"name": "..."}`.
- **`HandleContactUpdate` ahora también renombra**: tras buscar el
  contact en Chatwoot (vía `findContactByIdentifier`, mismo helper
  del flujo de creación), si el `name` actual difiere del nuevo,
  PUT con el canónico. Best-effort: si falla, log + sigue con
  el PATCH loop de mensajes históricos.
- **`Incoming.ReconcileSavedContacts(ctx, instance, r)`** +
  `ReconcileResult{Instance, Scanned, Triggered}` JSON.
- **Endpoint admin `POST /api/instances/:name/retroactive/reconcile`**:
  bulk reconcile usando el connection del manager como
  `WAResolver`. Devuelve `{instance, scanned, triggered}`.
- **`wameow.WAResolver.GetSavedContacts(ctx)`** + impl en `Conn`:
  itera el store local y devuelve `map[PN JID]→canonical name`
  para entries con FullName o FirstName.

### Renamed

- **`applyRetroactivePatches` → `applyRetroactiveUpdates`**: el
  helper ahora hace dos cosas (rename + patches), nombre nuevo
  refleja el scope.

### Tests

- 6 nuevos tests en `internal/bridge/reconcile_test.go`:
  - `HandleContactUpdate_RenamesChatwootContact`
  - `HandleContactUpdate_DoesNotRenameIfNameAlreadyMatches`
  - `HandleContactUpdate_RenamesAndPatchesBoth`
  - `HandleContactUpdate_RenamesEvenWith1on1NoTrackedMsgs`
  - `ReconcileSavedContacts_DispatchesPerContact`
  - `ReconcileSavedContacts_NoOpIfDisabled`
- Stub HTTP del downstream que captura GET search, PUT rename
  y PATCH content por separado.

### Migration notes

- Sin breaking changes (de API ni de schema). Token Chatwoot
  necesita permisos PUT sobre contacts (ya los tiene si puede
  CREATE contacts, que es el caso del api_channel).
- Endpoint `reconcile` requiere auth si `QRSGEN_API_TOKEN` está
  configurado (igual que el resto de admin endpoints).
- Si `RetroactiveNameUpdate=false`, el endpoint devuelve 500 con
  `"retroactive name update disabled"`.

## [0.42.0] - 2026-05-29

Feature: **quote/reply context en mensajes incoming**. Cuando un
usuario responde a un mensaje en WhatsApp (long-tap → reply), el
mensaje citado se renderiza como blockquote markdown encima del
body en Chatwoot. El agente ve a qué se está respondiendo sin tener
que buscar el msg original arriba.

### Added

- **`extractContextInfo` + `extractQuotedText` + `formatQuotedBlock`**
  helpers en `internal/bridge/incoming.go`. Soportan texto plano y
  todos los tipos media (image/video/audio/document/sticker/location)
  con placeholders emoji para los no-textuales.
- **Renderizado en `handleMessage`**: si el incoming tiene
  `ContextInfo.QuotedMessage`, prefijar el body con
  `> _↩️ respondiendo a Name:_\n> texto citado\n\n` antes del flow
  de group-prefix.

### Format

Ejemplo de un reply en un grupo:

```
`+34604021705 · Ricard Penin`

> _↩️ respondiendo a Pepito:_
> hola, qué tal?

todo bien gracias
```

- Author resuelto vía `WAResolver` (preferimos saved name canónico,
  hereda fix v0.39.9). Fallback: phone E.164. Sin participant
  (1:1 chat) queda como `> _↩️ respondiendo:_` a secas.
- Texto citado truncado a 200 runas con `…` para no inflar la conv.
- Multilinea: cada línea del citado lleva su `> ` prefix.

### Tests

- 8 unit tests en `internal/bridge/quote_test.go`:
  - PlainTextReply, NoQuoteReturnsEmpty, NoResolverFallsBackToPhone,
    NoParticipantNoName, TruncatesLongQuote, MultilineQuotedText,
    ImageQuoteUsesPlaceholder, AudioPTTPlaceholder.

### Migration notes

- Sin breaking changes. Mensajes no-reply se procesan exactamente
  igual que antes (formatQuotedBlock devuelve "" silencioso).
- El blockquote se prepended ANTES del group prefix → el orden
  visual es: header → quoted block → body.

## [0.41.0] - 2026-05-29

Feature: **persistencia del retroactive name update**. La principal
limitación de v0.40.0 (state in-memory perdido en restart) queda
resuelta: el histórico tracked vive en la tabla `bridge_msg_history`
de Postgres y sobrevive a deploys.

### Added

- **Tabla `bridge_msg_history`** + `bridge.EnsureMsgHistorySchema(ctx, pool)`.
  Columnas: `instance, sender_jid, conv_id, msg_id, phone, name_used,
  was_saved, body, posted_at`. PK `(instance, msg_id)`. Índices en
  `(instance, sender_jid, posted_at DESC)` y `posted_at`.

- **`msgHistoryTracker.SetPool(pool, logger)`** + write-through
  asíncrono en Record y UpdateAfterPatch. Las escrituras a DB
  corren en goroutine (con timeout 5s) para no bloquear el flujo
  del mensaje.

- **`msgHistoryTracker.Warmup(ctx, keep)`**: al boot, carga las
  entries con `posted_at > NOW() - keep` desde DB hacia el cache
  in-memory. Respeta el cap per sender al cargar (entries más
  recientes ganan).

- **`msgHistoryTracker.CleanupOld(ctx, keep)`** + cron en
  `cmd/server/main.go` (cada 6h): borra entries con
  `posted_at < NOW() - keep`. Limita el crecimiento de la tabla.

- **Wrappers en `Incoming`**: `SetRetroactivePool`,
  `WarmupRetroactive`, `CleanupRetroactiveOld`. main.go los llama
  tras `EnableRetroactiveNameUpdate`.

### Config

- **`QRSGEN_RETROACTIVE_PERSIST`** (default `true`): activa la
  persistencia. `false` → modo in-memory v0.40.0.
- **`QRSGEN_RETROACTIVE_TTL`** (default `720h` = 30 días):
  ventana de retención. Tras este TTL las entries se borran en
  el cron. Trade-off: más TTL → más capacidad de retroactive
  update sobre mensajes viejos, más espacio en DB.

### Tests

- 5 integration tests (`internal/bridge/msg_history_integration_test.go`)
  verificados contra Postgres real:
  - `SchemaIdempotent` — ensure es repetible sin errores.
  - `RecordPersists` — Record escribe a DB de forma async.
  - `WarmupReloads` — un tracker fresco recupera entries grabadas
    por un tracker previo (simula restart).
  - `UpdateAfterPatchPersists` — el UPDATE retroactive se persiste.
  - `CleanupOldBorraEntries` — DELETE por edad funciona.
- Activar con `INTEGRATION_PG_DSN=postgres://...`. Sin la env,
  skip silencioso (compat con CI sin Postgres).

### Migration notes

- **Sin breaking changes**. Cargas previas con
  `QRSGEN_RETROACTIVE_PERSIST=false` mantienen el comportamiento
  v0.40.0 (in-memory).
- El schema se crea automáticamente al boot vía
  `EnsureMsgHistorySchema`. Para downgrades a v0.40.0, la tabla
  queda huérfana — no rompe nada, simplemente no se usa.
- Permisos: el role usado en `POSTGRES_USER` necesita `CREATE`
  sobre la DB. El qrsgen actual ya los tiene por las tablas
  previas (bridge_dedup, bridge_outgoing_queue, etc.).

## [0.40.1] - 2026-05-29

Pulido post-v0.40.0 + revert del separador `<br>` que en Chatwoot
no funciona (su parser markdown trata `<br>` como autolink, lo
renderiza como `<code>br</code>`). Default vuelve a `\n\n`
(paragraph break, único confirmado fiable); el separador es
ahora **configurable** vía env para que cada despliegue pruebe
la variante que mejor le venga.

### Fixed

- **`<br>` separator se renderiza como `<code>br</code>` en Chatwoot
  (regresión v0.39.10)**: el parser markdown de Chatwoot detecta
  `<...>` como autolink y, al no ser una URL/email válida,
  cae en inline-code con el texto "br". Default vuelve a `\n\n`.

- **Reactions heredan el fix v0.39.9 (LID con PN saved)**:
  `handleReaction` duplicaba la lógica de resolución de nombre/saved
  con el mismo bug que se arregló en v0.39.9 para
  `applyGroupSenderPrefix` (cuando el sender es LID con PushName y
  el PN está saved, se mostraba el PushName del LID sin tilde en vez
  del nombre canónico). Ahora delega a `resolveSenderInfo` —
  helper centralizado, fix transitivo.

### Added

- **`QRSGEN_GROUP_HEADER_SEP` env var** + `Incoming.SetHeaderSep` +
  `bridge.ResolveHeaderSep`. Alias soportados:
  - `paragraph` / `p` → `"\n\n"` (default)
  - `br` → `"<br>"`
  - `br_self` / `br/` → `"<br/>"`
  - `lsep` / `u2028` → `" "` (Unicode LINE SEPARATOR)
  - `nl` / `soft` → `"\n"`
  - `slash` / `slash_nl` → `"\\\n"` (trailing-backslash hard break)
  - `spaced_br` → `" <br> "`
  - cualquier otro valor se usa literal (escape hatch).
  Permite iterar sin rebuild.

### Changed

- **`HandleContactUpdate` ejecuta los PATCHes en goroutine**: la
  versión v0.40.0 corría la iteración secuencial sobre `entries`
  (cap=200 default) dentro del event loop de whatsmeow. Con ~50ms
  por PATCH, eso bloqueaba ~10s **todos los eventos de todas las
  instancias** (mensajes, presencia, receipts) durante el update.
  En v0.40.1 el work se mueve a goroutine fire-and-forget y se
  rastrea con un `sync.WaitGroup` en `Incoming`.

- **`Incoming.WaitRetroactivePatches()` nuevo**: bloquea hasta que
  todas las goroutines en vuelo terminen. Tests lo usan para
  assertions deterministas y `cmd/server/main.go` lo llama en el
  shutdown grace para no dejar PATCHes a medias al cerrar.

### Added

- **Métricas Prometheus para retroactive name update** vía la
  serie existente `qrsgen_realtime_events_total`:
  - `feature="retroactive_name", result="ok"` por cada PATCH OK.
  - `result="ds_error"` por cada PATCH fallido.
  - `result="skip_disabled"` (feature off via env).
  - `result="skip_fullsync"` (whatsmeow propagando agenda).
  - `result="skip_empty_name"` (contacto sin nombre).
  - `result="skip_no_entries"` (sender sin mensajes tracked).
- **Logs Debug en skip cases**: `empty name`, `no tracked msgs`
  ahora dejan rastro a nivel debug — útil para depurar sin tener
  que recompilar.

### Migration notes

- Sin breaking changes. La API pública es la misma; solo cambia
  el modelo de ejecución (sync → async + WaitGroup).
- Si tu test/integración asume que `HandleContactUpdate` ya
  completó los PATCHes al retornar, llamar a
  `incoming.WaitRetroactivePatches()` después.

## [0.40.0] - 2026-05-29

Feature: **retroactive name update**.

Cuando el dueño del bot añade un contacto a la agenda WhatsApp tras
haber recibido mensajes de él (típicamente desde grupos), qrsgen
ahora reescribe los mensajes históricos posteados al downstream
para que reflejen el nuevo nombre y desaparezca el `~` de
"no-saved". El header de cada mensaje viejo pasa de
`` `+34604021705 · ~Richard` `` a `` `+34604021705 · Ricard Penin` ``
sin tener que esperar al siguiente mensaje.

### Added

- **`Incoming.EnableRetroactiveNameUpdate(capPerSender int)`**: activa
  el feature. `cap` controla cuántos mensajes recordamos por sender
  (FIFO al desbordar). Default `200` vía `QRSGEN_RETROACTIVE_CAP_PER_SENDER`.

- **`Incoming.HandleContactUpdate(ctx, instance, jid, fullName, firstName, fromFullSync, r)`**:
  orchestrator que se cablea al `events.Contact` de whatsmeow. Si
  el JID tiene mensajes tracked, los PATCHa con el header nuevo.
  Ignora `fromFullSync=true` (sync inicial al conectar — el state no
  cambió, solo se propaga la agenda).

- **`downstream.Client.UpdateMessageContent(ctx, convID, msgID, content)`**:
  wrapper de `PATCH /conversations/{c}/messages/{m}` con
  `{"content": "..."}`. Usado por el orchestrator.

- **`wameow.ContactHandler`** + `Conn.SetContactHandler` + `Manager.SetContactHandler`:
  cableado para subscribirse a `*events.Contact` (whatsmeow emite uno
  por cada add/edit en la agenda local del dueño).

- **`internal/bridge/msg_history_tracker.go`**: tracker in-memory de
  mensajes posteados, indexado por `instance|senderJID`. Guarda
  `convID`, `msgID`, `phone`, `nameUsed`, `wasSaved`, `body`,
  `postedAt`. Estado se pierde en restart (mensajes pre-restart no
  se actualizarán — aceptable MVP).

### Config

- **`QRSGEN_RETROACTIVE_NAME_UPDATE`** (default `true`): activa el feature.
- **`QRSGEN_RETROACTIVE_CAP_PER_SENDER`** (default `200`): cap de
  mensajes recordados por sender. >100 da margen para que el update
  llegue tras horas/días de acumulación.

### Changed

- **`applyGroupSenderPrefix` refactor**: ahora delega a
  `resolveSenderInfo` + `renderGroupSenderPrefix` (helpers nuevos
  expuestos para reutilización desde el orchestrator de retroactive
  update). Sin cambios funcionales para el caller.
- **Separador header/body extraído a constante `groupHeaderBodySep`**:
  permite cambiar la implementación (actualmente `<br>`) en un solo
  lugar. Aplica también al render retroactivo.
- **`resolveSenderInfo` hereda el fix v0.39.9**: cuando el sender es
  LID con PushName y el PN está saved, prefiere `ContactName(pn)`
  como nombre canónico.

### Limitaciones

- **State in-memory**: un restart de qrsgen pierde el histórico
  tracked. Los mensajes posteados antes del restart no se
  actualizarán retroactivamente cuando cambien sus senders.
  Aceptable como MVP — persistir en Postgres sería el siguiente paso.
- **Solo grupos**: solo se trackean mensajes que llevan
  prefix de grupo (es donde la rename tiene impacto visual). Chats
  1:1 no usan el prefix → no se trackean.
- **Cap finito**: con tráfico alto en un grupo, los mensajes más
  viejos del cap caen FIFO antes de poder actualizarse.
- **No revierte si se elimina de agenda**: si el contacto se quita
  de la agenda, qrsgen recibe `events.Contact` con name vacío.
  Como no guardamos el PushName original al postear, saltamos en
  lugar de romper el histórico con un nombre vacío.

### Tests

- `internal/bridge/msg_history_tracker_test.go`: 5 tests
  (Record+List, Cap enforced, Per-sender isolation, UpdateAfterPatch,
  Empty list returns nil).
- `internal/bridge/handle_contact_update_test.go`: 7 tests
  (PatchesStaleEntries, SkipsFromFullSync, SkipsWhenDisabled,
  NoEntriesForSender, EmptyNameSkips, FirstNameFallback,
  AlreadyUpToDateNoPatch). Stub via httptest.

### Migration notes

- Sin breaking changes. Feature on por default. Desactivar con
  `QRSGEN_RETROACTIVE_NAME_UPDATE=false` si no quieres PATCHes al
  downstream o si el token no tiene permisos para editar mensajes.
- El feature degrada gracioso: si el downstream rechaza el PATCH
  (HTTP 4xx/5xx), se loguea un warning y se continúa con el
  siguiente mensaje.

## [0.39.10] - 2026-05-29

Patch UX: separador header/body cambia a `<br>` HTML directo.

v0.39.9 usaba `\n\n` (paragraph break) — funciona pero el aire entre
header y body queda más grande de lo deseado. v0.39.10 usa `<br>`
literal: Chatwoot lo pasa por el sanitizer markdown→HTML, dando un
line break compacto (equivalente a shift+enter en su composer).

### Changed

- **`applyGroupSenderPrefix` devuelve `prefix + "<br>" + body`** en
  lugar de `prefix + "\n\n" + body`.

### Migration notes

- Sin breaking changes funcionales. Si tu renderer downstream
  sanitiza HTML estrictamente (sin `<br>` en allowlist), volverás
  a ver el body inline con el header — en ese caso revertir a
  `\n\n` localmente.

## [0.39.9] - 2026-05-29

Patch que arregla dos bugs reportados sobre el formato de prefix
de grupo (v0.39.6..v0.39.8):

1. **Name resolution stale en LID con PN saved**: cuando el sender
   es LID con PushName guardado en el store local (no saved) y el
   PN resuelto vía LIDForPN sí está saved con otro nombre, el
   header mostraba el PushName del LID sin tilde (saved=true pero
   nombre incorrecto). Ej: PushName="Richard", agenda="Ricard Penin"
   → renderizaba `` `+34604021705 · Richard` `` en lugar de
   `` `+34604021705 · Ricard Penin` ``.
2. **Line break entre header y body invisible**: el hard break
   CommonMark (`  \n`) introducido en v0.39.8 no lo renderiza
   Chatwoot — el body quedaba inline con el header.

### Fixed

- **`applyGroupSenderPrefix` LID→PN fallback**: si el PN está saved
  pero el LID no, ahora preferimos el `ContactName(pn)` (nombre
  canónico de la agenda) sobre el `ContactName(lid)` (típicamente
  un PushName auto-asignado por el remitente). El fix:

  ```go
  if !saved && pnSaved {
      name = r.ContactName(pn)
      saved = true
  } else if name == "" {
      name = r.ContactName(pn)
      if !saved { saved = pnSaved }
  }
  ```

- **Separador header/body → `\n\n` (paragraph break)**: en
  v0.39.8 usábamos `"  \n"` (dos espacios + newline = CommonMark
  hard break). Chatwoot no lo honora — colapsaba a inline. Cambiamos
  a `"\n\n"` (paragraph break estándar) que cualquier renderer
  markdown convierte en separación vertical visible.

### Tests

- Nuevo regression test `LID PushName but PN saved → prefer saved
  PN name` que reproduce el caso Ricard Penin.
- Actualizados todos los tests existentes de `TestApplyGroupSenderPrefix`
  con el nuevo separador `\n\n`.

### Migration notes

- Sin breaking changes funcionales. Parsers regex que detectaban
  el formato pueden seguir matchando el prefix; solo cambia lo que
  hay entre prefix y body (de `"  \n"` a `"\n\n"`).

## [0.39.8] - 2026-05-28

UX micro-tweak: markdown hard line break (dos espacios + `\n`) entre
header y body, en lugar del soft break (`\n` pelado).

### Changed

- **`applyGroupSenderPrefix` ahora devuelve `prefix + "  \n" + body`**
  con dos espacios trailing antes del `\n`. En CommonMark estricto
  esto genera un `<br>` explícito; el `\n` pelado se trata como soft
  break (whitespace que el renderer puede colapsar).
- Algunos renderers (incluido Chatwoot) tratan ambos casos igual,
  pero el hard break es más portable.

### Migration notes

- Sin breaking changes funcionales. El body después del separator es
  idéntico. Si tu renderer ignora el hard break, el output queda
  igual que v0.39.7.

## [0.39.7] - 2026-05-28

Alinea el formato de reacciones (v0.33.0) con el formato del prefix
de grupo (v0.39.6): code block + teléfono primero + middle dot +
tilde solo si no saved. Consistencia visual entre todos los headers
de sender que qrsgen postea al downstream.

### Changed

- **`handleReaction` ahora genera content con formato unificado**:
  - Antes (v0.33.0..v0.39.6):
    `**~Name** reaccionó con 👍` (markdown bold, tilde always)
    `**~Name** ` + `` `+phone` `` ` reaccionó con 👍` (con phone solo en grupos no saved)
  - Ahora (v0.39.7):
    `` `+phone · Name reaccionó con 👍` `` (saved, en code block)
    `` `+phone · ~Name reaccionó con 👍` `` (no saved)
    `` `+phone · ~Name quitó su reacción` `` (emoji vacío)

- **Teléfono incluido siempre** (no solo en grupos no-saved). Mantiene
  consistencia con la columna fija que da el formato E.164.
- **Tilde solo cuando no saved** — replicado de v0.39.5.

### Migration notes

- Sin breaking changes en API. Parsers regex que detectaban
  reacciones via `^\\*\\*~.*reaccionó` deben actualizarse al patrón
  `` ^`\+\d+ · ~?.*reaccionó `` o similar.
- El "_quitó su reacción_" pierde el italic markdown porque dentro de
  inline code no se procesa. Queda como texto plano dentro del code
  block.

## [0.39.6] - 2026-05-28

Reordena el prefijo de grupo: teléfono primero, middle dot,
nombre al final. Teléfono da ancho fijo natural (por E.164) que
sirve de columna consistente entre mensajes.

### Changed

- **`applyGroupSenderPrefix` ahora genera
  `` `+phone · <tilde-si-no-saved><Name>` ``** en lugar del orden
  previo `name + tabs + phone`. Ejemplos:
  - Saved: `` `+34604021705 · Jean Paul` ``
  - No saved: `` `+34663504782 · ~Marcelo Lopez` ``
- **Drop tabs**: ya no usamos el sistema de 1/2 tabs según longitud
  del nombre. El teléfono al inicio del code block ya da columna
  consistente porque el formato E.164 tiene longitud predecible.
- **Drop bold markers**: `**` ya no se incluyen porque dentro de
  inline code markdown no procesa formato. El contraste lo da el
  code block en sí (monospace + background distintivo).

### Razón

Pruebas en producción mostraron que Chatwoot dentro de inline code:
- No procesa `**` bold (quedaban literales como `**Name**`)
- Colapsa tabs a single space
- Sí preserva chars literales como `·` y `~`

Reordenar con phone primero + dot + name produce un layout
consistente sin depender de whitespace especial ni formatting
markdown que el renderer ignora.

### Examples

```
Saved (Google Contacts → libreta → WA):
  `+34604021705 · Jean Paul`
  (mensaje)

No saved (solo push name):
  `+34663504782 · ~Marcelo Lopez`
  (mensaje)
```

### Migration notes

- Sin breaking changes en API. Parsers regex deben usar el patrón:
  `` `\+\d+ · ~?[^`]+` ``

## [0.39.5] - 2026-05-28

Tilde (`~`) delante del nombre solo cuando el contacto NO está
guardado en agenda — replica la convention de WhatsApp donde los
contactos en la libreta del bot no llevan tilde y los conocidos
solo por push name sí lo llevan.

### Changed

- **`applyGroupSenderPrefix` reintroduce la lectura de
  `IsContactSaved`** (eliminada en v0.39.4). Pero esta vez no afecta
  a la presencia del teléfono — solo al prefijo `~`:
  - Contacto saved: `` `**Jean Paul**\t\t+34604021705` `` (sin `~`)
  - No saved:       `` `**~Jean Paul**\t\t+34604021705` `` (con `~`)
- El teléfono se sigue mostrando SIEMPRE (v0.39.4 stays).
- Lookup LID → PN: si el sender es LID y no es saved/no tiene name,
  se intenta resolver al PN equivalente vía `PNForLID` y se
  re-chequea allí. Mismo patrón que v0.32.0/v0.39.3.

### Examples

```
Saved (Google Contacts → libreta → WA):
  `**Richard**		+34604021705`
  (mensaje)

No saved (solo push name):
  `**~Marcelo Lopez**	+34663504782`
  (mensaje)
```

### Migration notes

- Sin breaking changes en API. Si tu integración parseaba el `~`
  como indicador absoluto de "es un grupo", ahora puede no aparecer
  — usa los backticks como delimitador del header en su lugar.

## [0.39.4] - 2026-05-28

Dos cambios en el prefijo de grupo:

### Changed

- **Header entero envuelto en code block** (`` `...` ``). Antes el
  teléfono ya no llevaba code; ahora el header completo
  (`**~Name** tabs +phone`) va dentro de backticks. Render: bloque
  monospace con background distintivo de Chatwoot para todo el
  header. Los `**` quedan literales porque markdown no procesa
  formato dentro de inline code — pero el contraste visual del
  bloque entero sigue distinguiendo header de body.

- **El teléfono se muestra SIEMPRE**, esté el contacto en agenda o
  no. Revierte la lógica `IsContactSaved → omit phone` que
  introdujo v0.32.0. Razón: aún con contactos guardados, ver el
  teléfono ayuda a hacer cross-reference cuando se manejan
  múltiples plataformas o el mismo número con varias entradas.
- **`IsContactSaved` sigue en la interfaz `WAResolver`**, pero
  `applyGroupSenderPrefix` ya no lo consulta. Puede usarse por
  callers externos si quieren su propia lógica.

### Migration notes

- Sin breaking changes en el contrato de `WAResolver` ni env vars.
- Parsers regex deben usar `` ` `` como delimitador del header:
  - Antes (v0.39.3): `\*\*~[^*]+\*\*\t+\+\d+`
  - Ahora (v0.39.4): `` `\*\*~[^*]+\*\*\t+\+\d+` ``

## [0.39.3] - 2026-05-28

UX micro-tweak: separador tab variable (1 o 2) según longitud del
nombre, para que los teléfonos queden alineados visualmente cuando
se mezclan senders con nombres de longitud distinta en la misma conv.

### Changed

- **`applyGroupSenderPrefix` ahora usa 2 tabs si el nombre tiene
  ≤12 runes, 1 tab si tiene >12 runes**. Razón: con un solo tab,
  "**~Richard**\t+34..." y "**~Ivan Madrid Sánchez**\t+34..."
  quedan en columnas distintas porque el name varía mucho de ancho.
  Con doble tab para nombres cortos, ambos teléfonos quedan cerca
  de la misma posición horizontal.
- Usa `utf8.RuneCountInString` para contar caracteres (no bytes),
  así "Sánchez" cuenta como 7 y no como 8.

### Migration notes

- Sin breaking changes. Parsers regex deben usar `\t+` en lugar de
  `\t` exacto para matchear ambos formatos:
  `\*\*~[^*]+\*\*\t+\+\d+`

## [0.39.2] - 2026-05-28

UX tweak del prefijo de grupo: separador tab + teléfono sin code block.

### Changed

- **`applyGroupSenderPrefix` ahora genera `**~<Name>**\t+<digits>`**
  con un TAB (U+0009) entre el bold del nombre y el teléfono plano.
  Quita el code block de backticks que añadía v0.30.1 y reemplaza
  el em-space (U+2003) por tab. Razón: el `+` ya identifica visualmente
  el teléfono sin necesitar code block, y el tab da más separación que
  el em-space en algunos renderizadores del downstream.
- Caso degenerate phone-only: `+<digits>:` (sin backticks, consistente
  con el cambio de arriba).

### Migration notes

- Sin breaking changes. Parsers regex deben actualizar al nuevo patrón:
  - Antes: `\*\*~[^*]+\*\*\s*`+\d+`` (con em-space + backticks)
  - Ahora: `\*\*~[^*]+\*\*\t\+\d+` (con TAB + plain phone)

## [0.39.1] - 2026-05-28

Bugfix: el handler `conversation_updated` (mark-as-read outgoing
introducido en v0.39.0) bloqueaba el webhook response esperando al
round-trip de `MarkRead` con WhatsApp. Si la conexión WA estaba
lenta o el batch de WAIDs era grande (cap 50), el webhook tardaba
varios segundos en responder a Chatwoot — riesgo de timeout y
double-mark por reintento.

### Fixed

- **`Outgoing.handleConversationUpdated` ahora dispara el `MarkRead`
  en una goroutine fire-and-forget**. El webhook devuelve 200 al
  instante; el read receipt se envía a WA en background con timeout
  propio de 15s. Si falla, log warning + WAIDs ya drenados se
  pierden (cosmético — el cliente no ve doble check para esos msgs
  concretos, no afecta correctness).
- **Drain ANTES del spawn**: el drenado del tracker se hace
  sincrónicamente fuera de la goroutine. Garantiza que cada
  conversation_updated entrante se lleva su slice de WAIDs sin race
  con events siguientes.

### Migration notes

- Sin breaking changes. El feature sigue funcionando igual desde el
  punto de vista del usuario; la única diferencia observable es que
  el webhook response es ahora <100ms en vez de hasta varios segundos.
- Logs siguen apareciendo igual (`"mark-as-read sent to WhatsApp"` o
  warning si falla), solo que vienen de la goroutine async.

## [0.39.0] - 2026-05-28

Mark-as-read outgoing: cuando el agente abre la conv en el downstream
y marca los mensajes como leídos, qrsgen propaga el read receipt a
WhatsApp para que el cliente vea el doble check azul. Requiere config
explícita del downstream para enviar el evento `conversation_updated`
al webhook de qrsgen.

### Added

- **`wameow.Conn.MarkRead(ctx, chat, sender, messageIDs, ts)`** —
  wrapper sobre `client.MarkRead()` de whatsmeow. Idempotente:
  llamar dos veces sobre los mismos WAIDs no genera doble notificación
  al cliente.
- **`internal/bridge/waid_tracker.go`** — tracker in-memory per-conv
  de WAIDs incoming. Cap default 50 entries/conv (FIFO al desbordar).
  Métodos: `RecordIncoming`, `DrainBefore`. 4 tests cubriendo: record
  + drain con cutoff, cap enforcement, aislamiento per-conv, drain
  de conv vacía.
- **`bridge.ReadMarker`** interfaz pequeña con un solo método MarkRead.
  Permite que Outgoing no dependa directamente de wameow.
- **`bridge.Outgoing.EnableMarkAsRead(waids, marker)`** — wire de
  ambos componentes. Si no se llama, el feature está desactivado y
  los eventos conversation_updated se ignoran.
- **`bridge.Incoming.EnableMarkAsRead()` returns *waidTracker** —
  crea el tracker compartido + lo conecta al sync path (RecordIncoming
  tras cada PostMessage exitoso).
- **`WebhookPayload.Conversation.AgentLastSeenAt`** y
  `ContactLastSeenAt` — nuevos campos del payload que el downstream
  manda con conversation_updated. Necesarios para saber hasta qué
  timestamp el agente leyó.
- **`senderAdapter.MarkRead`** — implementa la interfaz desde el
  Manager, delegando en la Conn correcta.
- **`QRSGEN_MARK_AS_READ_OUTGOING`** (default `true`) — env var nueva.

### How it works

1. Cliente envía mensaje a WhatsApp → llega a qrsgen
2. qrsgen lo postea al downstream y registra el WAID en el tracker
   per-conv (con timestamp)
3. Agente abre la conv en Chatwoot y marca como leído (visualmente
   el agent_last_seen_at se actualiza)
4. Chatwoot dispara webhook `conversation_updated` con el nuevo
   agent_last_seen_at hacia qrsgen
5. qrsgen drena los WAIDs registrados con ts ≤ agent_last_seen_at
   y llama `MarkRead` via whatsmeow
6. Cliente WhatsApp ve doble check azul en los mensajes leídos

### Configuration

Para que el feature funcione, en Chatwoot:
- Configurar webhook al `POST /api/instances/{name}/webhook` de qrsgen
- Activar el evento `conversation_updated` además de `message_created`

Sin esa config el feature no hace nada (no hay event para reaccionar).

### Caveats

- **In-memory tracker**: restart de qrsgen pierde el historial. Worst
  case: msgs leídos durante el downtime no se marcan en WA. Cosmético.
- **Cap de 50 WAIDs/conv**: convs muy activas pueden perder los más
  viejos, pero MarkRead solo importa para los recientes.
- **No bidirectional**: este feature es DS→WA. El WA→DS (cliente lee
  → contact_last_seen_at en downstream) ya estaba desde v0.34.1.

### Migration notes

- Sin breaking changes. Si el downstream no manda
  `conversation_updated`, el comportamiento es idéntico a v0.38.x
  (read receipts solo incoming).

## [0.38.0] - 2026-05-28

Media polish: mejor compat de voice notes y stickers en
reproductores HTML5 (Chatwoot UI + browsers modernos). Cambios
solo en filenames y mimes — sin conversión de formato (no requiere
ffmpeg ni deps externas).

### Changed

- **Voice notes** (audio con `PTT=true`) ahora se postean al
  downstream con:
  - `filename: voice-note.ogg` en lugar de `audio.opus` — la
    extensión `.ogg` activa el codec en más browsers/players.
  - `mimetype: audio/ogg` sanitizado, sin el sufijo `; codecs=opus`
    que algunos players HTML5 no parsean.
- **Audio normal** (no-PTT): mismo sanitizado de mime, fallback
  default `audio/ogg` si WA devuelve vacío.
- **Stickers** WebP: default `image/webp` si WA devuelve mime
  vacío (necesario para que el browser sepa renderizar).

### Added

- **`sanitizeMime`** helper en `internal/bridge/incoming.go`. Quita
  el parámetro de codec del Content-Type (`"audio/ogg; codecs=opus"`
  → `"audio/ogg"`).
- 5 tests del `sanitizeMime` cubriendo: empty, sin parámetro, con
  codec, con animated=true, sin espacio antes del `;`.

### Not included

- **NO se hace conversión de formato real**. Stickers WebP siguen
  siendo WebP, voice notes siguen siendo OGG/Opus. Convertir
  requeriría ffmpeg en el container — fuera de scope para v0.38.0.
  Si tienes browsers viejos que no soportan WebP/Opus, el problema
  persiste; considera un proxy de conversión en el downstream.

### Migration notes

- Sin breaking changes. Si tu integración parseaba el filename de
  audio buscando exactamente `audio.opus` para distinguir voice
  notes de audio normal, actualízalo al nuevo prefijo
  `voice-note.ogg`.

## [0.37.0] - 2026-05-28

Soporte para encuestas (polls) de WhatsApp. Antes los polls se veían
como mensajes vacíos; ahora aparecen con la pregunta y opciones
numeradas para que el agente vea lo que el cliente preguntó.

### Added

- **`formatPollContent`** helper. Extrae el `Name` (pregunta),
  `Options[]` (cada una con `OptionName`) y `SelectableOptionsCount`
  del `PollCreationMessage` y genera body legible:

  ```
  🗳️ **Encuesta:** ¿Día para el meeting?
  1. Lunes
  2. Martes
  3. Miércoles
  _(elige 1 opción)_
  ```

  Multi-select: `_(elige hasta N opciones)_`. Unlimited (`max=0`)
  omite el hint.

- **Polls v1 + v3** soportados. `extractTextContent` chequea ambos
  `GetPollCreationMessage()` y `GetPollCreationMessageV3()`.

### Not propagated

- **`PollUpdateMessage`** (votos individuales) NO se procesa.
  Chatwoot no tiene widget de polls que pueda reflejar votos
  agregados, así que tirarlos al body como mensajes nuevos por
  cada voto sería ruido. v0.37.x podría añadir un modo opt-in si
  hace falta.

### Edge cases

- Poll sin pregunta (`Name=""`) → body vacío (no merece la pena).
- Poll sin options → body vacío.
- Comentario adjunto al poll (raro) no se incluye.

### Tests

- 5 sub-tests en `TestFormatPollContent`: single, multi, unlimited,
  sin nombre, sin opciones.

### Migration notes

- Sin breaking changes. Patrón canónico para detectar polls en
  parsers downstream: `^🗳️ \*\*Encuesta:\*\*`.

## [0.36.0] - 2026-05-28

Soporte para mensajes de ubicación de WhatsApp. Antes se veían vacíos
en el downstream; ahora aparecen con link a Google Maps + opcionalmente
nombre del lugar (POI), dirección y comment del cliente.

### Added

- **`formatLocationContent`** helper en `internal/bridge/incoming.go`.
  Extrae lat/lng del `LocationMessage` y genera un body legible con
  link directo a Google Maps. Incluye:
  - Header `📍 Ubicación compartida` (o `📍 Ubicación en vivo` si IsLive)
  - Nombre del POI en bold (si lo provee el cliente)
  - Dirección textual (si la provee)
  - Link `https://maps.google.com/?q=LAT,LNG` con precisión 6 decimales
  - Comment del cliente en italic (si lo añadió)
- **`extractTextContent` ahora delega en `formatLocationContent`** cuando
  el mensaje tiene `LocationMessage` no-nil — antes caía a "msg sin
  contenido" y se ignoraba.

### Behavior

- **Live locations** (cuando el cliente envía ubicación actualizable
  por tiempo limitado): cada update genera un mensaje nuevo con el
  header "en vivo". qrsgen no aggrega ni edita el msg anterior — la
  conv en Chatwoot recibe N snapshots, uno por update WA.
- **Coordenadas 0,0** se ignoran (location inválida o vacía).
- **Compatibilidad con Google Maps**: el link funciona en cualquier
  cliente (móvil, web, etc.) y ofrece direcciones automáticas.

### Tests

- 5 sub-tests en `TestFormatLocationContent` cubriendo: lat/lng pelado,
  con name+address, live, con comment, coordenadas inválidas (0,0).

### Migration notes

- Sin breaking changes. Mensajes de location que antes se descartaban
  ahora aparecen con contenido. Si tu integración n8n parseaba el
  body raw esperando mensajes vacíos para detectar location, ajusta
  el matching (el patrón `^📍 ` es el indicador canónico).

## [0.35.0] - 2026-05-28

Observability: contador Prometheus unificado para los eventos
real-time del bridge (avatar/reaction/typing/read_receipt). Permite
calcular tasas de éxito, detectar regresiones y alertar sobre fallos
en producción.

### Added

- **`qrsgen_realtime_events_total{feature, result, instance}`** —
  nuevo CounterVec en `internal/metrics/metrics.go`. Labels:
  - `feature`: "avatar" | "reaction" | "typing" | "read_receipt"
  - `result`: "ok" | "no_contact" | "no_conv" | "throttled" |
    "filtered" | "wa_miss" | "wa_error" | "ds_error"
  - `instance`: nombre de la instancia
- Cardinalidad: ~4 features × ~8 results × ~N instancias. Para una
  deployment típica (1-10 instancias) son 32-320 series.

### Wired

- `Incoming.syncAvatar`: incrementa con result=ok/wa_miss/wa_error/
  ds_error/throttled según el path tomado.
- `Incoming.handleReaction`: result=ok/no_contact/no_conv/ds_error.
- `Incoming.HandleChatPresence`: ok/no_contact/no_conv/throttled/ds_error.
- `Incoming.HandleReceipt`: ok/no_contact/no_conv/filtered/ds_error.

### PromQL examples

Tasa de errores de downstream por feature:
```promql
sum by (feature) (rate(qrsgen_realtime_events_total{result="ds_error"}[5m]))
```

% de avatares con foto vs sin foto (privacidad):
```promql
sum(rate(qrsgen_realtime_events_total{feature="avatar",result="ok"}[1h]))
/
sum(rate(qrsgen_realtime_events_total{feature="avatar",result=~"ok|wa_miss"}[1h]))
```

Typing events throttleados (debería ser >50% del total típicamente):
```promql
sum(rate(qrsgen_realtime_events_total{feature="typing",result="throttled"}[5m]))
/
sum(rate(qrsgen_realtime_events_total{feature="typing"}[5m]))
```

### Migration notes

- Sin breaking changes. Las métricas son aditivas; los exporters
  Prometheus existentes las recogen automáticamente.
- Dashboards Grafana nuevos: actualizar para incluir los paneles
  sobre la nueva métrica. Ejemplo en
  `examples/grafana-dashboard/qrsgen-realtime.json` (pendiente).

## [0.34.1] - 2026-05-28

Read receipts incoming: cuando el cliente WhatsApp abre el chat y ve
los mensajes que envió el agente, qrsgen actualiza el
`contact_last_seen_at` de la conv en el downstream. La UI marca los
mensajes con "leído" / doble check azul.

### Added

- **`wameow.ReceiptHandler`** — callback type para `*events.Receipt`.
- **`wameow.Conn.SetReceiptHandler`** + case nuevo para `*events.Receipt`
  en el dispatcher.
- **`manager.Manager.SetReceiptHandler`** — propagación a Conns.
- **`bridge.Incoming.HandleReceipt`** — filtra por kind in
  ("read", "read-self"), encuentra conv, llama
  `UpdateContactLastSeen`.
- **`downstream.Client.UpdateContactLastSeen(convID, ts)`** —
  `POST /api/v1/accounts/X/conversations/Y/update_last_seen` con
  `agent_last_seen_at` y `contact_last_seen_at` = ts del receipt.
- **`QRSGEN_READ_RECEIPTS_SYNC`** (default `true`).

### Behavior

- Solo procesamos `ReceiptTypeRead` y `ReceiptTypeReadSelf`. Los
  otros (`delivered`, `played`, `sender`) se ignoran — son menos
  accionables y aumentarían el ruido al downstream.
- Si el contacto no existe en el downstream o no hay conv abierta,
  el receipt se descarta silenciosamente.
- El timestamp del receipt se pasa al downstream tal cual — la UI
  muestra los msgs como leídos hasta ese momento.

### Migration notes

- Sin breaking changes. `QRSGEN_READ_RECEIPTS_SYNC=false` revierte
  al comportamiento de v0.34.0 (no propagación de receipts).

## [0.34.0] - 2026-05-28

Typing indicators (composing) de WhatsApp se propagan al downstream.
El agente ve "está escribiendo..." en la UI del downstream cuando el
cliente está tipeando del lado WhatsApp.

### Added

- **`wameow.ChatPresenceHandler`** — nuevo callback type. Se dispara
  con `*events.ChatPresence` (composing/paused, text/audio).
- **`wameow.Conn.SetChatPresenceHandler`** + nuevo case para
  `*events.ChatPresence` en el event dispatcher de Conn.
- **`manager.Manager.SetChatPresenceHandler`** — propaga el callback
  a Conns existentes y futuras.
- **`bridge.Incoming.HandleChatPresence`** — orquesta:
  - Find contact + conv (sin crear nada si no existen)
  - Throttle via typingTracker (anti-spam)
  - POST a `toggle_typing_status` del downstream
- **`downstream.Client.SetTypingStatus(convID, typing)`** — implementa
  `POST /api/v1/accounts/X/conversations/Y/toggle_typing_status` con
  body `{"typing_status":"on"|"off"}`.
- **`bridge.typingTracker`** — dedupea calls al downstream. Anti-spam
  con minInterval default 4s. Cambios de estado siempre emiten.
- **`QRSGEN_TYPING_SYNC`** (default `true`) — env var nueva.
- **5 tests** del typingTracker: primer emit, mismo state dentro
  intervalo, cambio de state, mismo state tras intervalo, aislamiento
  per-conv.

### Behavior

- **Throttle de 4s**: typing events de WhatsApp llegan varios por
  segundo durante una sesión de escritura. Solo propagamos al
  downstream cuando el estado cambia (composing↔paused) o si han
  pasado 4s desde la última propagación del mismo estado (refresh).
- **Si el contacto no existe en downstream**, evento descartado.
  No creamos contactos para typing sueltos.
- **Grupos**: cuando hay typing en un grupo, llega un evento por
  participante. El downstream solo soporta un indicator por conv,
  así que se ven todos como "alguien está escribiendo". El campo
  `sender` se loguea para debug pero no se incluye visualmente.

### Migration notes

- Sin breaking changes. Default ON aumenta la frecuencia de calls
  POST al downstream — si tienes rate-limits muy ajustados,
  considera el throttle de 4s y/o desactivar con
  `QRSGEN_TYPING_SYNC=false`.

## [0.33.0] - 2026-05-28

Propaga las reacciones (emojis) que los clientes WhatsApp añaden a
mensajes hacia el downstream como un mensaje incoming con formato
visible. El agente ve en la conv quién reaccionó y con qué emoji,
sin tener que adivinarlo.

### Added

- **Reactions read-side**: cuando un `*events.Message` llega con
  `ReactionMessage` no-nil, qrsgen ya no lo ignora. En su lugar,
  invoca `handleReaction` que:
  - Resuelve sender + conv via el mismo path que mensajes normales
  - Aplica el resolver de nombre (incluyendo `IsContactSaved` de
    v0.32.0): contactos en agenda se muestran sin teléfono,
    desconocidos en grupos se muestran con phone code block
  - Postea al downstream como `message_type: incoming` con
    `source_id: "WAID:reaction:<msg.Info.ID>"`
  - Si el emoji es vacío (reacción retirada), formato
    `"**~Name** _quitó su reacción_"`
- **`QRSGEN_REACTIONS_SYNC`** (default `true`) — env var nueva.
  Setear a `false` ignora todas las reacciones silenciosamente.

### Format examples

```
Contacto en agenda:        **~Jean Paul** reaccionó con 👍
Grupo + desconocido:       **~Richard** `+34604021705` reaccionó con ❤️
Reacción retirada:         **~Jean Paul** _quitó su reacción_
```

### Behavior

- **Reacciones del propio bot (`IsFromMe`) se ignoran**. El agente
  reaccionando desde el downstream no tiene flujo write-back hoy
  (sería v0.34.x outgoing reactions). Por ahora solo read-side.
- **Si el contacto no existe en downstream**, la reacción se
  descarta — no creamos contactos para reacciones sueltas.
  Esperamos al primer mensaje normal del JID para crearlo.
- **El target del mensaje reaccionado** se loguea (`target_msg_id`)
  pero no se incluye visualmente. Chatwoot no tiene UI nativa para
  asociar la reacción al mensaje original; la inferencia visual la
  hace el agente por proximidad en el timeline.

### Migration notes

- Sin breaking changes. Default ON aumenta el volumen de mensajes
  postados al downstream — si tu integración tiene rate-limits
  ajustados o métricas que cuentan mensajes (billing), considéralo.
- `QRSGEN_REACTIONS_SYNC=false` mantiene el comportamiento de
  v0.32.x (reacciones ignoradas).

## [0.32.1] - 2026-05-28

Bugfix del bulk avatar resync endpoint (v0.31.3): la ruta usada en
Chatwoot no existe y devolvía 404, haciendo que el endpoint fallara
con HTTP 500 inmediatamente.

### Fixed

- **`Client.ListContactsByInbox`** ahora usa el endpoint canónico de
  Chatwoot `GET /accounts/{a}/contacts?inbox_id={i}&page={n}` en
  lugar de `GET /accounts/{a}/inboxes/{i}/contacts?page={n}` que NO
  existe en la API. Con esto `POST /api/instances/:name/avatars/resync`
  funciona correctamente — itera todos los contactos del inbox y
  dispara el sync de avatar por cada uno.
- 2 tests nuevos en `internal/downstream/client_test.go`:
  - Verifica que el URL shape es el canónico (`/contacts?inbox_id=X&page=Y`)
  - Verifica que `hasMore=true` cuando la página tiene 15 contactos
    (page_size típico de Chatwoot)

### Impact

- El sync per-message (response a cada mensaje incoming) NO estaba
  afectado — funcionaba correctamente desde v0.31.0. Solo el bulk
  endpoint introducido en v0.31.3 estaba roto.
- Operadores que no usaron el bulk endpoint vieron sus avatares
  poblarse orgánicamente vía el path per-message (con o sin
  `QRSGEN_AVATAR_REFRESH_TTL=24h` activo).
- Tras este fix, el bulk endpoint puede usarse para backfillear
  contactos viejos sin esperar a que vuelvan a escribir.

### Migration notes

- Sin breaking changes. Es un bugfix puro. Upgradear desde v0.31.3
  → v0.32.1 lo arregla automáticamente.
- Operadores que no necesiten el bulk (porque ya están viviendo
  con el sync per-message) pueden seguir en v0.31.x sin afectación
  funcional.

## [0.32.0] - 2026-05-28

Distingue contactos guardados en la libreta del bot vs solo push name.
Los contactos en agenda se postean al downstream con solo el nombre
(el agente ya sabe quién es); los desconocidos siguen con el bloque
de teléfono code para identificarlos.

### Added

- **`WAResolver.IsContactSaved(jid) bool`** — nuevo método de la
  interfaz. Devuelve true si el JID tiene `FullName` o `FirstName`
  en el contact store de whatsmeow (lo que indica que está en la
  libreta del móvil del bot, originada típicamente desde Google
  Contacts → Android sync → WhatsApp app). PushName auto-asignado
  por el propio usuario NO cuenta como "saved".

### Changed

- **`applyGroupSenderPrefix` ahora omite el bloque de teléfono
  cuando `IsContactSaved` es true**. Formato resultante:

  ```
  Contacto en agenda:        **~Jean Paul**
                              <body>

  Solo push name (anónimo):  **~Richard** `+34604021705`
                              <body>
  ```

- Lookup LID → PN: si el sender llega como LID y no es saved/no
  tiene name, se intenta resolver al PN equivalente vía `PNForLID`
  y se re-chequea allí. Maneja correctamente el caso de senders
  hidden que sí están en la libreta del bot via su número.

### Behavior

- **Sin nuevas env vars**. La distinción es automática basada en
  el contact store de whatsmeow. Si tu móvil pareado no tiene
  Google Contacts sync activo, ningún contacto será "saved" y se
  comportará igual que v0.31.x (siempre muestra teléfono).
- **Grupos siempre van con teléfono** porque los grupos no se
  guardan en la libreta como contactos individuales (FullName/
  FirstName están vacíos para JIDs `@g.us`).
- **Push name no rompe nada**: si el contacto solo tiene PushName
  (no está en agenda), se sigue mostrando el formato anterior con
  bloque de teléfono.

### Migration notes

- Sin breaking changes. Es UX puro: el formato cambia solo para
  contactos que el bot tiene en su libreta. Parsers regex deben
  manejar ambos casos:
  - `\*\*~[^*]+\*\*` (saved, solo nombre)
  - `\*\*~[^*]+\*\* \`\+\d+\`` (unsaved, con teléfono)
- **`WAResolver` añade un método** (`IsContactSaved`). Mocks externos
  del interface necesitan implementarlo. Trivial: `return false`
  mantiene el comportamiento v0.31.x (siempre teléfono).

## [0.31.3] - 2026-05-28

Bulk re-sync endpoint para backfillear avatares de contactos viejos
(creados antes de v0.31.0 o que no han recibido mensajes recientes
que disparen el sync vía sync()).

### Added

- **`Client.ListContactsByInbox(ctx, inboxID, page)`** — endpoint
  helper para paginar contactos de un inbox via Chatwoot API
  (`GET /api/v1/accounts/X/inboxes/Y/contacts?page=Z`). Devuelve
  `(contacts, hasMore, error)`. Detecta paginación comparando con
  page_size default de Chatwoot (15).
- **`Incoming.ResyncInstanceAvatars(ctx, instance, r, inboxID)`** —
  itera todas las páginas de contactos del inbox, parsea identifier
  como JID, lanza `syncAvatar` por cada uno bypassando el tracker.
  Devuelve `ResyncResult` con scanned/skipped/queued/pages.
  Cap defensivo de 200 páginas (~3000 contactos).
- **`POST /api/instances/{name}/avatars/resync`** — endpoint REST
  que dispara el bulk. Requiere API token. Resuelve inbox via la
  config de la instancia y delega en `ResyncInstanceAvatars`.

### Use case

Tras adoptar v0.31.0+ por primera vez, los contactos viejos en
Chatwoot tienen letter-avatars. El sync automático solo aplica al
crear contacto o cuando llega un mensaje (sync flow) o cuando WA
emite `events.Picture`. Si tienes contactos inactivos, no se les
sincronizará nunca a menos que les llames manualmente. Este
endpoint permite hacerlo en una sola operación.

```bash
curl -X POST -H "Authorization: Bearer $QRSGEN_API_TOKEN" \
  https://qrsgen.example.com/api/instances/CONEXIA4/avatars/resync
```

Respuesta:
```json
{
  "instance": "CONEXIA4",
  "scanned": 234,
  "skipped": 5,
  "queued": 229,
  "pages": 16
}
```

### Migration notes

- Sin breaking changes. La feature es opt-in via llamada explícita
  al endpoint; sin invocarlo, el sistema funciona como en v0.31.2.

## [0.31.2] - 2026-05-28

Real-time avatar refresh: subscribe a `events.Picture` de whatsmeow
y fuerza re-sync inmediato cuando alguien (usuario o grupo) cambia
su foto. No depende del TTL del tracker — el evento es la señal
canónica.

### Added

- **`wameow.PictureHandler`** — nuevo callback type que se inyecta
  en `Conn.SetPictureHandler(h)`. Se dispara con `*events.Picture`
  (JID, pictureID, removed, resolver).
- **`wameow.Conn.handle`** — nuevo case para `*events.Picture`.
- **`manager.Manager.SetPictureHandler(h)`** — propaga el handler a
  cada Conn (existente y futura). Llamar antes de Bootstrap para
  capturar instancias auto-reconnect.
- **`Incoming.HandlePictureChange`** — orquesta la respuesta al
  evento: encuentra el contact en downstream via FindContact, resetea
  el LastID del tracker (forzar re-descarga), spawnea syncAvatar.
  Si el contact no existe, no hace nada (al primer mensaje del JID,
  sync()→CreateContact→maybeAvatarSync hará el sync inicial).

### Behavior

- Cuando un usuario cambia su foto en WhatsApp móvil → evento
  llega a qrsgen → en ~1s su avatar en Chatwoot también está
  actualizado. Sin esperar al siguiente mensaje, sin esperar al TTL.
- Mismo flow para grupos cuando el admin cambia la foto del grupo.
- Si `pictureID` está vacío y `removed=true`, syncAvatar internamente
  cachea el "" — el contact queda con su último avatar conocido en
  Chatwoot (no se elimina automáticamente).
- Sin tracker (`QRSGEN_AVATAR_REFRESH_TTL=0`), el evento sigue
  funcionando — se ignora el tracker pero el resto del flow opera.

### Migration notes

- Sin breaking changes. La feature está ON por default vía el
  wiring en `main.go`. Convs/contactos existentes en Chatwoot SE
  ven beneficiados sin acción operativa.

## [0.31.1] - 2026-05-28

Smart refresh del avatar: detecta cambios de foto en WhatsApp y
re-sincroniza solo lo necesario. Aplica a contactos EXISTENTES, no
solo a los recién creados (modo v0.31.0).

### Added

- **`WAResolver.GetProfilePictureID(ctx, jid) (string, error)`** —
  versión cheap de GetProfilePicture: solo devuelve el ID
  (hash/version) del avatar actual, no descarga la imagen. Permite
  comparar antes de decidir si re-descargar.
- **`internal/bridge/avatar_tracker.go`** — tracker en memoria
  (per-instancia + per-JID) con `ShouldCheck(TTL)`, `LastID`,
  `UpdateID`. ShouldCheck es atómica: bumpea timestamp en `true`
  para que múltiples goroutines concurrentes (burst de mensajes)
  no spawnen el mismo sync varias veces.
- **`Incoming.maybeAvatarSync`** — wrapper que gate-keepea el spawn
  de la goroutine via el tracker.
- **`QRSGEN_AVATAR_REFRESH_TTL`** (default `24h`) — env var nueva.
  `0` desactiva el refresh (modo v0.31.0: sync solo al crear).
- **7 tests** del avatar_tracker: primer chequeo, dentro/fuera de
  TTL, UpdateID, aislamiento per-JID, aislamiento per-instancia,
  concurrencia (burst de 20 goroutines, solo una ve `true`).

### Changed

- **`Incoming.syncAvatar` refactorizado** a flow smart:
  1. Get current ID (cheap metadata, no descarga).
  2. Si == lastKnownID → skip descarga.
  3. Si == "" → sin foto, cachear y exit.
  4. Si distinto → download + upload + update tracker.
  
  Antes (v0.31.0) siempre descargaba la imagen completa.
  
- **Aplica el sync tanto a contactos creados como existentes** (el
  callsite en sync() ya no está dentro del `if contact == nil`). El
  tracker decide si toca según TTL.

### Migration notes

- Sin breaking changes. Default ON, TTL 24h. Si quieres mantener
  el comportamiento exacto de v0.31.0 (solo al crear contact, sin
  refresh), setea `QRSGEN_AVATAR_REFRESH_TTL=0`.
- **`WAResolver` añade un método** (`GetProfilePictureID`). Mocks
  externos necesitan implementarlo.

## [0.31.0] - 2026-05-28

Sincroniza la foto de perfil de WhatsApp al avatar del contacto en
el downstream. Aplica tanto a contactos 1-on-1 (foto del usuario)
como a grupos (foto del grupo). Reemplaza los letter-avatars
autogenerados por Chatwoot por las fotos reales.

### Added

- **`WAResolver.GetProfilePicture(ctx, jid) ([]byte, string, error)`** —
  nuevo método de la interfaz. Hace `client.GetProfilePictureInfo` +
  HTTP GET a la URL devuelta. Devuelve `([], "", nil)` cuando el JID
  no tiene foto configurada (estado válido, no es error).
- **`Client.UploadContactAvatar(ctx, contactID, data, mime)`** —
  PUT multipart a `/api/v1/accounts/X/contacts/Y` con el avatar.
- **`Incoming.syncAvatar(ds, r, contactID, jid)`** — orquesta el sync
  fire-and-forget. Spawn en goroutine tras `CreateContact` exitoso.
- **`QRSGEN_AVATAR_SYNC`** (default `true`) — env var nueva.
  Setear a `false` desactiva la feature; los contactos siguen creándose
  pero sin avatar (Chatwoot pinta su letter-avatar default).

### Behavior

- **Solo en contact-creation**. Si el contacto ya existía en downstream,
  qrsgen NO refresca el avatar — un cambio de foto en WhatsApp tras la
  creación no se propaga. Refresh periódico queda para v0.31.1.
- **Fire-and-forget**. Errores de WA (foto privada, account restringida)
  o downstream (upload rechazado) loguean warning pero no bloquean el
  flujo del mensaje. El contact se crea aunque el avatar falle.
- **Timeouts**: 10s para fetch desde WA, 30s overall para todo el sync.
- **No bloquea el msg processing**. La goroutine corre paralela al
  envío del mensaje a downstream.

### Migration notes

- Sin breaking changes. La feature está ON por default — espera ver
  fotos reales en convs nuevas tras deploy. Convs/contactos existentes
  no se actualizan (no hay bulk sync; v0.31.2 si se quiere).
- **`WAResolver` añade un método**. Mocks externos del interface
  necesitan implementarlo. Trivial: return `nil, "", nil`.

## [0.30.2] - 2026-05-28

UX tweak: em-space (U+2003) entre nombre y code block del teléfono.

### Changed

- **`applyGroupSenderPrefix`** ahora intercala un em-space (U+2003,
  un carácter Unicode "wide space") entre el bold del nombre y el
  inline code del teléfono. Razón: en v0.30.1 quedaban tocándose,
  porque Chatwoot colapsa espacios normales a uno solo en render.
  El em-space NO se colapsa (es un codepoint distinto al `\x20`)
  y ocupa ~4x más, dando la respiración que faltaba.

### Migration notes

- Cambio cosmético. Si tu parser regex matchea exactamente
  ` ` entre el `**` y el `` ` ``, ahora hay un ` ` allí.

## [0.30.1] - 2026-05-28

UX tweak: teléfono en inline code block en lugar de italic + separador.

### Changed

- **`applyGroupSenderPrefix` ahora usa `**~<Name>** \`+digits\``**
  con el teléfono envuelto en backticks (inline code) en lugar de
  underscores (italic). Chatwoot renderiza inline code con fondo
  distintivo y fuente monospace, lo que separa visualmente el
  teléfono del nombre sin necesitar el dot `·` que añadimos en
  v0.29.7. Se ve más limpio y profesional.
- **Caso degenerate phone-only** ahora también va en code:
  `` `+digits`: `` en lugar de `_+digits_:`.

### Migration notes

- Cambio puramente cosmético. Parsers regex actualizar al nuevo
  patrón `\*\*~[^*]+\*\* \`\+\d+\`` si dependían del italic.

## [0.30.0] - 2026-05-28

Suprime el header de remitente en mensajes consecutivos del mismo
participante dentro de un grupo. Replica la convención visual de
WhatsApp (header en el primer msg del burst, nada en los siguientes).

### Added

- **`internal/bridge/group_tracker.go`** — tracker per-instancia +
  per-grupo del último sender visto. API: `RecordAndCheck(instance,
  chatJID, senderJID) bool`. Returns true si toca emitir header
  (sender distinto al previo, o TTL expirado, o no había registro).
  Estado en memoria — un restart de qrsgen reinicia los burst counters.
- **`QRSGEN_GROUP_HEADER_TTL`** (default `10m`) — env var nueva.
  Setear a `0` desactiva la feature (header siempre).
- **6 tests** del tracker: primer msg, burst del mismo sender, cambio
  de sender, TTL expirado, aislamiento per-grupo, aislamiento
  per-instancia.

### Changed

- **`Handle` en `incoming.go` ahora consulta el tracker** antes de
  aplicar el prefix de grupo. Mensajes `fromMe` (del agente) se
  registran como `_bot` para que el siguiente mensaje real reciba
  header correctamente tras una intervención del agente.

## [0.29.7] - 2026-05-28

UX tweak final del prefijo de grupo: middle dot como separador
visible (markdown colapsa N espacios a 1) + teléfono compacto sin
espacio entre CC y national.

### Changed

- **`applyGroupSenderPrefix` ahora usa `**~<Name>** · _+<digits>_`**
  con middle dot (·, U+00B7) entre name y teléfono. Razón: Chatwoot
  colapsa los espacios consecutivos a uno solo en render. Dot
  explícito siempre se ve.
- **`formatE164` simplificado al mínimo**: `"34604021705" → "+34604021705"`.
  El espacio entre CC y national también era ruido cuando el teléfono
  está adyacente al nombre — `+` ya marca el inicio del número.

### Removed

- **`detectCountryCode` + tablas `ccLen1/2/3`** — dead code tras
  simplificar formatE164. ~30 LoC menos.

## [0.29.6] - 2026-05-28

UX micro-tweak: 3 espacios entre nombre y teléfono en lugar de 2.

### Changed

- **`applyGroupSenderPrefix` ahora usa TRES espacios** entre el bold
  del nombre y el italic del teléfono:
  `**~<Name>**   _+CC ..._`. Razón: con 2 espacios el espacio entre
  ambos era demasiado tight; 3 marca mejor la separación visual.

## [0.29.5] - 2026-05-28

UX tweak: tilde delante del nombre, dentro del bold — matchea la
convención visual que WhatsApp usa para indicar remitente.

### Changed

- **`applyGroupSenderPrefix` ahora genera `**~<Name>**  _+CC ..._`**
  (tilde como primer char dentro del bold) en lugar de
  `**<Name>**  _+CC ..._`. Razón: el tilde delante es la pista visual
  que WhatsApp usa nativa para "esto es el sender del grupo", y
  reproducirlo hace que el chat lea más como WhatsApp y menos como
  un text dump genérico.
- Mismo `~` aplica al caso degenerate name-only: `**~<Name>**:`.

### Migration notes

- Cambio puramente cosmético. Parsers regex actualizar a
  `\*\*~[^*]+\*\*` si dependían del bold-name-pattern.

## [0.29.4] - 2026-05-28

UX tweak: el teléfono ahora solo separa el country code del national
number con un espacio; el national queda compacto.

### Changed

- **`formatE164` simplificado**: `"34604021705" → "+34 604021705"` en
  lugar de `"+34 604 02 17 05"`. Razón: el agrupamiento intra-national
  era ruido. Lo único que aporta lectura es el `+CC` separado del
  resto.
- **Tabla de CCs reconocidos sigue igual** (NANP, Europa, Latam, Asia
  operacional, Portugal, Israel, Emiratos). CCs no listados se dejan
  compactos con `+` delante (sin separación).

### Removed

- **`groupNationalNumber` y `groupByThree`** — funciones internas no
  longer needed con el nuevo formato. Eliminadas para reducir
  superficie.

### Migration notes

- Cambio puramente cosmético. Si un parser regex usaba el patrón
  `\+\d+( \d+){3,4}` (CC + 3-4 grupos), ahora debe ajustarse a
  `\+\d+ \d+` (CC + un solo grupo).

## [0.29.3] - 2026-05-28

UX tweak adicional del prefijo de grupo: nombre en **bold** para que
salte visualmente.

### Changed

- **`applyGroupSenderPrefix` ahora genera `**<Name>**  _+CC ..._`**
  (nombre en bold). Chatwoot renderiza `**...**` como negrita.
  Razón: en una conv con 3-5 senders el ojo necesita escanear quién
  habla; nombre en bold + teléfono italic da la jerarquía visual
  correcta sin sumar líneas.
- Mismo bold aplica al caso degenerate name-only: `**<Name>**:`.

### Migration notes

- Cambio puramente cosmético, sin nuevas env vars.
- Parsers regex actualizar a `\*\*[^*]+\*\*\s+_[^_]+_` si dependían
  del formato anterior.

## [0.29.2] - 2026-05-28

UX tweak del prefijo de grupo: quita los paréntesis del teléfono y
añade doble espacio de separación.

### Changed

- **`applyGroupSenderPrefix` ahora genera `<Name>  _+CC NNN NN NN NN_`**
  (dos espacios entre name y teléfono, teléfono italic sin paréntesis)
  en lugar de `<Name> _(+CC ...)_`. Razón: los paréntesis añadían
  ruido visual y el doble espacio marca mejor la separación entre
  foreground (nombre) y background (teléfono).
- **Degradación phone-only** ahora también va italic:
  `_+CC NNN ..._:\n<body>` en lugar de `+CC NNN ...:`. Consistencia
  con el caso con nombre.

### Migration notes

- **Sin nuevas env vars**, sin cambios de comportamiento del routing.
- Cambio cosmético: parsers que matcheaban `_\(\+[\d ]+\)_` necesitan
  actualizar al patrón `_\+[\d ]+_` (sin paréntesis). El doble espacio
  entre nombre y teléfono también puede afectar regex.

## [0.29.1] - 2026-05-28

UX patch del prefijo de grupo: nombre primero, teléfono en "segundo
plano" (italic + paréntesis), y teléfono formateado E.164 con
espaciado por país. La readabilidad mejora notablemente cuando
varios participantes intervienen en la misma conversación.

### Changed

- **`applyGroupSenderPrefix` ahora genera `<Name> _(+CC NNN NN NN NN)_`**
  en lugar de `+<phone> - <Name>:`. Chatwoot renderiza el `_..._` como
  italic, así que el teléfono queda visualmente subordinado al nombre.
  Razón: con 3+ senders distintos en una conv del grupo, el "+phone -"
  inicial le robaba prioridad al contenido del mensaje.
- **Teléfono formateado E.164 por país**. España (`+34`) usa 3-2-2-2
  (e.g. `+34 604 02 17 05`, matcheando el estilo de Evolution); resto
  de países agrupa por 3 desde la izquierda (`+33 612 345 678`). CCs
  reconocidos: NANP (+1), Europa común (+30..+49), Latam (+51..+58),
  Asia operacional, Portugal (+351), Israel (+972), Emiratos (+971).
  Para CCs no listados, deja el número compacto con `+` delante.

### Added

- **`formatE164`, `detectCountryCode`, `groupNationalNumber`,
  `groupByThree`** — helpers en `internal/bridge/incoming.go`. 12 tests
  cubriendo Spain mobile/landline, France, Germany, UK, US, Portugal,
  Italy, Mexico, CCs desconocidos, y casos degenerados.

### Migration notes

- **Sin nuevas env vars**. `QRSGEN_GROUP_PREFIX_SENDER=false` sigue
  desactivando el prefix entero como antes.
- **Cambio de formato observable** en Chatwoot/downstream para
  integraciones que parseaban el body raw esperando `+phone - Name:`.
  Si tienes un parser regex de n8n que dependa del formato viejo,
  actualízalo al nuevo (`<Name> _\(\+[\d ]+\)_`) o desactiva el prefix.

## [0.29.0] - 2026-05-28

Conversaciones de WhatsApp en grupo se reflejan correctamente en
downstream: el nombre del grupo va al título de la conv, y cada
mensaje incoming lleva prefijo identificando al participante.

### Added

- **`wameow.WAResolver.GroupSubject(jid)`** — nuevo método de la
  interfaz que resuelve el subject (nombre visible) de un grupo
  via `client.GetGroupInfo()`. Implementación con cache TTL de 10
  min para positivos / 1 min para negativos: GetGroupInfo es un
  round-trip al server WA y no queremos hacerlo por cada mensaje.
- **`QRSGEN_GROUP_PREFIX_SENDER`** (default `true`) — env var nueva
  que controla si qrsgen prefija el body de los mensajes incoming
  de grupos con la identidad del remitente. Sin él, en una misma
  conv del downstream múltiples participantes son indistinguibles.
  Formato: `+<phone> - <name>:\n<body>` cuando hay teléfono y push
  name; degrada a uno solo si falta el otro. Si el participante
  no es identificable (LID sin mapping ni push name), el body se
  postea sin prefijo en lugar de basura.
- **Tests nuevos** en `internal/bridge/incoming_test.go` cubriendo
  los 6 cases de `applyGroupSenderPrefix` (PN, LID resolvable, LID
  no resolvable, body vacío, sin identificación posible) +
  `GroupSubject` en el `fakeResolver`.

### Changed

- **`internal/bridge/incoming.go` Handle ahora distingue grupos**.
  Para `chat.Server == g.us` consulta `GroupSubject(chat)` primero
  (en lugar de `ContactName`, que solo cubre contactos individuales
  porque `Store.Contacts` no indexa grupos). Si el subject no
  resuelve, NO cae a `PushName` del participante (eso pondría el
  nombre del primer remitente como título del grupo entero).

### Migration notes

- **Sin breaking changes de API**. La nueva env var tiene default
  `true` — si tu integración n8n parseaba el body raw sin esperar
  el prefijo `+34… - Name:\n`, ponla a `false` o ajusta tu parser.
- **`WAResolver` añade un método nuevo** (`GroupSubject`). Si tienes
  un mock externo del interfaz, necesitas añadirlo. Implementación
  trivial que devuelva `("", false)` mantiene compat.

## [0.28.5] - 2026-05-27

Public error codes: contrato estable para que integradores pattern-
matcheen errores programáticamente sin parsear strings.

### Added

- **`internal/errcode/` package** con códigos string públicos del
  patrón `QRSGEN_<CATEGORY>_<REASON>`. Inicial set: `SpamguardBlocked`,
  `HMACMismatch`, `PayloadInvalid`, `QueueFull`, `InstanceNotFound`,
  `TenantNotFound`, `Internal`. `HumanText(code)` devuelve descripción
  humana en español para mostrar en UI.
- **Doc nuevo** `docs/api/error-codes.md` con la tabla completa +
  ejemplos de uso (Python, n8n).

### Changed

- **Respuestas de error ahora incluyen `error_code` + `error`** además
  de los campos contextuales. Ejemplo para spamguard 422:
  ```json
  {
    "error_code": "QRSGEN_SPAMGUARD_BLOCKED",
    "error": "Bloqueado por QRsGEN — duplicado...",
    "status": "blocked"
  }
  ```
  Backward compatible: el HTTP status sigue siendo el mismo, y los
  campos antiguos (`status`, `reason`) se mantienen donde aplicaba.

## [0.28.4] - 2026-05-27

Spamguard block ahora se refleja visualmente en el chat del cliente:
mensaje en rojo en lugar de verde.

### Changed

- **`POST /api/instances/:name/webhook` devuelve HTTP 422** cuando
  spamguard bloquea un outgoing duplicado. Chatwoot (y cualquier
  downstream que respete el contrato `api_channel`) marca entonces
  el mensaje como `failed` (icono rojo) en lugar de `sent` (verde).
  El agente sabe al instante que su mensaje no se entregó. Antes
  qrsgen devolvía 200 silenciosamente y el block solo se notificaba
  vía evento lifecycle externo.
- Nuevo sentinel error público `bridge.ErrSpamguardBlocked` para que
  callers puedan distinguir el block del resto de errores.

## [0.28.3] - 2026-05-27

UX del `spam_blocked` event: contexto para que el integrador pueda
linkear al mensaje y avisar en la conversación del cliente.

### Added

- **`spam_blocked` lifecycle event ahora incluye `msg_id`, `conv_id`
  y `remote_jid`** en los extras del payload, además de `count` y
  `preview` que ya estaban. Permite que el integrador (n8n / Omnia)
  construya un link directo al mensaje bloqueado (`/conversations/{conv_id}`)
  y poste una nota interna en la conv del cliente avisando que el
  outgoing no se entregó. Antes el agente veía su mensaje como `sent`
  en Chatwoot sin saber que qrsgen lo había bloqueado.

## [0.28.2] - 2026-05-27

Tres pulidos observables: una race real arreglada + métrica de versión
+ UX del key cifrado.

### Fixed

- **Race "Error al enviar" en body de `backend_started`** tras deploy.
  Síntoma observable: pill verde OK + body en rojo justo después. Causa:
  el broadcast se disparaba antes de que el HTTP server del nuevo
  container estuviera accept-ready, así que el dispatch de vuelta de
  Chatwoot al webhook hit connection-refused. Fix: el broadcast ahora
  hace polling al `/api/health` propio (timeout 5s) antes de emitir,
  garantizando que estamos aceptando POSTs.

### Added

- **`qrsgen_version_info{version="X.Y.Z"} 1`** — nueva métrica
  Prometheus (info-style gauge fijo a 1) para que dashboards Grafana
  hagan join contra otras series y muestren la versión activa de
  qrsgen. Estándar de ops desde Prometheus 2.x.

### Changed

- **`OUTBOX_ENCRYPTION_KEY`** ahora acepta también base64 URL-safe (con
  o sin padding), no solo el estándar. Soluciona friction cuando la
  key viene de un secret manager (Vault, GitHub Actions) que normaliza
  a URL-safe. Tests añadidos para las 4 variantes.
- **Sample defaults** del repo (`docker-compose.yml` + `.env.example`)
  bumped de `0.19.1` (estancado meses) a `0.28.1`. Sin impacto en
  binarios — solo template files para nuevos integradores.

## [0.28.1] - 2026-05-27

Cleanup pass post-marathon: cabos sueltos de v0.27 + v0.28, sin
nuevas features. Incluye un nuevo campo `version` en lifecycle events
(útil para integradores que quieran mostrar el tag del binario al
usuario, e.g. "QRsGEN v0.28.1 operativo").

### Added

- **`version` en el payload de todos los lifecycle events**:
  emitidos vía `emitLifecycleWebhook` y `emitCustomWebhook`. Inyectado
  por `mgr.SetVersion(version)` desde main. El integrador puede
  renderizar el tag en sus mensajes (e.g. el bot Omnia ahora muestra
  "QRsGEN vX.X.X" en los pills de backend_started / backend_restarting).

### Changed

- **Outbox schema**: `payload` (JSONB) ahora NULLable. La migración v0.27
  insertaba `'null'::jsonb` como placeholder cuando había cifrado; ahora
  va NULL real. Migración idempotente (ALTER COLUMN DROP NOT NULL).
- **`tenant.Resolver.Patch`** usa `strings.Join` en vez del helper local
  `joinComma` que reinventaba la rueda. Sin cambio funcional.

### Fixed

- **`docs/deployment/env-vars.md`**: faltaba `OUTBOX_ENCRYPTION_KEY`
  (introducido en v0.27.0). Default de `QRSGEN_VERSION` actualizado
  (era `0.23.0-rc1` desde hace meses).
- **`docs/security/outbox-encryption.md`**: la nota sobre KEK/DEKs
  decía "considerado para v0.28+" pero v0.28 ya salió sin eso;
  cambiado a "futuras versiones".

## [0.28.0] - 2026-05-27

Spamguard persistence: cierra la known limitation de v0.21.0 sobre
contadores in-memory que se perdían en restarts.

### Added

- **`SpamguardTracker` persistido en DB** (vía `SetPool` + `Warmup`).
  Nuevas tablas `bridge_spamguard_recent` (last-2 hashes per
  instance+jid_user) y `bridge_spamguard_counter` (counter acumulado
  per instance). El bloqueo dup sobrevive a restarts: un agente que
  intente enviar el mismo mensaje justo después de un deploy sigue
  siendo bloqueado.
- **Cleanup cron** (cada 30 min) que purga `bridge_spamguard_recent`
  con `updated_at > 1h`. La ventana de relevancia del spamguard es
  corta — más viejo que 1h sin actividad ya no es spam realista.

### Changed

- `SpamguardTracker.CheckAndRecord` ahora hace best-effort UPSERT
  tras cada decisión (in-memory cambia primero; DB después). Hot path
  tolera fallos DB sin bloquear el flow.

### Known limitations resueltas

- ✅ v0.21.0: "Spamguard counter in-memory: se resetea en cada
  restart" — hecho en v0.28.0.

## [0.27.0] - 2026-05-27

Outbox encryption at-rest: cierra una known limitation pendiente desde
v0.23.0. Opt-in vía env, backward compatible.

### Added

- **AES-256-GCM encryption** opcional para payloads en
  `bridge_outgoing_queue`. Activar con `OUTBOX_ENCRYPTION_KEY` (32
  bytes en base64). Si vacío → no cifra (compat). Las filas cifradas
  llevan `nonce IS NOT NULL`; las legacy `nonce IS NULL` se entregan
  en claro (backward compat durante migración).
- Nuevo helper `internal/outbox/crypto.go` con `sealPayload` /
  `openPayload` + `DecodeEncryptionKey` que valida tamaño.
- Schema migration idempotente: añade `payload_enc BYTEA` y `nonce
  BYTEA` a `bridge_outgoing_queue`.
- Doc `docs/security/outbox-encryption.md` con setup, rotación y qué
  vector cubre (DBA compromise / dump de Postgres).

### Changed

- `outbox.Outbox` añade `SetEncryptionKey([]byte)` setter. Si la key
  está set, `Enqueue` cifra y persiste en `payload_enc + nonce`;
  `DrainOnce` / `ExpireOnce` descifran al leer.

### Known limitations (v0.27.0)

- Rotación segura requiere drenar el outbox antes de cambiar la key.
  Rotación dual-key sin pausa queda para futuras versiones.
- Cifrado per-tenant (KEK + DEKs) queda para v0.28+.

## [0.26.0] - 2026-05-27

Tenant config completeness: `PATCH` parcial + HMAC per-tenant para
aislar credenciales del webhook entrante entre clientes en multi-
downstream.

### Added

- **`PATCH /api/tenants/:owner_tag`**: update parcial. Solo modifica
  los campos del body — el resto se preserva. Útil para rotar
  `webhook_hmac_secret` sin reenviar el `downstream_api_token`. Audit
  log registra `tenant.patch` con `metadata.fields` (nombres, no
  valores). Mismas keys whitelisteadas que en PUT.
- **`bridge_tenant.webhook_hmac_secret`** (nueva columna, migración
  idempotente). HMAC secret per-tenant write-only — nunca se devuelve
  en GET (igual que `downstream_api_token`).
- **HMAC verify per-tenant**: el middleware del webhook entrante
  resuelve el secret efectivo de la instancia: si su `owner_tag` tiene
  tenant con `webhook_hmac_secret` set → usa ese; sino → fallback al
  `WEBHOOK_HMAC_SECRET` global del env; sino → bypass (compat).

### Changed

- `PUT /api/tenants/:owner_tag` ahora acepta `webhook_hmac_secret`.
  Semántica de **replace**: campos no enviados se vacían (incluido el
  secret). Para preservar selectivamente, usa PATCH.
- `tenant.Resolver` añade `Patch(ctx, ownerTag, fields)` con
  whitelist de campos. Get/List/Warmup/Set actualizados para leer y
  escribir el nuevo campo (`COALESCE(webhook_hmac_secret, '')`).

## [0.25.0] - 2026-05-27

Observabilidad per-tenant: cuando un proceso qrsgen sirve varios
clientes vía `owner_tag`, ahora puedes separar métricas y entradas de
audit por tenant sin tener que cruzar contra DB.

### Added

- **Label `owner_tag`** en los counters Prometheus per-instance:
  `qrsgen_messages_total`, `qrsgen_spamguard_blocks_total`,
  `qrsgen_lifecycle_events_total`, `qrsgen_message_dispatch_errors_total`.
  Vacío para instancias sin tenant configurado. Las queries existentes
  sin filtrar por `owner_tag` siguen funcionando (Prometheus agrega).
- **`Router.OwnerTagFor(ctx, instance)`** nuevo método en la interfaz —
  `*Client` devuelve `""` (single-downstream), `*Registry` resuelve
  desde el cache TTL existente. Permite a callsites de métricas
  etiquetar sin nuevos lookups DB.
- **`GET /api/audit?owner_tag=tenant-acme`** — filtro nuevo en el
  endpoint de audit. Subquery sobre `bridge_instance.owner_tag`. Si se
  combina con `?instance=X` el AND es lógico. Sin filtro = todo.
  Permite que un admin de tenant solo vea sus entradas sin acceso al
  resto del audit log.

### Changed

- `audit.Logger.Query` mantiene firma original; nueva `QueryFiltered`
  acepta el segundo filtro opcional. `Query` delega en `QueryFiltered`
  con `ownerTag=""` para backward compat.
- Manager expone `SetOwnerTagResolver(r)` para inyectar el `Registry`
  (o cualquier impl de la interface). Sin él, el label sale como `""`.

## [0.24.2] - 2026-05-27

Bug fix de "🎉 espurio" + robustez en `backend_started` + cache 30s
del endpoint público + timestamps en tenant API.

### Fixed

- **`connected` espurio tras EventConnected duplicado de whatsmeow**:
  el dispatcher ahora trackea `connectedEmitted` per-instance. Mientras
  la sesión esté viva, EventConnected adicionales (re-handshake silent,
  session renewal sin Disconnect intermedio) **no re-emiten** el evento
  `connected` (que en n8n se renderiza como "Conexión establecida 🎉").
  El flag se limpia en `disconnect` / `logged_out`, dejando que la
  próxima sesión emita normalmente. Caso reportado: SAT-MARC mostrando
  🎉 a las 5:17 AM sin desconexión previa visible.

### Changed

- **`BroadcastBackendStarted` ahora espera a `state=ready`** (timeout
  20s) por instancia antes de reportar `connected: true`. Sustituye al
  snapshot fijo de `IsConnected()` a los 8s post-boot, que daba falsos
  negativos cuando WhatsApp negociaba handshake lento. El pre-sleep de
  8s en `cmd/server` queda eliminado (la espera vive en el broadcast).
- **`/api/public/stats` cacheado in-memory 30s**: la landing hace
  polling cada 10s; sin cache hacíamos 5 SELECTs por request. Ahorra
  ~95% de hits a DB sin sacrificar frescura.
- **`tenant.Resolver.{List,Get,Warmup,Set}` devuelven `created_at` y
  `updated_at`**: visibles vía `/api/tenants*` para UIs de admin. El
  `PUT` los recibe del `RETURNING` del upsert y los persiste en cache.

## [0.24.1] - 2026-05-27

Fix de `version` hardcodeada y simetría con multi-downstream en el
endpoint público.

### Added

- **`tenants_total`** en `/api/public/stats` — cuenta filas en
  `bridge_tenant`. Completa la familia `instances_total` /
  `installations_total` / `tenants_total`.

### Fixed

- **`/api/health` y `/api/public/stats` ahora reportan la versión real
  de cada release**. Antes devolvían `"0.23.0"` hardcoded sin importar
  el tag. Ahora la inyectamos en build via
  `-X main.version={{.Version}}` (GoReleaser). Builds locales reportan
  `"dev"`.

## [0.24.0] - 2026-05-27

Multi-downstream real: un solo proceso puede servir varios clientes con
config downstream propia por `owner_tag`, manteniendo backward compat
total con el fallback `DOWNSTREAM_*` del env.

### Added

- **Multi-downstream real** (`internal/tenant` + `internal/downstream/registry.go`):
  un proceso qrsgen puede servir varios downstreams distintos, enrutados
  por `bridge_instance.owner_tag` vía la nueva tabla `bridge_tenant`
  (owner_tag PK + downstream URL/token/account/inbox). Cache in-memory per
  tenant con invalidación en cada upsert/delete; instance→owner_tag con
  TTL 30s. Si una instancia no tiene `owner_tag`, o no hay tenant para él,
  cae al fallback global (`DOWNSTREAM_*` del env) — totalmente backward
  compatible.
- **Endpoints `/api/tenants/*`**: `GET` (list/detail sin tokens),
  `PUT /:owner_tag` (upsert con `downstream_api_token` solo de escritura),
  `DELETE /:owner_tag` (instancias caen al fallback global). Cada cambio
  invalida el cache del `*Client` por tenant.

### Changed

- `bridge.Incoming` y `bridge.Outgoing` dependen del nuevo interface
  `downstream.Router` en lugar de `*downstream.Client`. `*Client` lo
  implementa devolviéndose a sí mismo, así el callsite single-downstream
  no necesita ningún cambio.
- `resolveInbox` (incoming flow) ahora prioriza
  `bridge_tenant.downstream_inbox_id` antes que el per-instance y el env.

## [0.23.0] - 2026-05-26

Robustez producción + capa de monetización ligera + cero pérdida en
restarts + telemetría pública opt-in + 64 sub-páginas docs.

### Added (post rc1)

- **Lifecycle webhook retry exponencial** para eventos críticos
  (`strike`, `ban_risk`, `outgoing_expired`, `logged_out`,
  `spam_blocked`, `backend_restarting`). 3 reintentos async con
  backoff 5s → 30s → 5min. Métrica
  `qrsgen_lifecycle_webhook_retries_total{event,outcome}` para
  alerting (`outcome=exhausted` → el downstream lleva ≥5min caído).
- **Health endpoint enriquecido**: `/api/health` ahora hace DB ping
  (timeout 2s), reporta `outbox_pending`, `uptime_seconds`,
  `checks.db.{ok, latency_ms}`. Devuelve `503` con
  `status: "degraded"` si DB no responde → Docker HEALTHCHECK falla
  → Swarm restart automático.
- **`installations_total`** en `/api/public/stats` (simetría con
  `instances_total`). Cuenta DISTINCT instances en el audit log
  → sobrevive a DELETE.
- **`instance.paired` registrado en audit log**: ahora cada pairing
  exitoso queda como entrada en `bridge_audit_log`. La métrica
  `qrs_scanned_total` del endpoint público lo cuenta.
- **Cards live en la landing** (`docs/home/status.md`): 6 cards
  conectadas a `/api/public/stats` con polling 10s y toggle
  on/off persistido en localStorage. Fetch con AbortController
  (timeout 3s) para no bloquear UI si el endpoint público está
  caído.
- **Documentación masivamente ampliada**:
  - 64 sub-páginas en sidebar Material (estructura por dominio).
  - Sección **Migrations** con 7 páginas (Evolution / wajs / Baileys
    / SaaS overview / Whapi.cloud / MaytAPI / Salir de qrsgen).
    Schemas de origen detallados en cada una.
  - Sección **Integrations** con n8n + Python (cliente httpx
    + FastAPI receiver).
  - Glosario al final de cada sub-página (>50 términos técnicos
    explicados consistentemente).
- **`tools/migrate/`**: `bulk-provision.py`, `validate.py`,
  `export-config.py` (Python httpx) para automatizar migraciones
  desde plataformas existentes.

### Added (en rc1, inalterado)

- **Outbox persistido** (`internal/outbox` + tabla `bridge_outgoing_queue`).
  El endpoint `POST /api/instances/:name/webhook` ahora encola el payload
  cuando la instancia no está conectada y devuelve `202 {status:"queued"}`.
  Un drainer reentrega cada 5s; mensajes sin entregar a los 5 min expiran
  y emiten el evento lifecycle `outgoing_expired`. Per-instance backlog
  hard-cap (200) + retry budget (5 attempts) + audit hooks.

### Added

- **BanWatcher** (`internal/banwatch` + `GET /api/instances/:name/ban-risk`):
  detector proactivo con tres señales (velocity / diversity / delivery
  ratio) sobre ventanas rolling, score 0-1 + nivel ok|low|moderate|high.
  Emite el evento lifecycle `ban_risk` en rising edge para que el
  integrador reduzca el ritmo.
- **Usage tracking persistido** (`internal/usage` + `bridge_usage_daily`):
  contadores diarios in/out + spamguard + lifecycle, flush a Postgres
  cada 60s. Endpoints `/api/instances/:name/usage` (por instancia),
  `/api/usage` (todas) y `/api/usage/summary` (agregado mensual por
  `owner_tag` — el campo principal para billing).
- **Audit log inmutable** (`internal/audit` + `bridge_audit_log`). Tabla
  append-only con triggers plpgsql que rechazan `UPDATE` y `DELETE`.
  Hooks en provision / patch / delete / boot / outbox.{enqueue,expire,failed}.
  Endpoint `GET /api/audit?instance=&limit=`.
- **`owner_tag` en `bridge_instance`**: string libre que el integrador
  usa para correlacionar instancias con su modelo de tenants. qrsgen no
  lo interpreta. Aceptado en `POST /api/instances` y `PATCH …`, devuelto
  en `GET …`, agrupado en `/api/usage/summary`.
- **HMAC opcional** del webhook entrante (`WEBHOOK_HMAC_SECRET`). Cuando
  está set, exige `X-Qrsgen-Signature: sha256=<hex>` calculado como
  HMAC-SHA256(secret, raw body). Mismatches devuelven 401. Cuando está
  vacío, comportamiento previo (backward compat).
- **HEALTHCHECK** en `Dockerfile` y `Dockerfile.release` vía flag
  `-healthcheck` del propio binario (distroless friendly, no necesita
  curl). Interval 30s, timeout 5s, start-period 20s.
- **Backups Postgres** automatizados: script `ops/backup/postgres-backup.sh`
  + systemd timer (`qrsgen-postgres-backup.timer`) — daily 03:00, retention
  7 daily + 4 weekly, restore runbook en `ops/backup/README.md`.
- **Read-only rootfs** del container + tmpfs `/tmp` 64 MB en compose. El
  binario no escribe a disco; cualquier mutación del fs es signal de
  compromiso.
- **Lifecycle suavizado** para evitar paneles ruidosos:
  - 60s de silencio antes de emitir `unreachable` (blip silencioso si
    reconecta en ese rango).
  - 5s de estabilidad antes de emitir `reconnected`.
- Tests: `internal/config` (100%), `internal/downstream` (79%),
  `internal/banwatch` (95%), `internal/usage`, `internal/audit`
  (integration gated por `INTEGRATION_PG_DSN`). Tests HMAC dedicados.
- Documentación: API completa para todos los endpoints nuevos,
  arquitectura reescrita reflejando outbox / banwatch / audit / usage,
  sección hardening en security.md.

### Changed

- Migración a APIs no-deprecadas de whatsmeow: `DownloadAny` ahora rutea
  por `client.Download` con `DownloadableMessage`; `simpleTextMessage`
  devuelve `*waE2E.Message` (drop `binary/proto`). `SA1019` re-activado en
  golangci-lint.
- `errorlint` reactivado: 5 comparaciones `err == sentinel` migradas a
  `errors.Is`.
- Naming genérico en docs y ejemplos: `SAT-XXX` → `whatsapp-main`,
  `SAT-ALBERT` → `whatsapp-sales`, `<INSTANCE_NAME>` como placeholder.
- `update_config.order: stop-first` en compose (previene race de dos
  containers compitiendo por el mismo JID WhatsApp durante deploy — a
  cambio de ~15s downtime; el outbox lo cubre).

### Security

- HMAC verify opcional del webhook entrante.
- Read-only rootfs + tmpfs `/tmp` 64 MB.
- Audit log inmutable a nivel DB.
- Dependencias actualizadas: `github.com/jackc/pgx/v5` 5.7.1→5.9.2 (cierra
  GHSA-9jj7-4m8r-rfcm critical + GHSA-j88v-2chj-qfwx low),
  `filippo.io/edwards25519` 1.1.0→1.1.1 (GHSA-fw7p-63qq-7hpr),
  `github.com/caarlos0/env/v11` 11.2.2→11.4.1, Echo v4 (CVE
  GO-2025-3553 en `golang-jwt/jwt v3` transitivo cerrado).
- Workflows actualizados: `actions/setup-go@v6`, `docker/login-action@v4`,
  `goreleaser/goreleaser-action@v7`, golang docker 1.25 → 1.26.

### Known limitations

- Outbox payloads sin cifrado en disco — para multi-tenant serio
  se debería cifrar por tenant.
- Single downstream por proceso aún — usar `owner_tag` + un proceso
  por downstream como workaround.
- BanWatcher per-process (no comparte estado entre réplicas — qrsgen
  corre `replicas: 1` por diseño así que no es problema en producción
  típica).

## [0.21.0] - 2026-05-22

Primera release pública.

### Added

- Multi-instance orchestrator: un binario gestiona N sesiones WhatsApp.
- Lifecycle events: `qr_generated`, `paired`, `connected`, `reconnected`, `unreachable`, `disconnected`, `logged_out`, `strike`, `spam_blocked`, `backend_restarting`, `backend_started`.
- Bridge incoming/outgoing con formato `Channel::Api`-compatible.
- Spamguard: tracker in-memory de últimos 2 mensajes por (instance, jid) — bloquea duplicados back-to-back.
- LID/PN twin dedup con ventana configurable (default 10s).
- Bearer auth (`QRSGEN_API_TOKEN`) protege endpoints administrativos. `/api/instances/:name/webhook` exento.
- Egress firewall script + watcher systemd: allowlist Meta CIDRs + LAN, DROP el resto.
- Métricas Prometheus en `/metrics`: messages_total, spamguard_blocks_total, lifecycle_events_total, message_dispatch_errors_total, active_instances, total_instances.
- Healthcheck `/api/health` (sin auth) para liveness/readiness probes.
- Idempotencia de outgoing por `chatwoot_msg_id` (vía `Deduper.SeenIncomingMsg`).
- Stack Docker Compose portable con env vars + ejemplo `.env.example`.
- Documentación: architecture, api, deployment, security, operations, n8n-example.
- 12 unit tests en `internal/bridge` (spamguard tracker, normalize, hashContent).

### Security

- Auth Bearer en API.
- Egress filtering vía iptables.
- Distroless container image.

### Known limitations

- Multi-tenant aún no soportado: un proceso qrsgen sirve un solo downstream.
- Spamguard counter in-memory: se resetea en cada restart.
- LID twin del cliente: dedup limpia downstream pero el destinatario sigue recibiendo 2 msgs si WhatsApp hace dispatch dual.

[Unreleased]: https://github.com/rricajos/qrsgen/compare/v0.28.5...HEAD
[0.28.5]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.5
[0.28.4]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.4
[0.28.3]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.3
[0.28.2]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.2
[0.28.1]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.1
[0.28.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.28.0
[0.27.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.27.0
[0.26.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.26.0
[0.25.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.25.0
[0.24.2]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.2
[0.24.1]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.1
[0.24.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.24.0
[0.23.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.23.0
[0.21.0]: https://github.com/rricajos/qrsgen/releases/tag/v0.21.0
