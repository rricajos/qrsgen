# qrsgen

**WhatsApp ↔ HTTP API bridge en Go.** Mantiene una sesión WhatsApp Web por
instancia (vía [whatsmeow](https://github.com/tulir/whatsmeow)) y la expone
como una API HTTP REST estándar lista para producción.

```
Tu sistema (n8n / CRM / app custom)
    │
    │ HTTP REST + Bearer auth (+ HMAC opcional)
    ▼
qrsgen ────► WebSocket TLS ────► Meta servers
    │
    │ HTTP POST con incoming msgs + lifecycle events
    ▼
Tu webhook endpoint
```

> ⚠️ Antes de usar, lee el [DISCLAIMER](legal/disclaimer.md) — riesgos
> WhatsApp ToS, GDPR, limitación de responsabilidad. No afiliado con
> WhatsApp / Meta.

## Características destacadas

- **Multi-instancia** real en un solo proceso. Cada instancia es un número.
- **Outbox persistido** con TTL de 5 min — cero pérdida en restarts cortos.
- **BanWatcher**: velocity / diversity / delivery_ratio + score + alertas
  proactivas para reducir ritmo antes de que WhatsApp tome medidas.
- **Audit log inmutable** a nivel de DB (triggers que rechazan
  UPDATE/DELETE).
- **Usage tracking** persistido + endpoint `/api/usage/summary` mensual
  por `owner_tag` — listo para facturación multi-tenant ligero.
- **HMAC opcional** del webhook entrante. **Read-only rootfs** del
  container. **Backups Postgres** automatizados con systemd timer.
- **12 eventos de lifecycle** emitidos como webhooks por instancia.

## Por dónde empezar

- **[Arquitectura](architecture/)** — entender los flujos internos, las
  tablas Postgres, cómo se compone el binario.
- **[API](api/)** — endpoints, payloads, lifecycle webhooks, recetas
  curl. **Lee primero el quickstart** si vas a integrar.
- **[Deployment](deployment/)** — stack swarm, variables de entorno,
  portabilidad multi-VPS.
- **[Security](security/)** — siete capas explicadas (Bearer auth,
  HMAC, firewall iptables, TLS, container hardening, audit, backups).
- **[Operations](operations/)** — runbook: diagnóstico rápido,
  procedimientos comunes, troubleshooting, alerting Prometheus.
- **[n8n example](n8n-example.md)** — receta concreta de integración con
  un orquestador externo (extrapolable a Zapier, Make, Temporal, etc.).

## Estado del proyecto

Producción con 4+ instancias activas. Tag estable más reciente en
[releases](https://github.com/rricajos/qrsgen/releases). Cada release
documentada en [CHANGELOG](https://github.com/rricajos/qrsgen/blob/main/CHANGELOG.md).

<div class="qrsgen-stats" id="qrsgen-stats" data-endpoint="https://telemetry.qrsgen.ricajos.dev/api/public/stats" hidden>
  <div class="stat-card">
    <div class="stat-value" id="stat-connected">—</div>
    <div class="stat-label">QRs conectados</div>
  </div>
  <div class="stat-card">
    <div class="stat-value" id="stat-scanned">—</div>
    <div class="stat-label">QRs escaneados</div>
  </div>
  <div class="stat-card">
    <div class="stat-value" id="stat-active">—</div>
    <div class="stat-label">Instalaciones activas</div>
  </div>
  <div class="stat-card">
    <div class="stat-value" id="stat-total">—</div>
    <div class="stat-label">Instalaciones totales</div>
  </div>
  <div class="stat-card">
    <div class="stat-value" id="stat-in">—</div>
    <div class="stat-label">Mensajes recibidos</div>
  </div>
  <div class="stat-card">
    <div class="stat-value" id="stat-out">—</div>
    <div class="stat-label">Mensajes enviados</div>
  </div>
</div>

<p class="telemetry-controls" id="telemetry-controls" hidden>
  <span id="telemetry-status" class="telemetry-status">Telemetría en vivo (actualiza cada 10 s)</span>
  <button id="telemetry-toggle" class="md-button md-button--primary" type="button">Activar</button>
</p>

## Licencia y avisos legales

- [License](legal/license.md) — MIT.
- [Disclaimer](legal/disclaimer.md) — riesgos antes de usar.
- [Notice](legal/notice.md) — atribución a librerías de terceros.

## Glosario

**Bridge**: programa intermediario que traduce entre dos protocolos. qrsgen
hace de bridge entre el protocolo binario de WhatsApp y HTTP REST.

**WhatsApp Web / Multi-Device**: API no oficial de WhatsApp que permite a
clientes externos (no oficial app) mantener una sesión vinculada a un
número escaneando un QR. Multi-Device permite hasta 4 dispositivos
simultáneos por número.

**whatsmeow**: librería Go open-source que implementa el protocolo
WhatsApp Web. Es lo que qrsgen usa para hablar con Meta.

**Instancia**: una sesión WhatsApp dentro del proceso qrsgen. Una
instancia = un número de teléfono. Un solo binario gestiona N instancias
en paralelo.

**Outbox**: cola persistida en Postgres donde van los mensajes outgoing
cuando la instancia está temporalmente desconectada. Se reentregan al
volver, con TTL de 5 minutos.

**BanWatcher**: módulo interno que analiza el ritmo de envíos para
detectar patrones que WhatsApp suele penalizar (velocity, diversity,
delivery ratio) y avisa antes de que actúe.

**Lifecycle event**: notificación HTTP que qrsgen POSTea cuando ocurre
algo relevante en una instancia (conexión, desconexión, QR generado,
strike, etc.).

**owner_tag**: string libre que el integrador puede asignar a una
instancia para correlacionarla con su modelo de tenants (clientes
internos). qrsgen lo expone en los agregados de facturación pero no lo
interpreta.

**Audit log inmutable**: tabla `bridge_audit_log` con triggers de
Postgres que rechazan UPDATE/DELETE — solo INSERT. Útil para forensics
y compliance.

**HMAC**: hash-based message authentication code. Firma criptográfica que
demuestra que un mensaje viene de quien dice y no ha sido modificado.
qrsgen lo usa opcionalmente para autenticar el webhook entrante.

**Distroless**: imagen Docker mínima sin shell ni paquetes auxiliares.
Reduce la superficie de ataque ante un RCE (remote code execution).
