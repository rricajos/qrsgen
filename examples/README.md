# Examples

Clientes de ejemplo para integrar qrsgen desde distintos stacks.

| Carpeta | Stack | Qué cubre |
|---|---|---|
| [`curl/`](curl/) | bash + curl | Provisioning, listado, fetch QR, send |
| [`python/`](python/) | Python 3 | Cliente HTTP + procesamiento de lifecycle webhook |
| [`node/`](node/) | Node.js 20 | Cliente HTTP + servidor de webhook receiver |
| [`n8n/`](../n8n/) | n8n workflows | Workflows JSON exportados — importables en cualquier n8n |

Todos asumen `QRSGEN_URL` (default `http://qrsgen:3100`) y `QRSGEN_TOKEN` (Bearer).
