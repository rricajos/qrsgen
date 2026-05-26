#!/usr/bin/env python3
"""
Bulk-provision instancias en qrsgen desde un plan JSON.

Usage:
    QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN=$TOK \\
        python3 bulk-provision.py plan.json [--dry-run]

Plan JSON:
    {
      "instances": [
        {
          "name": "whatsapp-main",
          "events_webhook_url": "https://...",
          "inbox_id": 90,
          "owner_tag": "tenant-acme"
        }
      ]
    }

Notas:
- POST /api/instances es idempotente. Reusa la instancia si ya existe.
- El campo "name" es el único required. Los demás se omiten si no se
  ponen en el plan.
- Tras la ejecución, las instancias quedan en state="qr_pending" y los
  usuarios tienen que escanear cada QR manualmente (limitación
  WhatsApp).
"""

import json
import os
import sys

try:
    import httpx
except ImportError:
    sys.exit("Falta httpx. Instala con: pip install httpx")


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)

    plan_path = sys.argv[1]
    dry_run = "--dry-run" in sys.argv

    base = os.environ.get("QRSGEN_URL")
    token = os.environ.get("QRSGEN_TOKEN")
    if not base or not token:
        sys.exit("QRSGEN_URL y QRSGEN_TOKEN env vars son requeridas")

    with open(plan_path) as f:
        plan = json.load(f)

    instances = plan.get("instances", [])
    if not instances:
        sys.exit(f"Plan {plan_path}: no encontré ninguna 'instances'")

    print(f"Plan: {len(instances)} instancias")
    if dry_run:
        print("--- DRY RUN ---")
        for inst in instances:
            print(f"  POST /api/instances <- {inst.get('name')}")
        return

    client = httpx.Client(
        base_url=base,
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        timeout=15.0,
    )

    ok, err = 0, 0
    for inst in instances:
        name = inst.get("name")
        if not name:
            print(f"  ✗ saltada (sin name): {inst}")
            err += 1
            continue

        body = {k: v for k, v in inst.items() if v is not None}

        try:
            r = client.post("/api/instances", json=body)
            r.raise_for_status()
            state = r.json().get("state", "?")
            print(f"  ✓ {name:<30} -> {state}")
            ok += 1
        except httpx.HTTPStatusError as e:
            print(f"  ✗ {name:<30} HTTP {e.response.status_code}: {e.response.text[:80]}")
            err += 1
        except Exception as e:
            print(f"  ✗ {name:<30} {e}")
            err += 1

    print(f"\nResultado: {ok} ok, {err} errores")
    sys.exit(0 if err == 0 else 1)


if __name__ == "__main__":
    main()
