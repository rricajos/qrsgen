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
