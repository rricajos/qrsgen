#!/usr/bin/env python3
"""
Valida el estado de todas las instancias en qrsgen.

Usage:
    QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN=$TOK \\
        python3 validate.py

Salida:
    ✓ whatsapp-main      ready    34650367855
    ✓ whatsapp-sales     ready    34602253018
    ✗ whatsapp-legacy    qr_pending
    ✗ whatsapp-old       disconnected
"""

import os
import sys

try:
    import httpx
except ImportError:
    sys.exit("Falta httpx. Instala con: pip install httpx")


GREEN = "\033[32m"
RED = "\033[31m"
YELLOW = "\033[33m"
RESET = "\033[0m"


def main():
    base = os.environ.get("QRSGEN_URL")
    token = os.environ.get("QRSGEN_TOKEN")
    if not base or not token:
        sys.exit("QRSGEN_URL y QRSGEN_TOKEN env vars son requeridas")

    client = httpx.Client(
        base_url=base,
        headers={"Authorization": f"Bearer {token}"},
        timeout=10.0,
    )

    try:
        instances = client.get("/api/instances").json()
    except Exception as e:
        sys.exit(f"Error listando instancias: {e}")

    if not instances:
        print("(no hay instancias)")
        return

    ready, pending, broken = 0, 0, 0
    for inst in instances:
        name = inst.get("name", "?")
        state = inst.get("state", "?")
        jid = inst.get("jid", "")
        phone = ""
        if "@" in jid:
            phone = jid.split("@")[0].split(":")[0]

        if state in ("ready", "connected"):
            mark, color = "✓", GREEN
            ready += 1
        elif state in ("qr_pending", "paired", "connecting"):
            mark, color = "○", YELLOW
            pending += 1
        else:
            mark, color = "✗", RED
            broken += 1

        print(f"  {color}{mark}{RESET} {name:<30} {state:<15} {phone}")

    total = len(instances)
    print(f"\n{ready}/{total} ready, {pending}/{total} pending, {broken}/{total} broken")
    sys.exit(0 if broken == 0 else 2)


if __name__ == "__main__":
    main()
