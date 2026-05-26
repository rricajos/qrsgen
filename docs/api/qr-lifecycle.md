# QR y ciclo de vida

Endpoints para gestionar el escaneo del QR y forzar transiciones de estado.

## `GET /api/instances/:name/qr`

Devuelve el PNG bytes del QR actual.

**Response 200:** binario `image/png`.

**Response 404:** la instancia no tiene QR pendiente (ya está `ready` o
aún no inició el pairing).

---

## `GET /api/instances/:name/wait-ready?timeout=180`

Long-poll. Bloquea hasta que la instancia llega a `ready` o expira el
timeout (segundos, máximo 600).

**Response 200:** ver `GET /api/instances/:name`.
**Response 404:** instancia no existe.
**Response 408:** timeout expirado. Body incluye estado actual + QR si
está disponible.

---

## `POST /api/instances/:name/refresh-qr`

Fuerza la regeneración del canal QR — útil cuando el QR caducó (20s) y
quieres uno fresco sin esperar.

**Response 200:** `{"message":"qr refresh initiated"}`.

---

## `POST /api/instances/:name/restart`

Cierra y re-abre la conexión whatsmeow. Útil si la instancia está en un
estado raro pero la sesión no se ha perdido.

---

## `POST /api/instances/:name/logout`

Invalida la sesión a nivel server-side (Meta). El siguiente uso requiere
nuevo QR. Distinto de `DELETE` — el row de `bridge_instance` permanece.

## Glosario

**QR code (PNG)**: imagen que el usuario escanea con la app oficial de
WhatsApp para vincular su número a qrsgen. qrsgen lo genera cada ~20s
hasta que se escanea o expira la sesión de pairing.

**Pairing**: proceso de vinculación entre un número WhatsApp y qrsgen.
Solo se hace la primera vez — la sesión se persiste en Postgres.

**Long-poll**: request HTTP que el servidor mantiene abierta hasta que
ocurre un evento o expira el timeout, en lugar de hacer polling
periódico desde el cliente.

**Refresh QR**: forzar la regeneración del QR sin esperar al ciclo
natural de 20s. Útil cuando el QR mostrado al usuario caducó y quieres
uno nuevo inmediatamente.

**Restart de la conexión**: cierra el WebSocket actual y abre uno nuevo,
manteniendo la sesión vinculada. Distinto de `logout` (que invalida la
sesión).

**Logout server-side**: invalidación de la sesión en los servidores de
Meta. Tras esto, la sesión no se puede recuperar — hay que escanear un
QR nuevo. Equivale a "cerrar sesión" en la app oficial.
