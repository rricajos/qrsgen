# Examples

Recetas de integración con qrsgen desde distintos stacks y casos de
uso. Cada carpeta es self-contained — `README.md` propio explicando
qué hace y cómo arrancarlo.

| Carpeta | Stack | Qué cubre |
|---|---|---|
| [`curl/`](curl/) | bash + curl | Provisioning end-to-end con scripts. El más simple para entender la API a pelo. |
| [`python/`](python/) | Python 3 (httpx + FastAPI) | Cliente HTTP + servidor de webhook receiver en ~180 líneas. |
| [`node/`](node/) | Node.js 20 ESM (sin deps) | Cliente HTTP + receiver con `http` nativo. |
| [`n8n-basic/`](n8n-basic/) | n8n workflows | 3 workflows JSON importables: lifecycle notifier, healthcheck cron, comando `/qrv2` para regenerar QR. |
| [`grafana-dashboard/`](grafana-dashboard/) | Grafana + Prometheus | `dashboard.json` con 10 paneles (incluido split per-tenant + version info). |
| [`multi-tenant-saas/`](multi-tenant-saas/) | Receta completa | Multi-tenant SaaS: provisión bulk, HMAC per-tenant, observabilidad por cliente, billing. |

Todos los ejemplos asumen `QRSGEN_URL` (default `http://qrsgen:3100`)
y `QRSGEN_TOKEN` (Bearer) configurados en el entorno.

## Por dónde empezar

- **¿Solo quiero ver la API?** → [`curl/`](curl/)
- **¿Estoy integrando desde un orquestador?** →
  [`n8n-basic/`](n8n-basic/) o [`python/`](python/) o [`node/`](node/)
- **¿Estoy montando un SaaS multi-cliente?** →
  [`multi-tenant-saas/`](multi-tenant-saas/)
- **¿Necesito visibilidad operacional?** →
  [`grafana-dashboard/`](grafana-dashboard/)
