#!/usr/bin/env python3
"""
Cliente Python para qrsgen — provisioning + webhook receiver.

Uso:
    QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN=... python3 client.py

Requiere: `pip install httpx fastapi uvicorn`
"""

import os
import sys
import time

import httpx
from fastapi import FastAPI, Request
import uvicorn

QRSGEN_URL = os.environ.get("QRSGEN_URL", "http://qrsgen:3100")
QRSGEN_TOKEN = os.environ.get("QRSGEN_TOKEN", "")

if not QRSGEN_TOKEN:
    sys.exit("QRSGEN_TOKEN env var required")

client = httpx.Client(
    base_url=QRSGEN_URL,
    headers={"Authorization": f"Bearer {QRSGEN_TOKEN}"},
    timeout=10.0,
)


def list_instances() -> list[dict]:
    return client.get("/api/instances").json()


def provision(name: str, events_webhook_url: str | None = None) -> dict:
    body = {"name": name}
    if events_webhook_url:
        body["events_webhook_url"] = events_webhook_url
    r = client.post("/api/instances", json=body)
    r.raise_for_status()
    return r.json()


def status(name: str) -> dict:
    r = client.get(f"/api/instances/{name}")
    r.raise_for_status()
    return r.json()


def fetch_qr_png(name: str) -> bytes | None:
    r = client.get(f"/api/instances/{name}/qr")
    if r.status_code == 404:
        return None
    r.raise_for_status()
    return r.content


def send_outgoing(name: str, jid: str, content: str, msg_id: int) -> None:
    """
    JID típico: '34600000000@s.whatsapp.net'.
    msg_id: ID que identifica al mensaje en tu sistema (para idempotencia).
    """
    payload = {
        "event": "message_created",
        "message_type": "outgoing",
        "content": content,
        "conversation": {
            "id": 1,
            "meta": {"sender": {"identifier": jid}},
        },
        "id": msg_id,
        "private": False,
    }
    # Endpoint /webhook está exento de Bearer auth
    r = httpx.post(
        f"{QRSGEN_URL}/api/instances/{name}/webhook",
        json=payload,
        timeout=10.0,
    )
    r.raise_for_status()


# ---------------------------------------------------------------------------
# Webhook receiver: levanta un servidor FastAPI que recibe los lifecycle
# events y los procesa.

webhook_app = FastAPI()


@webhook_app.post("/webhook/qrsgen-events")
async def handle_event(req: Request):
    event = await req.json()
    instance = event.get("instance", "?")
    name = event.get("event", "?")
    print(f"[{instance}] event={name} payload={event}")
    # Dispatch por tipo:
    if name == "qr_generated":
        png = fetch_qr_png(instance)
        if png:
            with open(f"/tmp/{instance}_qr.png", "wb") as f:
                f.write(png)
            print(f"  → saved /tmp/{instance}_qr.png")
    elif name == "strike":
        print(f"  🚨 STRIKE en {instance} — frena el ritmo de envíos!")
    elif name == "connected":
        print(f"  ✅ {instance} operativo")
    return {"ok": True}


@webhook_app.post("/webhook/messages-incoming")
async def handle_incoming(req: Request):
    """
    qrsgen postea aquí los mensajes incoming de WhatsApp. Configurable vía
    DOWNSTREAM_BASE_URL en el stack qrsgen.
    """
    msg = await req.json()
    sender = msg.get("conversation", {}).get("meta", {}).get("sender", {})
    content = msg.get("content", "")
    print(f"INCOMING from {sender.get('identifier')}: {content[:100]}")
    return {"ok": True}


# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import argparse

    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)

    sub.add_parser("list")

    s = sub.add_parser("provision")
    s.add_argument("name")
    s.add_argument("--webhook", help="events_webhook_url")

    s = sub.add_parser("status")
    s.add_argument("name")

    s = sub.add_parser("qr")
    s.add_argument("name")
    s.add_argument("--out", default=None, help="output PNG path")

    s = sub.add_parser("send")
    s.add_argument("name")
    s.add_argument("jid")
    s.add_argument("content")
    s.add_argument("--id", type=int, default=None)

    sub.add_parser("listen", help="Run webhook receiver on :8000")

    args = p.parse_args()

    if args.cmd == "list":
        for i in list_instances():
            print(f"  {i['name']:<20} {i['state']:<15} {i.get('jid','')}")
    elif args.cmd == "provision":
        print(provision(args.name, args.webhook))
    elif args.cmd == "status":
        st = status(args.name)
        for k, v in st.items():
            print(f"  {k}: {v}")
    elif args.cmd == "qr":
        png = fetch_qr_png(args.name)
        if not png:
            sys.exit("no QR available")
        path = args.out or f"/tmp/{args.name}_qr.png"
        with open(path, "wb") as f:
            f.write(png)
        print(f"saved {path}")
    elif args.cmd == "send":
        send_outgoing(args.name, args.jid, args.content, args.id or int(time.time()))
        print("sent")
    elif args.cmd == "listen":
        uvicorn.run(webhook_app, host="0.0.0.0", port=8000)
