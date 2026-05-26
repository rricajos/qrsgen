# Migrar desde SaaS (Whapi.cloud, MaytAPI, etc.)

Si vienes de un SaaS de pago como
[Whapi.cloud](https://whapi.cloud/), [MaytAPI](https://maytapi.com/),
[Z-API](https://z-api.io/) o similares, la migración es más sencilla
en el plano técnico (no tienes DB que extraer) pero requiere
**revisar carefully el modelo de pricing y compromisos**.

## Motivaciones típicas para migrar

- **Coste**: SaaS comerciales cobran $30-300/instancia/mes. Auto-hospedar
  qrsgen baja a coste de VPS (5-20€/mes para 10+ instancias).
- **Privacidad**: tus mensajes pasan por un tercero opaco.
- **Auditabilidad**: el SaaS no te enseña el código, no puedes auditar.
- **Compliance**: GDPR/HIPAA pueden requerir control total del flujo
  de datos.
- **Lock-in**: si el SaaS cierra o sube precios, te quedas atrapado.

## Lo que SÍ obtienes

- Outbox persistido, audit log, BanWatcher (que los SaaS no exponen).
- Multi-tenant ligero vía `owner_tag` (para revender a tus propios
  clientes si tu modelo lo permite).
- Control total: hardware, datos, logs, métricas.

## Lo que pierdes (sé honesto contigo mismo)

- **El SaaS gestiona el ban risk por ti** (en teoría). Si tu cuenta cae,
  ellos la recuperan. qrsgen te da el BanWatcher pero la responsabilidad
  operativa es tuya.
- **Infraestructura propia**: VPS, Postgres, backups, monitoreo,
  on-call. Si tu equipo no quiere gestionarlo, el SaaS gana.
- **Soporte 24/7**: qrsgen es open-source — el "soporte" eres tú o un
  contractor.

## Mapeo conceptual (Whapi.cloud como ejemplo)

| Whapi.cloud | qrsgen |
|---|---|
| `token` por instancia (channel) | Bearer global + `owner_tag` para distinguir clientes |
| `https://gate.whapi.cloud/messages` | `POST /api/instances/:name/webhook` |
| Webhook config en el panel | `events_webhook_url` per-instance |
| `channelId` | `name` de instancia |
| Sus webhooks en formato Whapi-específico | Channel::Api-compatible (Chatwoot-style) |

## Receta

### 1. Inventory desde el SaaS

La mayoría exponen una API de listado:

**Whapi.cloud:**
```bash
curl -sS -H "Authorization: Bearer $WHAPI_TOKEN" \
  https://manager.whapi.cloud/channels | jq '.[]|{id,name,webhook}'
```

**MaytAPI:**
```bash
curl -sS -u "$MAYT_USER:$MAYT_TOKEN" \
  https://api.maytapi.com/api/$PRODUCT_ID/listPhones | jq
```

Manual fallback: anota desde el panel web los `channelId` / `phoneId`,
los nombres lógicos que les diste, y a qué webhook estaban apuntando.

### 2. Generar plan JSON

```python
# A partir de tu listado:
plan = {
  "instances": [
    {"name": "whatsapp-main",  "events_webhook_url": "https://my-app.example.com/qrsgen-events", "owner_tag": "tenant-acme"},
    {"name": "whatsapp-sales", "events_webhook_url": "https://my-app.example.com/qrsgen-events", "owner_tag": "tenant-acme"},
    # ...
  ]
}
```

### 3. Aplicar plan en qrsgen

```bash
python3 tools/migrate/bulk-provision.py plan.json
```

### 4. Re-pairing

Cada usuario re-escanea. Los SaaS guardan la sesión en SU
infraestructura; el QR escaneado allí no sirve para qrsgen (y no
tienes acceso a sus claves de todas formas).

### 5. Switchover gradual

Recomendación: **migra primero las instancias menos críticas** y deja
la principal para el final. Así pruebas la operación end-to-end con
clientes que no son los más sensibles.

Para cada instancia migrada:
- Apaga el webhook en el SaaS (Whapi/MaytAPI).
- Configura `events_webhook_url` en qrsgen apuntando a tu downstream.
- Verifica que llegan mensajes.
- Cancela ese número en el SaaS.

### 6. Cuándo NO migrar (todavía)

Si te aplica alguno de estos:

- Tu equipo no tiene experiencia operando Postgres / Docker.
- No tienes un on-call defined para incidents.
- Tu pricing al cliente final no soporta el coste operativo (si cobras
  10€/mes/cliente y el VPS te cuesta 100€/mes, necesitas 30+ clientes
  para empezar a tener margen).
- Tu volumen es bajo (<5 instancias): el SaaS te sale más barato que
  un sysadmin.

## Comparativa de coste (estimación)

| Item | Whapi.cloud | qrsgen (self-host) |
|---|---|---|
| 5 instancias / 30k msgs/mes | ~$300/mes | ~$15/mes VPS + tu tiempo |
| 25 instancias / 200k msgs/mes | ~$1500/mes | ~$30/mes VPS + monitoreo |
| 100 instancias / 1M msgs/mes | ~$6000+/mes | ~$80/mes VPS + tiempo serio |

A partir de ~10 instancias / 50k msgs/mes, qrsgen self-host empieza a
ser más rentable **si valoras tu tiempo a <40€/h** y tienes la skill
operativa.

## Glosario

**SaaS** (Software as a Service): software hospedado por un tercero,
accedido vía API/web. Te ahorra infraestructura pero pierdes control.

**Whapi.cloud / MaytAPI / Z-API**: providers SaaS para WhatsApp
business. Pricing típico $30-300/instancia/mes.

**Channel / Channel ID** (Whapi): equivalente a una instancia qrsgen.

**Phone ID** (MaytAPI): mismo concepto, distinto naming.

**Manager API**: en los SaaS, el endpoint para administrar canales
(crear, configurar, eliminar). qrsgen lo expone como `/api/instances/*`.

**Switchover**: paso operativo de transferir tráfico de un sistema al
otro. Gradual = un cliente a la vez. Big-bang = todos a la vez (más
arriesgado).

**Total Cost of Ownership (TCO)**: coste real de operar algo
incluyendo tiempo humano, no solo licencias. Self-host parece barato
hasta que cuentas tu hora de sysadmin.

**On-call**: persona responsable de responder a incidentes 24/7. Si
no tienes esto definido para self-host, posiblemente el SaaS te sigue
saliendo a cuenta.
