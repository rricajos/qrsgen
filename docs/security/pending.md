# Mejoras pendientes

| Mejora | Vector que cubre | Esfuerzo |
|---|---|---|
| Backups off-site (S3/Backblaze) | Pérdida total del VPS | Bajo |
| Audit entries firmadas (HMAC por fila) | Forensics ante DBA compromise | Medio |
| Cert pinning whatsmeow | Vector #3 con CA root compromise | Alto |
| Rate-limiting de `/api/*` | Vector #1 abuso intra-LAN | Bajo |
| eBPF observability (Falco / Tetragon) | Detección de comportamiento anómalo | Alto |
| Multi-downstream con HMAC por tenant | Aislamiento entre clientes (multi-tenant real) | Medio |
