# Python (httpx + FastAPI)

Cliente Python listo para integrar en scripts o servicios propios.
Incluye:

- CLI con sub-comandos para `list / provision / status / qr / send`.
- Listener de webhooks con FastAPI para recibir lifecycle events y
  mensajes incoming.

El código completo vive en
[`examples/python/client.py`](https://github.com/rricajos/qrsgen/blob/main/examples/python/client.py)
del repo. Aquí explico los patrones; el archivo es copy-pasteable.

## Requisitos

```bash
pip install httpx fastapi uvicorn
```

Solo `httpx` si no necesitas el listener de webhooks. FastAPI + uvicorn
si quieres recibir lifecycle events o mensajes incoming.

## Variables de entorno

```bash
export QRSGEN_URL="http://qrsgen:3100"      # default si está en overlay
export QRSGEN_TOKEN="$BEARER_TOKEN"          # genera con secrets.token_urlsafe(32)
```

## Cliente básico

```python
import httpx, os

client = httpx.Client(
    base_url=os.environ["QRSGEN_URL"],
    headers={"Authorization": f"Bearer {os.environ['QRSGEN_TOKEN']}"},
    timeout=10.0,
)

# Listar instancias
print(client.get("/api/instances").json())

# Provisionar nueva
r = client.post("/api/instances", json={
    "name": "whatsapp-main",
    "events_webhook_url": "https://my-app.example.com/qrsgen-events",
    "owner_tag": "tenant-acme",
})
print(r.json())

# Estado rico
print(client.get("/api/instances/whatsapp-main").json())
```

## Descargar QR PNG

```python
r = client.get("/api/instances/whatsapp-main/qr")
if r.status_code == 200:
    with open("/tmp/qr.png", "wb") as f:
        f.write(r.content)
elif r.status_code == 404:
    print("No QR pending — instance already paired or never created")
```

## Enviar mensaje outgoing

```python
# Nota: /webhook NO usa Bearer auth — httpx.post directo sin headers
r = httpx.post(
    f"{os.environ['QRSGEN_URL']}/api/instances/whatsapp-main/webhook",
    json={
        "event": "message_created",
        "message_type": "outgoing",
        "content": "Hola desde Python",
        "conversation": {
            "id": 1,
            "meta": {"sender": {"identifier": "34600000000@s.whatsapp.net"}},
        },
        "id": 42,           # id en TU sistema, para idempotencia
        "private": False,
    },
    timeout=10.0,
)
data = r.json()
if data.get("status") == "queued":
    print(f"Encolado: queue_id={data['queue_id']} expires_at={data['expires_at']}")
else:
    print(f"Enviado: {data}")
```

## HMAC opcional del webhook

Si tu qrsgen tiene `WEBHOOK_HMAC_SECRET` configurado, firma cada body:

```python
import hmac, hashlib, json

def signed_post(url: str, body: dict, secret: str):
    raw = json.dumps(body).encode("utf-8")
    sig = "sha256=" + hmac.new(secret.encode(), raw, hashlib.sha256).hexdigest()
    return httpx.post(url, content=raw, headers={
        "Content-Type": "application/json",
        "X-Qrsgen-Signature": sig,
    }, timeout=10.0)
```

## Webhook receiver con FastAPI

```python
from fastapi import FastAPI, Request

app = FastAPI()

@app.post("/qrsgen-events")
async def on_lifecycle(req: Request):
    ev = await req.json()
    instance = ev["instance"]
    name     = ev["event"]

    if name == "qr_generated":
        # Descargar el PNG del QR y mostrarlo al usuario
        ...
    elif name == "strike":
        print(f"🚨 STRIKE en {instance} — frena el ritmo de envíos")
    elif name == "ban_risk":
        if ev.get("level") == "high":
            print(f"⚠️ Ban-risk HIGH en {instance}: {ev.get('alert')}")
    elif name == "outgoing_expired":
        # Un mensaje en el outbox expiró sin entregarse
        print(f"💀 Expirado msg #{ev.get('queue_id')}: {ev.get('preview')}")
    return {"ok": True}


@app.post("/messages-incoming")
async def on_incoming(req: Request):
    """
    qrsgen postea aquí los mensajes incoming de WhatsApp.
    Configurable vía DOWNSTREAM_BASE_URL en el stack qrsgen.
    """
    msg = await req.json()
    sender  = msg["conversation"]["meta"]["sender"]["identifier"]
    content = msg.get("content", "")
    print(f"INCOMING from {sender}: {content[:100]}")
    return {"ok": True}
```

Run:

```bash
uvicorn webhook_receiver:app --host 0.0.0.0 --port 8000
```

## Helpers útiles

### Esperar a que la instancia llegue a `ready` (long-poll)

```python
def wait_ready(name: str, timeout: int = 120) -> dict:
    r = client.get(f"/api/instances/{name}/wait-ready",
                   params={"timeout": timeout},
                   timeout=timeout + 5)
    r.raise_for_status()
    return r.json()
```

### Reporte mensual de uso (billing)

```python
from datetime import date

def monthly_summary(month: str | None = None) -> list[dict]:
    """month: 'YYYY-MM'. Default: mes actual."""
    if month is None:
        month = date.today().strftime("%Y-%m")
    r = client.get("/api/usage/summary", params={"from": month, "to": month})
    r.raise_for_status()
    return r.json()["rows"]

# Uso:
for row in monthly_summary():
    print(f"{row['owner_tag']:<20} msgs_in={row['messages_in']:>6}  msgs_out={row['messages_out']:>6}")
```

### Snapshot de ban-risk

```python
def ban_risk(name: str) -> dict:
    return client.get(f"/api/instances/{name}/ban-risk").json()

snap = ban_risk("whatsapp-main")
if snap["level"] in ("high", "moderate"):
    print(f"⚠️ {name}: {snap['level']} (alerts: {snap['alerts']})")
```

## Manejo de errores

```python
import httpx

try:
    r = client.post("/api/instances", json={"name": "test"})
    r.raise_for_status()
except httpx.HTTPStatusError as e:
    if e.response.status_code == 401:
        sys.exit("Bearer token inválido o vacío en QRSGEN_TOKEN")
    elif e.response.status_code == 503:
        # outbox full (en el endpoint webhook)
        print("Queue full — instancia desconectada hace mucho")
    else:
        print(f"Error {e.response.status_code}: {e.response.text}")
```

## Glosario

**httpx**: cliente HTTP moderno para Python con soporte sync y async.
Drop-in replacement de `requests` con timeouts saneables.

**FastAPI**: framework Python para crear APIs REST con type hints + auto-docs.
Útil aquí como receptor de webhooks lifecycle.

**uvicorn**: servidor ASGI que ejecuta apps FastAPI en producción.

**Bearer token en httpx.Client**: la opción más limpia es pasarlo en
`headers` del constructor del Client; queda en todas las requests
automáticamente. El endpoint `/webhook` NO usa Bearer auth, así que para
él hay que hacer `httpx.post(...)` directo (sin `client`).

**Idempotencia (campo `id`)**: si tu sistema reintenta una request por
network error, manda el mismo `id` numérico — qrsgen detectará el
duplicado y devolverá 200 sin reenviar.

**Long-poll**: técnica donde el cliente mantiene una request HTTP abierta
y el servidor responde cuando ocurre el evento. `wait-ready` lo
implementa para evitar polling busy-loop.

**Outbox 202**: cuando recibes `{"status": "queued"}` en lugar de
`{"status": "sent"}`, significa que la instancia está disconnected y
qrsgen entregará el mensaje cuando vuelva (TTL 5 min).
