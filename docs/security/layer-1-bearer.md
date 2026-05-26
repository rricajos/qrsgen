# Capa 1 — Bearer auth de la API

## Qué hace

Todos los endpoints `/api/*` exigen
`Authorization: Bearer $QRSGEN_API_TOKEN`. Excepciones:

- `GET /api/health` — para liveness/readiness probes.
- `POST /api/instances/:name/webhook` — el downstream rara vez manda
  headers arbitrarios; usa HMAC en otro header ([capa 2](layer-2-hmac.md)).
- `GET /metrics` — Prometheus scrape.
- `GET /static/*` — assets públicos.

## Configuración

```yaml
environment:
  QRSGEN_API_TOKEN: "${QRSGEN_API_TOKEN}"
```

Generación del token (32 bytes URL-safe):

```bash
python3 -c "import secrets;print(secrets.token_urlsafe(32))"
```

Si la variable está vacía, qrsgen arranca con auth desactivada (modo dev)
y emite un WARNING al boot.

## Qué mitiga

Vector #1 (compromiso lateral): aunque un container del overlay resuelva
`qrsgen:3100`, sin token no puede crear/borrar instancias, leer QRs, ni
consultar audit / usage.

## Cómo verificarla

```bash
# Sin token → 401
curl -sS -o /dev/null -w "%{http_code}\n" http://qrsgen:3100/api/instances
# 401

# Con token correcto → 200
curl -sS -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $TOK" \
  http://qrsgen:3100/api/instances
# 200
```
