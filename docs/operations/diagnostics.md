# Diagnóstico rápido

## "¿Está vivo qrsgen?"

```bash
docker service ps qrsgen_qrsgen
curl -sS http://qrsgen:3100/api/health | jq   # desde la overlay
```

Si el container no está running pero tampoco crashea:

```bash
docker service logs qrsgen_qrsgen --tail 50 | grep -E "ERROR|panic|FATAL"
```

## "¿Cuántas instancias están conectadas?"

```bash
curl -sS -H "Authorization: Bearer $TOK" http://qrsgen:3100/api/instances \
  | jq -r '.[] | [.name, .state, .jid // ""] | @tsv'
```

## "¿Una instancia específica está OK?"

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main | jq
```

Campos clave: `state` (`ready` = OK), `last_event_at` (¿hace cuánto que
no pasa nada?), `owner_tag` (tenant).

## "¿Cuántos mensajes han pasado hoy?"

Vía métricas:

```bash
curl -sS http://qrsgen:3100/metrics | grep qrsgen_messages_total
```

O vía usage tracking:

```bash
TODAY=$(date -u +%Y-%m-%d)
curl -sS -H "Authorization: Bearer $TOK" \
  "http://qrsgen:3100/api/usage?from=$TODAY&to=$TODAY" | jq
```

## "¿Está bloqueando spam?"

```bash
curl -sS http://qrsgen:3100/metrics | grep qrsgen_spamguard_blocks_total
```

O en el snapshot de instancia, ver el campo `spamguard_blocks`.

## "¿Hay mensajes encolados sin entregar?"

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/outbox | jq
```

Campos: `pending` (los que aún se intentarán entregar), `sent`,
`expired`, `failed`. Si `pending > 0` durante mucho rato, la instancia
probablemente está disconnected.

## "¿Estamos cerca de un ban?"

```bash
curl -sS -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances/whatsapp-main/ban-risk | jq
```

Mira `level` (`ok` / `low` / `moderate` / `high`) y `alerts`. Si hay
`alerts: ["high_velocity"]` o similar, **reduce ritmo de envíos**.

## Glosario

**Liveness probe**: comprobación de que el proceso está vivo y responde.
Para qrsgen es `/api/health`.

**State**: estado actual de una instancia. Valores: `qr_pending`,
`paired`, `ready` / `connected`, `connecting`, `disconnected`,
`logged_out`.

**last_event_at**: timestamp de la última transición lifecycle de la
instancia. Útil para saber si lleva mucho rato silenciosa.

**Snapshot live**: foto del estado actual del sistema (instances
conectadas en este momento). Distinto de los counters históricos
acumulados.

**Counter histórico**: contador que solo crece (mensajes totales,
spamguard blocks acumulados). Para tasas se calcula derivada
(`rate()`).

**Ban-risk score**: agregado 0-1 de las tres señales del BanWatcher
(velocity / diversity / delivery_ratio). Más alto = más cerca del
ban.
