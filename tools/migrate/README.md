# Migration tools

Scripts Python para automatizar partes de migraciones desde/hacia qrsgen.

Requisitos:

```bash
pip install httpx
```

## Variables de entorno

Todos los scripts esperan:

```bash
export QRSGEN_URL="http://qrsgen:3100"
export QRSGEN_TOKEN="$QRSGEN_API_TOKEN"
```

## Scripts

### `bulk-provision.py`

Lee un plan JSON y crea instancias en qrsgen.

```bash
python3 bulk-provision.py plan.json
```

Plan format:

```json
{
  "instances": [
    {
      "name": "whatsapp-main",
      "events_webhook_url": "https://wf.example.com/qrsgen-events",
      "inbox_id": 90,
      "owner_tag": "tenant-acme"
    }
  ]
}
```

### `validate.py`

Comprueba el estado de todas las instancias y reporta cuáles están
`ready` vs `qr_pending` vs `disconnected`. Útil tras un re-pairing
masivo.

```bash
python3 validate.py
```

Salida:
```
✓ whatsapp-main      ready    34650367855
✓ whatsapp-sales     ready    34602253018
✗ whatsapp-legacy    qr_pending
✗ whatsapp-old       disconnected
```

### `export-config.py`

Exporta toda la config de instancias actuales a JSON. Útil para:

- Backup adicional al pg_dump.
- Migración entre VPS.
- Salida de qrsgen hacia otra plataforma.

```bash
python3 export-config.py > qrsgen-export-$(date +%F).json
```

## Casos de uso documentados

Ver [docs/migrations/](https://rricajos.github.io/qrsgen/migrations/) para
las recetas completas por origen (Evolution API, Baileys,
whatsapp-web.js, SaaS).
