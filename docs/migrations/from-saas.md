# Migrar desde un SaaS WhatsApp

Si vienes de un SaaS de pago, la lógica de migración es **más simple
técnicamente** que con plataformas auto-hospedadas (no tienes DB que
extraer), pero requiere revisar **carefully el pricing y compromisos
contractuales** antes de mover producción.

## Páginas dedicadas por provider

- [**Whapi.cloud**](from-whapi.md) — el más popular en EU/US.
- [**MaytAPI**](from-maytapi.md) — popular en Brasil y LATAM.

Ambas páginas incluyen:

- Mapeo conceptual (channel ↔ instancia, token ↔ owner_tag, etc.).
- API surface del provider y dónde extraer la info.
- Receta paso a paso con scripts.
- Comparativa de coste y "cuándo NO migrar".

## Lo común a todos los SaaS

### Motivaciones típicas

- **Coste**: SaaS comerciales cobran $30-300/instancia/mes. Self-host
  qrsgen baja a coste de VPS (~10-30 €/mes para 10+ instancias).
- **Privacidad**: tus mensajes pasan por un tercero opaco.
- **Auditabilidad**: el SaaS no enseña el código.
- **Compliance** (GDPR / HIPAA): puede requerir control total del flujo.
- **Lock-in**: si suben precios o cierran, te quedas atrapado.

### Lo que SÍ obtienes en qrsgen

- Outbox persistido, audit log inmutable, BanWatcher (los SaaS no
  exponen ninguno de los tres).
- Multi-tenant ligero vía `owner_tag` para revender.
- Control total: hardware, datos, logs, métricas.

### Lo que pierdes (sé honesto contigo mismo)

- **El SaaS gestiona el ban risk por ti** (en teoría). Si tu cuenta
  cae, ellos la recuperan.
- **Infraestructura propia**: VPS, Postgres, backups, monitoreo,
  on-call.
- **Soporte 24/7**: qrsgen es open-source — el "soporte" eres tú o un
  contractor.

### Receta común (independiente del provider)

```
1. Inventory:  listar canales/phones del provider via su API.
2. Plan:       generar plan.json para bulk-provision.
3. Provision:  POST a qrsgen para cada uno.
4. Re-pair:    usuarios re-escanean sus QRs (limitación WhatsApp).
5. Switch:     reconfigurar webhook URL en tu downstream para apuntar a qrsgen.
6. Cancel:     dar de baja los canales del SaaS provider.
```

Los pasos 1, 2, 5, 6 varían por provider — están en las páginas
dedicadas. El 3 y 4 son idénticos para cualquier origen.

### Comparativa de coste rápida

| Volumen | Whapi/MaytAPI típico | qrsgen self-host (VPS + tu tiempo) |
|---|---|---|
| 5 instancias / 30k msgs/mes | ~$300/mes | ~$15/mes + bajo |
| 25 instancias / 200k msgs/mes | ~$1500/mes | ~$30/mes + medio |
| 100+ instancias / 1M+ msgs/mes | ~$6000+/mes | ~$80/mes + serio |

A partir de **~10 instancias / 50k msgs/mes**, qrsgen empieza a ser
más rentable **si valoras tu tiempo a <40 €/h** y tienes la skill
operativa.

### Cuándo NO migrar (todavía)

- Tu equipo no tiene experiencia con Postgres / Docker.
- No tienes on-call definido para incidentes.
- Pricing al cliente final no soporta coste operativo.
- Volumen muy bajo (<5 instancias): el SaaS te sale más barato que un
  sysadmin.

## Glosario

**SaaS** (Software as a Service): software hospedado por un tercero,
accedido vía API/web.

**Channel / Phone / Instance**: distintos providers usan nombres
distintos para "una sesión WhatsApp". Whapi → Channel, MaytAPI →
Phone, qrsgen → Instance.

**Switchover**: cambiar el tráfico productivo del sistema antiguo al
nuevo. Gradual = un cliente a la vez. Big-bang = todos a la vez.

**TCO** (Total Cost of Ownership): coste real incluyendo tiempo
humano, no solo licencias. Self-host parece barato hasta que cuentas
tu hora de sysadmin.

**On-call**: persona responsable de responder a incidentes 24/7. Si
no tienes esto definido, posiblemente el SaaS te sigue saliendo a
cuenta.
