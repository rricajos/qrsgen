# Migrar desde Baileys / WPPConnect

[Baileys](https://github.com/WhiskeySockets/Baileys) es una librería
TypeScript que implementa el protocolo WhatsApp Web sin browser
(igual filosofía que whatsmeow). Suele usarse embebida en una app
Node, o vía
[WPPConnect](https://github.com/wppconnect-team/wppconnect) que añade
una capa más alta encima.

La migración a qrsgen tiene sentido cuando quieres:

- Separar el bridge de la app de negocio.
- Outbox persistido sin implementarlo tú.
- BanWatcher proactivo.
- Audit log inmutable.

## Mapeo conceptual

| Baileys / WPPConnect | qrsgen |
|---|---|
| `creds.json` + `keys/` filesystem | Postgres `whatsmeow_*` — **re-pairing obligatorio** |
| `socket.ev.on('messages.upsert', ...)` | POST a `events_webhook_url` |
| `socket.sendMessage(jid, {text})` | `POST /webhook` con `message_type=outgoing` |
| `useMultiFileAuthState('./auth')` | Persistencia automática en Postgres |
| `wppconnect.create({session, ...})` | `POST /api/instances {name}` |

## Receta

### 1. Inventory de sessions actuales

**Baileys puro:**

Si tu app guarda sesiones en `./auth-<session>/` por cada número:

```bash
ls auth-*/ -d | sed 's|auth-||' | sed 's|/||' > /tmp/sessions.txt
# Salida:
# main
# sales
# support
```

**WPPConnect:**

```javascript
// migrate-export.js
const sessions = await wppconnect.getSessions(); // o similar según tu setup
const plan = {
  instances: sessions.map(s => ({
    name: s.session,
    events_webhook_url: process.env.NEW_WEBHOOK_URL,
    owner_tag: 'migrated-from-baileys',
  })),
};
require('fs').writeFileSync('/tmp/qrsgen-plan.json', JSON.stringify(plan, null, 2));
```

### 2. Aplicar plan en qrsgen

```bash
QRSGEN_URL=http://qrsgen:3100 \
QRSGEN_TOKEN="$QRSGEN_API_TOKEN" \
python3 tools/migrate/bulk-provision.py /tmp/qrsgen-plan.json
```

### 3. Re-pairing

Las sesiones Baileys (filesystem `creds.json` + `keys/*.json`) **no
son compatibles** con whatsmeow. Aunque ambas libraries hablan el
mismo protocolo, los formatos de serialización de claves son
distintos.

Los usuarios re-escanean. Sin atajos.

### 4. Refactor del código

```typescript
// Antes (Baileys embebido)
import { makeWASocket, useMultiFileAuthState } from '@whiskeysockets/baileys';

const { state, saveCreds } = await useMultiFileAuthState('./auth-main');
const sock = makeWASocket({ auth: state });
sock.ev.on('messages.upsert', m => handleIncoming(m));
await sock.sendMessage('34600000000@s.whatsapp.net', { text: 'Hola' });

// Después (qrsgen como bridge externo)
const TOK = process.env.QRSGEN_TOKEN;
const BASE = 'http://qrsgen:3100';

// Send
await fetch(`${BASE}/api/instances/main/webhook`, {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({
    event: 'message_created',
    message_type: 'outgoing',
    content: 'Hola',
    conversation: {id: 1, meta: {sender: {identifier: '34600000000@s.whatsapp.net'}}},
    id: Date.now(),
    private: false,
  }),
});

// Receive (Express ejemplo)
app.post('/qrsgen-events', (req, res) => {
  handleLifecycle(req.body);
  res.json({ok: true});
});
```

### 5. Ventajas vs seguir con Baileys

- **No tienes que mantener tu propio retry de reconexión** — el outbox lo cubre.
- **No gestionas filesystem de auth** — Postgres lo maneja con backups.
- **Outbox 5 min** — cero pérdida durante restarts de tu app.
- **Multi-instance trivial** — sin nuevos procesos Node por número.
- **Hardening serio** (distroless, read-only) — defensa real, no nominal.

### 6. Diferencias técnicas a tener en cuenta

| Aspecto | Baileys | qrsgen |
|---|---|---|
| Persistencia | Filesystem JSON | Postgres |
| Backup | Manual / volumes | systemd timer |
| Event API | EventEmitter Node | HTTP webhooks |
| Multi-Device dedup | Manual | Automatic (LID-twin) |
| Ban prevention | Reactiva | Proactiva (BanWatcher) |
| Audit log | No nativo | Inmutable, DB triggers |

## WPPConnect específico

WPPConnect ya expone una HTTP API similar a qrsgen, pero más limitada:

- No tiene outbox persistido (mensajes durante reconexión se pierden).
- No tiene BanWatcher.
- Las sessions se guardan en filesystem (no Postgres).
- No tiene multi-tenant via `owner_tag`.

La migración es directa: los mismos endpoints (`/start-session`,
`/send-message`) tienen equivalente en qrsgen
(`POST /api/instances`, `POST /webhook`). Reemplaza las URLs y formatos
JSON en tu cliente HTTP.

## Glosario

**Baileys**: librería TypeScript que implementa el protocolo
WhatsApp Web sin browser. Análoga a whatsmeow (Go). Stars ~15k.

**WPPConnect**: framework Node basado en Baileys que añade una capa
HTTP encima. Suele compararse con Evolution API o qrsgen.

**Multi-file auth state**: estrategia de Baileys que guarda la sesión
en N archivos JSON en filesystem. No portable a otros clientes.

**`creds.json` + `keys/`**: archivos donde Baileys guarda las claves
criptográficas y metadatos de sesión. Formato propio, no compatible
con whatsmeow.

**`messages.upsert`**: evento de Baileys que se dispara con cada
mensaje nuevo (entrante o reciente sync). Equivalente al webhook
incoming de qrsgen.

**EventEmitter**: patrón Node de eventos en proceso. Baileys lo usa
extensivamente. qrsgen usa HTTP webhooks en su lugar para desacoplar
del proceso integrador.
