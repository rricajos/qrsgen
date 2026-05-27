# Outbox encryption

Desde **v0.27.0**, qrsgen puede cifrar los payloads del outbox
(`bridge_outgoing_queue`) en reposo. Opt-in via env var: si no la
configuras, no se cifra nada y el comportamiento es idéntico a
v0.26.x.

## Cómo funciona

- **AES-256-GCM** con nonce aleatorio de 12 bytes por fila.
- La key vive en `OUTBOX_ENCRYPTION_KEY` (env del proceso, 32 bytes
  codificados en base64 estándar).
- Schema: las columnas `payload_enc` (bytea) y `nonce` (bytea) se
  añaden idempotentemente. Si `nonce IS NULL` la fila está en claro
  (legacy / opt-out); si `nonce IS NOT NULL` se descifra al drenar.

## Habilitar

Generar una key:

```bash
head -c 32 /dev/urandom | base64
# ejemplo de output: 4HfZ9pX0/...mGgkw=
```

Añadir al `.env` del stack:

```bash
OUTBOX_ENCRYPTION_KEY=4HfZ9pX0/...mGgkw=
```

`docker stack deploy` y listo. En el log de boot verás:

```
outbox encryption enabled (AES-256-GCM)
```

Desde ese momento, **payloads nuevos** se cifran. Filas en
`bridge_outgoing_queue` que ya estuvieran encoladas en claro siguen
entregándose en claro durante su TTL (5 min default) y desaparecen.

## Rotación de key

qrsgen v0.27.0 soporta **una sola key activa a la vez**. La rotación
segura es:

1. Generar nueva key `OUTBOX_ENCRYPTION_KEY_NEW`.
2. **NO** desplegar todavía.
3. Pausar el flujo de outgoings del downstream durante ~6 min.
4. Esperar a que el outbox drene (`/api/instances/:name/outbox`
   `pending=0`).
5. Cambiar `OUTBOX_ENCRYPTION_KEY` al valor nuevo en el `.env`.
6. Redeploy.
7. Reactivar el flujo del downstream.

Si rotas con outgoings encolados con la key vieja, esos mensajes
**fallarán al descifrar** (log warn + se quedan en pending hasta
expirar). No se pierde el aviso (`outgoing_expired` lifecycle event
sale igual), pero el contenido se pierde porque no podemos descifrar.

Rotación dual-key (sin window de pausa) queda como mejora futura.

## Qué NO cifra

- **Otras tablas**: `bridge_instance`, `bridge_tenant`,
  `bridge_audit_log`, `bridge_usage_daily`. Estas tienen su propia
  consideración de cifrado (mayoría no requieren — son metadatos).
- **Headers HTTP** ni el body de la HTTP request en sí — el cifrado
  es solo at-rest del payload una vez encolado.

## Qué mitiga

Vector cubierto: **DBA compromise / dump de Postgres**. Si alguien
con acceso a la DB lee `bridge_outgoing_queue`, ve únicamente
ciphertext + nonce. Sin la key del proceso, no puede recuperar el
contenido.

Vector NO cubierto: compromise del **propio proceso qrsgen** (la key
vive en memoria del binario). Para defenderse de eso harían falta
HSMs o sealing de OS-level — fuera del scope de qrsgen.

## Glosario

**AES-256-GCM**: cifrado simétrico autenticado. GCM (Galois/Counter
Mode) añade integridad: alterar el ciphertext provoca fallo de
descifrado, no recuperación silenciosa de basura.

**Nonce**: número aleatorio de un solo uso. Para AES-GCM debe ser
único por (key, payload) — qrsgen genera 12 bytes random por fila.
Reusar un nonce con la misma key compromete la confidencialidad.

**At-rest encryption**: cifrado de datos persistidos en disco/DB.
Diferente de "in-flight" (TLS) y "in-use" (memoria del proceso).

**KEK / DEK** (no usado en v0.27.0): patrón two-tier donde una Key
Encryption Key cifra Data Encryption Keys per-tenant. Permite rotar
DEKs sin re-encriptar todo. Considerado para v0.28+ junto con cifrado
per-tenant.
