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

> ⚠️ Antes de usar, lee el [Disclaimer](home/legal.md) — riesgos
> WhatsApp ToS, GDPR, limitación de responsabilidad. No afiliado con
> WhatsApp / Meta.

## Sub-apartados de esta sección

- **[Características destacadas](home/features.md)** — outbox,
  BanWatcher, audit log, usage tracking, HMAC, hardening, avatar sync,
  formato adaptativo del prefijo de grupo, sincronización de reacciones,
  typing indicators y read receipts.
- **[Por dónde empezar](home/getting-started.md)** — caminos para
  integrar, entender, desplegar u operar.
- **[Estado del proyecto](home/status.md)** — telemetría en vivo
  (QRs conectados / escaneados, instalaciones, mensajes).
- **[Licencia y avisos legales](home/legal.md)** — MIT, disclaimer,
  notice + riesgos importantes.

## Otras secciones

- **[Arquitectura](architecture/index.md)** — flujos internos, tablas Postgres,
  composición del binario.
- **[API](api/index.md)** — endpoints, payloads, lifecycle webhooks, recetas
  curl.
- **[Deployment](deployment/index.md)** — stack swarm, env vars, telemetría
  pública, multi-VPS.
- **[Security](security/index.md)** — siete capas (Bearer / HMAC / firewall /
  TLS / hardening / audit / backups).
- **[Operations](operations/index.md)** — runbook diario, troubleshooting,
  alerting.
- **[Integrations](integrations/index.md)** — recetas para n8n, Python, etc.

## Glosario

**Bridge**: programa intermediario que traduce entre dos protocolos.
qrsgen hace de bridge entre el protocolo binario de WhatsApp y HTTP
REST.

**WhatsApp Web / Multi-Device**: API no oficial de WhatsApp que permite
a clientes externos (no oficial app) mantener una sesión vinculada a un
número escaneando un QR. Multi-Device permite hasta 4 dispositivos
simultáneos por número.

**whatsmeow**: librería Go open-source que implementa el protocolo
WhatsApp Web. Es lo que qrsgen usa para hablar con Meta.

**Instancia**: una sesión WhatsApp dentro del proceso qrsgen. Una
instancia = un número de teléfono. Un solo binario gestiona N instancias
en paralelo.

**Lifecycle event**: notificación HTTP que qrsgen POSTea cuando ocurre
algo relevante en una instancia (conexión, desconexión, QR generado,
strike, etc.).
