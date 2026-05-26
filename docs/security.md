# Seguridad

## Modelo de amenaza

qrsgen vive en una overlay docker compartida con el downstream y n8n. Tres vectores principales:

1. **Compromiso lateral en LAN**: si otro container del overlay es vulnerado, ¿qué puede hacer contra qrsgen?
2. **Compromiso del propio qrsgen**: si un atacante gana RCE en qrsgen, ¿qué puede exfiltrar o usar?
3. **MITM en el WebSocket WhatsApp**: ¿puede alguien interceptar el tráfico qrsgen ↔ Meta?

## Capa 1 — Auth API (Bearer token)

Todas las rutas `/api/*` requieren `Authorization: Bearer $QRSGEN_API_TOKEN` excepto:

- `/api/health` (liveness)
- `/api/instances/:name/webhook` (el downstream manda su propia firma HMAC en otro header)

Si `QRSGEN_API_TOKEN` está vacío → auth desactivada (modo dev, log warning emitido).

Configuración en el stack:

```yaml
environment:
  QRSGEN_API_TOKEN: "${QRSGEN_API_TOKEN}"
```

Generación del token:

```bash
python3 -c "import secrets;print(secrets.token_urlsafe(32))"
```

→ Mitiga vector #1: aunque otro container resuelva `qrsgen:3100`, sin token no puede crear/borrar instancias, leer QR, etc.

## Capa 2 — Egress firewall (iptables)

`firewall.sh` mantiene una cadena `QRSGEN_EGRESS` que filtra el outbound del container qrsgen:

**Allowlist**:

- Established/related (TCP return traffic)
- DNS Docker (`127.0.0.11:53`)
- Overlay LAN (`10.0.0.0/8`, `172.16.0.0/12`)
- VPS public IP (para llamadas vía DNS público al downstream)
- Meta/Facebook CIDRs en `:443` (14 rangos AS32934)

**Default**: LOG + DROP.

Comandos:

```bash
sudo /opt/qrsgen-stack/firewall.sh apply    # aplica DROP
sudo /opt/qrsgen-stack/firewall.sh log      # solo LOG (testing)
sudo /opt/qrsgen-stack/firewall.sh flush    # quita todas las reglas
sudo /opt/qrsgen-stack/firewall.sh status   # muestra reglas + counters
```

Watchdog systemd (`qrsgen-firewall.service`) re-aplica automáticamente cuando docker reporta `container start` para qrsgen — necesario porque la IP en `docker_gwbridge` cambia tras restarts.

→ Mitiga vector #2: si qrsgen es comprometido, no puede exfiltrar datos a un servidor C2 arbitrario — solo Meta y la propia LAN están permitidos.

## Capa 3 — TLS de WhatsApp

whatsmeow usa el cliente TCP/TLS estándar de Go. El bundle de CAs de la imagen distroless valida los certs de Meta. **MITM pasivo es imposible** (TLS estricto). MITM activo requeriría:

- Comprometer CA root del VPS (poco probable, requiere root del host)
- Forzar al cliente a aceptar cert arbitrario (whatsmeow no lo permite sin patches)

Sin cert pinning explícito, un atacante con root del VPS podría inyectar un CA root y MITM. Pero si tienes root del VPS comprometido, **el MITM del WebSocket es la menor de tus preocupaciones**.

Mejora futura: cert pinning en whatsmeow para defender ante CA root compromise (alto esfuerzo).

## Capa 4 — Container hardening (read-only rootfs)

El stack arranca qrsgen con `read_only: true` y un tmpfs montado en `/tmp`
(64 MB). El binario no escribe a disco — toda la persistencia vive en
Postgres. Cualquier escritura inesperada al filesystem es un indicio
inmediato de compromiso.

```yaml
read_only: true
volumes:
  - type: tmpfs
    target: /tmp
    tmpfs:
      size: 67108864
```

Combinado con la imagen distroless (sin shell ni paquetes auxiliares) y el
`USER nonroot:nonroot`, la superficie de un RCE queda reducida a:
- No puede instalar herramientas (rootfs read-only, sin gestor de paquetes).
- No puede persistir un implante (sólo /tmp escribible, se vacía con cada
  redeploy, además limitado a 64 MB).
- No puede escalar a root (nonroot user, sin capabilities adicionales).

## Credenciales en n8n

Los workflows n8n usan **n8n Credentials** (no hardcode en JSON):

- `el downstream API (production)` → `httpHeaderAuth` con header `api_access_token`
- `qrsgen API` → `httpHeaderAuth` con header `Authorization: Bearer ...`

Estos credentials se guardan encriptados en la DB de n8n. Para exportar workflows con secretos:

```bash
# n8n CLI export incluye SOLO los IDs de credentials, no los valores
n8n export:workflow --id=<wf_id>
```

→ Mitiga el riesgo de filtrar secretos al hacer screenshot/export de workflows.

## Audit / logs

- qrsgen escribe slog JSON estructurado a stdout → `docker service logs qrsgen_qrsgen`
- Firewall logs → `journalctl -u qrsgen-firewall.service` + `/var/log/qrsgen-firewall.log`
- Paquetes droppeados por iptables → `dmesg | grep QRSGEN-DROP` (rate-limited 5/min)
- Métricas Prometheus → `qrsgen_message_dispatch_errors_total{kind}` para alertas de errores

## Mejoras pendientes (no implementadas)

| Mejora | Impacto | Esfuerzo |
|---|---|---|
| Verificar HMAC del webhook downstream en `/webhook` | Mitiga spoofing del entrypoint | Bajo |
| Rate-limiting en `/api/*` | Mitiga abuso intra-LAN | Bajo |
| Cert pinning whatsmeow | Defensa ante CA compromise | Alto |
| Audit log inmutable | Forensics post-incidente | Medio |
| Read-only filesystem en container | Defensa ante RCE escalation | Bajo |
| eBPF observability (Falco/Tetragon) | Detección de comportamiento anómalo | Alto |
