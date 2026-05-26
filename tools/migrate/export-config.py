#!/usr/bin/env python3
"""
Exporta la config completa de instancias en qrsgen a JSON.

Usage:
    QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN=$TOK \\
        python3 export-config.py > qrsgen-export-$(date +%F).json

El JSON resultante tiene el mismo shape que un "plan" consumible por
bulk-provision.py — útil para:

- Backup adicional (no reemplaza al pg_dump pero da read-only legible).
- Migración entre VPS: extraes en uno, aplicas en otro con
  bulk-provision.py.
- Migración out: lo usas como input para tu nueva plataforma.

NOTA: las sesiones WhatsApp NO se exportan aquí (no son portables).
Solo configuración: name, events_webhook_url, inbox_id, owner_tag.
"""

import json
import os
import sys

try:
    import httpx
except ImportError:
    sys.exit("Falta httpx. Instala con: pip install httpx")


def main():
    base = os.environ.get("QRSGEN_URL")
    token = os.environ.get("QRSGEN_TOKEN")
    if not base or not token:
        sys.exit("QRSGEN_URL y QRSGEN_TOKEN env vars son requeridas")

    client = httpx.Client(
        base_url=base,
        headers={"Authorization": f"Bearer {token}"},
        timeout=15.0,
    )

    instances = client.get("/api/instances").json()
    plan = {"exported_from": base, "instances": []}

    for inst in instances:
        name = inst["name"]
        try:
            detail = client.get(f"/api/instances/{name}").json()
        except Exception as e:
            print(f"WARN: no se pudo leer detalle de {name}: {e}", file=sys.stderr)
            continue

        entry = {"name": name}
        # events_webhook_url no está en el GET rich actualmente; lo extraemos
        # de DB si lo necesitas — para portabilidad rápida basta con name +
        # owner_tag + inbox_id si los hay.
        if detail.get("owner_tag"):
            entry["owner_tag"] = detail["owner_tag"]
        # inbox_id no aparece en el JSON del status — leer aparte si hace
        # falta. Lo dejamos como None y el integrador lo añade manual.
        plan["instances"].append(entry)

    print(json.dumps(plan, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
