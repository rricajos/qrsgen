# Migrar desde whatsapp-web.js

[whatsapp-web.js](https://github.com/pedroslopez/whatsapp-web.js) es la
librería Node.js más popular para integrar WhatsApp (~17k stars). Suele
usarse embebida en una app Node con `LocalAuth` (filesystem) o
`RemoteAuth` (cualquier store).

La migración a qrsgen tiene sentido cuando:

- Tu app Node embebía la librería y operar varios números se vuelve
  complicado.
- Necesitas features que whatsapp-web.js no tiene (outbox, BanWatcher,
  audit log inmutable).
- Quieres separar el bridge de tu app de negocio.

## Mapeo conceptual

| whatsapp-web.js | qrsgen |
|---|---|
| `clientId` (por session) | `name` de la instancia |
| `LocalAuth({clientId})` / `RemoteAuth` | Sesión persistida en `whatsmeow_*` (Postgres) — **re-pairing obligatorio** |
| `client.on('message', ...)` | POST a `events_webhook_url` |
| `client.sendMessage(jid, body)` | `POST /api/instances/:name/webhook` con `message_type=outgoing` |
| Auth manual en código | `QRSGEN_API_TOKEN` + Bearer |

## Receta

### 1. Inventory desde tu app Node

Si tu app gestiona N clients, exporta la lista:

```javascript
// migrate-export.js (ejecutado dentro de tu app)
const fs = require('fs');
const clients = [/* tu array de clientIds y metadata */];

const plan = {
  instances: clients.map(c => ({
    name: c.clientId,
    events_webhook_url: process.env.NEW_WEBHOOK_URL,
    inbox_id: c.inboxId || null,
    owner_tag: c.tenantId || 'migrated-from-wajs',
  })),
};

fs.writeFileSync('/tmp/qrsgen-plan.json', JSON.stringify(plan, null, 2));
console.log(`Exportadas ${plan.instances.length} instancias`);
```

### 2. Aplicar el plan en qrsgen

```bash
QRSGEN_URL=http://qrsgen:3100 \
QRSGEN_TOKEN="$QRSGEN_API_TOKEN" \
python3 tools/migrate/bulk-provision.py /tmp/qrsgen-plan.json
```

### 3. Re-pairing manual

Igual que en cualquier otra migración WhatsApp: los usuarios reescanean
sus QRs. La sesión `LocalAuth` que whatsapp-web.js guardaba en
`.wwebjs_auth/` **no es compatible** con whatsmeow (qrsgen).

### 4. Refactor de tu app

El cambio más grande no es de datos sino de **arquitectura**:

```javascript
// Antes (whatsapp-web.js embebido)
const { Client, LocalAuth } = require('whatsapp-web.js');
const client = new Client({ authStrategy: new LocalAuth({clientId: 'main'}) });
client.on('message', msg => handleIncoming(msg));
await client.sendMessage('34600000000@c.us', 'Hola');

// Después (qrsgen como bridge externo)
const httpx = ...
await fetch('http://qrsgen:3100/api/instances/main/webhook', {
  method: 'POST',
  body: JSON.stringify({
    event: 'message_created',
    message_type: 'outgoing',
    content: 'Hola',
    conversation: { id: 1, meta: { sender: { identifier: '34600000000@s.whatsapp.net' }}},
    id: Date.now(),
    private: false,
  }),
  headers: { 'Content-Type': 'application/json' },
});

// Y recibes incoming via webhook receiver de Express/Fastify/etc:
app.post('/qrsgen-events', async (req, res) => {
  const ev = req.body;
  if (ev.event === 'qr_generated') { /* mostrar al user */ }
  if (ev.event === 'connected')    { /* opcional: notificar */ }
  res.json({ ok: true });
});
```

### 5. JID format differences (cuidado)

- **whatsapp-web.js**: `<phone>@c.us` (legacy) o `<phone>@s.whatsapp.net`.
- **qrsgen / whatsmeow**: SIEMPRE `<phone>@s.whatsapp.net` para números.
  Los `@c.us` son auto-convertidos por whatsmeow al `@s.whatsapp.net`
  internamente.

Si tu código tiene `@c.us` hardcoded, sustitúyelo:

```javascript
// Antes
client.sendMessage(`${phone}@c.us`, content);

// Después (en payload qrsgen)
{ ..., conversation: { meta: { sender: { identifier: `${phone}@s.whatsapp.net` }}}}
```

### 6. Ventajas inmediatas tras la migración

- **Outbox automático**: si tu app falla cuando intenta enviar, qrsgen
  lo encola. Antes perdías el mensaje.
- **Sin gestión de re-conexión**: whatsmeow + outbox de qrsgen lo
  manejan. Quitas todo el código de retry/reconnect de tu app.
- **Multi-instance trivial**: añade más instancias sin levantar nuevos
  procesos Node.
- **Persistencia sin filesystem**: te quitas la complejidad de
  backupear `.wwebjs_auth/`.

## Ejemplo: migrar una app con 5 clients

Si tu app Node tiene 5 clients hardcoded:

```javascript
const clientIds = ['support', 'sales', 'tech', 'billing', 'marketing'];
```

Plan JSON:

```json
{
  "instances": [
    {"name": "support",   "events_webhook_url": "https://app.example.com/qrsgen-events", "owner_tag": "main"},
    {"name": "sales",     "events_webhook_url": "https://app.example.com/qrsgen-events", "owner_tag": "main"},
    {"name": "tech",      "events_webhook_url": "https://app.example.com/qrsgen-events", "owner_tag": "main"},
    {"name": "billing",   "events_webhook_url": "https://app.example.com/qrsgen-events", "owner_tag": "main"},
    {"name": "marketing", "events_webhook_url": "https://app.example.com/qrsgen-events", "owner_tag": "main"}
  ]
}
```

Aplicar:
```bash
QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN=... \
  python3 tools/migrate/bulk-provision.py plan.json
```

Tus 5 clients en qrsgen, esperando QR. Tras re-pairing, tu app Node
ya no necesita whatsapp-web.js — solo hace fetch a qrsgen.

## Glosario

**whatsapp-web.js**: librería Node.js que implementa el protocolo
WhatsApp Web vía Puppeteer + Chromium (más pesado que whatsmeow).
Stars en GitHub: ~17k.

**LocalAuth**: estrategia de whatsapp-web.js que guarda la sesión en
filesystem (`.wwebjs_auth/clientId/`). No portable a otros clientes.

**RemoteAuth**: estrategia donde la sesión se guarda en un store
externo (MongoDB, S3). Más flexible pero igual de no-portable a otro
cliente.

**clientId**: identificador de una sesión en whatsapp-web.js.
Equivalente al `name` de instancia en qrsgen.

**Puppeteer / Chromium**: stack que whatsapp-web.js usa para hablar
con WhatsApp Web (controla un navegador headless). Mucho más pesado
que whatsmeow (sin browser).

**JID format**: WhatsApp tiene dos formatos para identificar números:
`@c.us` (legacy) y `@s.whatsapp.net` (estándar Multi-Device). qrsgen
usa el segundo siempre.
