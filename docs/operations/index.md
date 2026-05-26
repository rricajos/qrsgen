# Operations — Runbook

Recetas para operar qrsgen en producción. Asume que tienes Bearer token
en `$TOK` y los nombres de instancia en `whatsapp-main` /
`whatsapp-sales` como placeholders.

## Navegación

- [Diagnóstico rápido](diagnostics.md) — health, instances, mensajes,
  spam, outbox, ban-risk.
- [Procedimientos comunes](procedures.md) — re-pareado, borrado,
  restart, owner_tag, billing report, spamguard toggle.
- [Investigaciones / forensics](forensics.md) — strike investigation,
  mensajes expirados, auditoría de cambios.
- [Troubleshooting](troubleshooting.md) — `202 queued` inesperado,
  outbox full, ban-risk alto, "Error al enviar", reconexiones.
- [Alerting Prometheus](alerting.md) — reglas sugeridas.
- [Logs útiles](logs.md) — qrsgen, firewall, backups.
