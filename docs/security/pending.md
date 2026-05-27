# Mejoras pendientes

| Mejora | Vector que cubre | Esfuerzo |
|---|---|---|
| Backups off-site (S3/Backblaze) | Pérdida total del VPS | Bajo |
| Audit entries firmadas (HMAC por fila) | Forensics ante DBA compromise | Medio |
| Cert pinning whatsmeow | Vector #3 con CA root compromise | Alto |
| Rate-limiting de `/api/*` | Vector #1 abuso intra-LAN | Bajo |
| eBPF observability (Falco / Tetragon) | Detección de comportamiento anómalo | Alto |
| HMAC del webhook entrante per-tenant | Aislamiento de credenciales en multi-downstream | Medio |
| Outbox encryption per-tenant | Payloads en cola no legibles para DBA | Medio |
| Spamguard persistido en DB | Counter sobrevive a restart | Bajo |

Notas: **multi-downstream** (servir varios downstreams desde un proceso)
ya está implementado desde v0.24.0 — ver
[arquitectura](../architecture/multi-instance.md). Lo que sigue pendiente
del modelo multi-tenant "serio" es el aislamiento de credenciales (HMAC
+ outbox encryption) por tenant.

## Glosario

**Off-site**: ubicación física/lógica distinta al servidor primario.
Backup off-site = copia en otro datacenter / cloud / región.

**Audit firmada (HMAC por fila)**: cada entrada del audit log llevaría
una firma criptográfica del actor, evitando que un atacante con DBA
pueda forjar entradas creíbles.

**Cert pinning** (mejora futura): cliente solo confía en certs con un
fingerprint específico, no en la cadena CA. Defensa contra CA root
compromise.

**Rate-limiting (API)**: límite de requests por unidad de tiempo por
cliente. Defensa contra abuso intra-LAN.

**eBPF**: tecnología del kernel Linux para insertar programas seguros
en el path del syscall, permitiendo observabilidad y filtros a nivel
kernel.

**Falco / Tetragon**: herramientas eBPF para detectar comportamiento
anómalo en containers (intentos de syscalls inesperados, escrituras a
paths sensibles).

**Multi-tenant real**: arquitectura donde cada cliente tiene
aislamiento real (DB separada, credentials separados, etc.). Distinto
del multi-tenant ligero por `owner_tag` que qrsgen tiene hoy.
