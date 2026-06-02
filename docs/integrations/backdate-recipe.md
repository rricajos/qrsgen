# Recipe: backdate `created_at` desde `external_created_at`

Desde v0.46.0 qrsgen envía `external_created_at` en cada `PostMessage`
del downstream, dentro de `content_attributes`. Muchos downstreams
(Chatwoot entre ellos) ignoran el `created_at` suministrado por
api-tokens normales y siempre asignan `NOW()` en el INSERT. Para que
los mensajes importados aparezcan con su timestamp original en la
UI cronológica del downstream, hace falta una operación post-hoc que
copie `external_created_at` → `created_at`.

**qrsgen NO ejecuta esta operación** (desde v0.64.7) para mantenerse
agnóstico del downstream. Cada integrador la implementa donde
prefiera (cron en su DB, workflow n8n, scheduled job en su backend,
etc.).

## Receta SQL (Chatwoot)

Chatwoot guarda `messages.content_attributes` como columna `json`
(no `jsonb`) y su modelo Ruby aplica `store :content_attributes,
coder: JSON` — Rails serializa el Hash a **un string JSON** antes
de persistirlo. Resultado en disco: una JSON-string dentro de una
columna json, **no** un objeto JSON directo.

Por eso un `(content_attributes::jsonb) ?` directo nunca encuentra
claves (busca en una string, que no tiene claves). El truco es
unwrap con `#>> '{}'` (devuelve el inner text), luego `::jsonb`
parsea el objeto real:

```sql
WITH stale AS (
  SELECT id
  FROM messages
  WHERE ((content_attributes #>> '{}')::jsonb) ? 'external_created_at'
    AND created_at > to_timestamp((((content_attributes #>> '{}')::jsonb)->>'external_created_at')::bigint)
                      + make_interval(secs => 5)
  ORDER BY id DESC
  LIMIT 500
)
UPDATE messages m
SET created_at = to_timestamp((((m.content_attributes #>> '{}')::jsonb)->>'external_created_at')::bigint)
FROM stale
WHERE m.id = stale.id
RETURNING m.id;
```

Notas:

- **`#>> '{}'` unwrap** (v0.65.1+): obligatorio contra Chatwoot real.
  Sin él la query no encuentra mensajes (Rails store-coder JSON los
  guarda string-encoded). Versiones previas de este doc usaban
  `content_attributes::jsonb` directo — ese SQL funcionaba en tests
  sintéticos con `jsonb_build_object` pero NO contra mensajes reales
  insertados vía MessageBuilder.
- **Tolerancia de 5s** (`+ make_interval(secs => 5)`): evita bucles
  infinitos cuando hay micro-jitter entre `created_at` y el
  `external_created_at` rondeado a segundos en pg.
- **LIMIT 500**: batch size razonable. Sube si tienes un backlog
  grande y la DB lo aguanta sin lock contention.
- **Idempotente**: la WHERE filtra rows ya backdated, sucesivos
  ticks no rehacen trabajo.

## Recetas por integrador

### n8n + Chatwoot (Omnia stack)

El integration repo `rricajos/integration-n8n-qrsgen-omnia` incluye
el workflow `OMNIA_BACKDATE.template.json`:

- **Trigger**: cron cada 30s
- **Postgres node**: ejecuta la query de arriba contra la DB de
  Chatwoot (credential `Chatwoot Postgres`).
- **Log node**: imprime `rows updated` solo si > 0.

Despliegue: ver el README del integration repo. Requiere:

1. Crear credential Postgres en n8n apuntando a la DB de Chatwoot
   (host, port, database, user, password).
2. Substituir `${CLIENT_CRED_CHATWOOT_DB_ID}` en el template por el
   ID de esa credential.
3. Subir el workflow al n8n del cliente y activarlo.

### Cron + psql (sin n8n)

Si tu stack no usa n8n:

```bash
#!/bin/bash
# /usr/local/bin/qrsgen-backdate-tick.sh
# Cron entry: * * * * * /usr/local/bin/qrsgen-backdate-tick.sh >/dev/null 2>&1

PGPASSWORD="$CHATWOOT_DB_PASSWORD" psql -h pgvector -U postgres -d chatwoot -c "
WITH stale AS (
  SELECT id FROM messages
  WHERE ((content_attributes #>> '{}')::jsonb) ? 'external_created_at'
    AND created_at > to_timestamp((((content_attributes #>> '{}')::jsonb)->>'external_created_at')::bigint)
                    + make_interval(secs => 5)
  ORDER BY id DESC LIMIT 500
)
UPDATE messages m
SET created_at = to_timestamp((((m.content_attributes #>> '{}')::jsonb)->>'external_created_at')::bigint)
FROM stale WHERE m.id = stale.id;
"
```

### Endpoint admin de Chatwoot

Si tienes un super-admin user en Chatwoot, puedes editar el
`MessageBuilder` para respetar el `created_at` POST input. Más
invasivo, no recomendado para versiones standard de Chatwoot.

## Verificación

Inserta un row sintético con `external_created_at` de hace varios
días y verifica que el `created_at` se actualiza:

```sql
-- Pick any msg, override external_created_at to 30 días atrás.
-- Importante: usamos to_json(text)::json para emular el shape
-- string-encoded de Chatwoot real (Rails store coder: JSON sobre
-- columna json). Si usas jsonb_build_object directo, generarás un
-- objeto-encoded y NO reproduce el bug — la query del worker lo
-- encontrará trivialmente y no validas el fix del #>> '{}'.
UPDATE messages
SET content_attributes = to_json(
  json_build_object(
    'external_created_at',
    EXTRACT(EPOCH FROM NOW() - interval '30 days')::bigint
  )::text
)::json
WHERE id = <pick_one>;

-- Espera el siguiente tick (30s si usas el workflow n8n)

-- Verifica que created_at quedó alineado con external_created_at
SELECT id, created_at, to_timestamp((((content_attributes #>> '{}')::jsonb)->>'external_created_at')::bigint) AS ext
FROM messages WHERE id = <pick_one>;
```

## Historia

- **v0.46.0**: qrsgen empieza a emitir `external_created_at` en
  cada POST.
- **v0.54.0**: qrsgen ejecuta el backdate worker internamente vía
  `CHATWOOT_DB_URL` env (acopla qrsgen al schema de Chatwoot).
- **v0.64.7**: el worker se mueve al integration repo. qrsgen queda
  agnóstico del downstream — solo emite la info, cada integrador
  decide cómo materializarla.

---

Última actualización: v0.64.7 (2026-06-02)
