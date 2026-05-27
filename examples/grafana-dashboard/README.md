# Grafana dashboard

Dashboard JSON con 10 paneles que cubren las métricas `qrsgen_*`
expuestas en `/metrics`. Soporta multi-tenant (label `owner_tag`)
desde v0.25.0 y versión activa (`qrsgen_version_info`) desde v0.28.2.

## Importar

1. Asegúrate de que Prometheus scraping a qrsgen `/metrics` está
   configurado y devuelve métricas. Test manual:
   ```bash
   curl -sf http://qrsgen:3100/metrics | grep ^qrsgen_
   ```
2. En Grafana: **Dashboards → Import** → pegar el contenido de
   `dashboard.json` o subir el archivo.
3. Seleccionar tu datasource Prometheus.

## Paneles

| # | Panel | Métrica base |
|---|---|---|
| 1 | Version | `qrsgen_version_info` |
| 2 | Active instances | `qrsgen_active_instances` |
| 3 | Total instances | `qrsgen_total_instances` |
| 4 | Mensajes/min (out) | `qrsgen_messages_total{direction="out"}` |
| 5 | Mensajes in/out rate | `qrsgen_messages_total` |
| 6 | Mensajes por tenant | `qrsgen_messages_total` con `owner_tag` |
| 7 | Lifecycle events rate | `qrsgen_lifecycle_events_total` |
| 8 | Spamguard blocks por tenant | `qrsgen_spamguard_blocks_total` |
| 9 | Dispatch errors por kind | `qrsgen_message_dispatch_errors_total` |
| 10 | Webhook retries (alerta `outcome=exhausted`) | `qrsgen_lifecycle_webhook_retries_total` |

## Alertas que podrías construir encima

- **`outcome=exhausted` > 0 en 15m** → downstream caído ≥5 min, page on-call.
- **`active_instances < total_instances` sostenido > 5 min** → instancia
  caída sin recuperarse.
- **`dispatch_errors_total{kind="send_text"}` rate > 1/s** → algo
  está sistemáticamente fallando en outgoing.
- **`spamguard_blocks` spike** → un agente o flujo está mandando
  duplicados, revisar configuración.

## Personalizar por tenant

Si tu deploy es multi-tenant, añade una variable Grafana
`$owner_tag` con `query: label_values(qrsgen_messages_total, owner_tag)`
y filtra todos los paneles con `{owner_tag=~"$owner_tag"}`. Así cada
cliente puede ver solo sus números.
