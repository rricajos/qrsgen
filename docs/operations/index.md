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

## Glosario

**Runbook**: documento operativo con procedimientos paso a paso para
las tareas y situaciones más frecuentes en producción.

**Diagnóstico rápido**: serie de comprobaciones para entender el
estado del sistema en menos de 1 minuto.

**Forensics**: investigación post-incidente para reconstruir qué pasó,
quién lo hizo y qué metadata acompañaba.

**Troubleshooting**: proceso de identificar y resolver problemas
concretos (códigos de error inesperados, comportamientos anómalos).

**Alerting**: configuración que notifica automáticamente cuando un
sistema cruza ciertos umbrales (instancias caídas, alta tasa de
errores, strikes).

**Procedure**: secuencia de pasos para una operación común (re-pareado,
borrado, restart, billing report).
