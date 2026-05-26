# Migraciones

Recetas para mover una configuración existente desde otra herramienta
WhatsApp a qrsgen — preservando todo lo que es portable y dejando
claro qué requiere intervención manual.

## ⚠️ Lo que NO se puede migrar

**Las sesiones WhatsApp jamás son portables**. WhatsApp Web/Multi-Device
vincula las claves criptográficas al device físico que escaneó el QR. Si
cambias de cliente (Evolution → qrsgen, Baileys → qrsgen, etc.) el
usuario **debe re-escanear el QR**.

Lo único portable son los **datos asociados**:

- Nombres de instancia / inbox.
- Mapeo número → identificador en tu downstream.
- Workflows del orquestador.
- Histórico de mensajes (si tu fuente lo exponía).

Esto es **una limitación del protocolo de WhatsApp, no de qrsgen**.
Cualquier migración entre clientes Multi-Device tiene esta restricción.

## Estrategia general

```
[Plataforma origen]
        │
        │ 1. Inventory: extraer config (nombres, números, inboxes)
        ▼
   plan.json
        │
        │ 2. Bulk provision: aplicar plan en qrsgen
        │    POST /api/instances por cada uno
        ▼
   qrsgen con instancias creadas pero state = qr_pending
        │
        │ 3. Re-pairing: usuarios re-escanean QRs uno por uno
        │    (no automatizable — limitación de WhatsApp)
        ▼
   qrsgen con sessions activas
        │
        │ 4. Webhook switch: el downstream apunta a qrsgen
        │    en lugar de la plataforma origen
        ▼
   qrsgen operativo, plataforma origen apagada
        │
        │ 5. Validate: smoke test por instancia
        ▼
       FIN
```

## Por origen

- [**Evolution API**](from-evolution-api.md) — el más común
  (PHP/Laravel + Postgres, popular en LATAM).
- [**whatsapp-web.js**](from-whatsapp-web-js.md) — librería Node.js
  con LocalAuth en filesystem.
- [**Baileys / WPPConnect**](from-baileys.md) — TypeScript, SQLite o
  filesystem.
- [**SaaS (Whapi.cloud, MaytAPI, etc.)**](from-saas.md) — sin acceso a
  DB; solo API.

## Migración inversa (cómo salir de qrsgen)

Si decides usar otra plataforma, sigue
[Salir de qrsgen](leaving.md) — la misma lógica, pero al revés.
Documentamos esto explícitamente para que sepas que **no hay lock-in**.

## Herramientas

Las recetas usan los scripts de
[`tools/migrate/`](https://github.com/rricajos/qrsgen/tree/main/tools/migrate)
del repo:

- `bulk-provision.py` — recibe un plan JSON y crea instancias en qrsgen.
- `validate.py` — comprueba que cada instancia provisionada responde.
- `export-config.py` — exporta toda la config actual de qrsgen a JSON
  (útil para migrar entre VPS o como backup adicional).

Todos los scripts están en Python con httpx — funcionan en cualquier
host con Python 3.10+ y `pip install httpx`.

## Glosario

**Migración**: trasladar una configuración / datos de una plataforma a
otra. En el contexto WhatsApp implica re-pareado obligatorio porque las
claves del protocolo no son portables.

**Re-pairing**: proceso donde el usuario vuelve a escanear el QR en su
WhatsApp móvil. Imposible de automatizar — Meta lo exige por diseño.

**Bulk provisioning**: crear muchas instancias de golpe vía
`POST /api/instances` programáticamente, normalmente a partir de un
plan extraído de la plataforma origen.

**Plan JSON**: estructura intermedia entre extracción e import. Permite
revisar y editar antes de aplicar.

**Webhook switch**: cambiar la URL del downstream del orquestador para
que apunte a qrsgen en lugar de la plataforma origen. Suele hacerse
después del re-pairing exitoso.

**Lock-in**: situación donde cambiar de plataforma es prohibitivamente
caro o destructivo. qrsgen lo evita exponiendo `export-config.py` y
una API REST estándar.

**Honest dealing**: documentar cómo salir del producto con la misma
claridad que cómo entrar. Genera confianza B2B.
