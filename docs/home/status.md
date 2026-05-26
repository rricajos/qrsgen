# Estado del proyecto

Producción con 4+ instancias activas. Tag estable más reciente en
[releases](https://github.com/rricajos/qrsgen/releases). Cada release
documentada en
[CHANGELOG](https://github.com/rricajos/qrsgen/blob/main/CHANGELOG.md).

## Telemetría en vivo

Si el endpoint público está habilitado, las cards de abajo muestran
contadores en tiempo real (polling cada 10 s). El visitante puede
pausar/reanudar el polling con el botón.

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
    <div class="stat-value" id="stat-installs-total">—</div>
    <div class="stat-label">Instalaciones totales (histórico)</div>
  </div>
  <div class="stat-card">
    <div class="stat-value" id="stat-total">—</div>
    <div class="stat-label">Instancias en memoria</div>
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

## ¿Qué significan estas métricas?

| Card | Significado |
|---|---|
| **QRs conectados** | Instancias en estado `ready` / `connected` **ahora mismo**. Snapshot live. |
| **QRs escaneados** | Total acumulado histórico de pairings exitosos. Cuenta `instance.paired` events en `bridge_audit_log`. Incluye re-pairings. |
| **Instalaciones activas** | Instancias en DB con `jid` configurado (alguna vez se pareron y siguen registradas). |
| **Instalaciones totales (histórico)** | Cuántas instalaciones han existido **alguna vez** — incluye las ya borradas. Sobrevive a DELETE porque sale del audit log append-only. |
| **Instancias en memoria** | Total de instancias gestionadas por el proceso ahora mismo (incluye `qr_pending`, `disconnected`). Snapshot live. |
| **Mensajes recibidos / enviados** | All-time totals agregados desde `bridge_usage_daily`. |

## ¿Cómo activar la telemetría en mi instalación?

La telemetría está apagada por defecto. Para activarla, sigue
[Deployment · Telemetría pública](../deployment/public-stats.md).

## Glosario

**Snapshot live**: foto del estado actual del sistema (instancias
conectadas en este momento), distinto de los counters históricos
acumulados.

**Counter histórico**: contador que solo crece. Para ver tasas en
tiempo se calcula la derivada (`rate()` en PromQL).

**QR conectado**: instancia en estado `ready` o `connected` ahora
mismo. Snapshot live.

**QR escaneado**: pairing exitoso histórico. qrsgen cuenta filas
`action='instance.paired'` en `bridge_audit_log` — sobrevive a borrar
instancias.

**Instalación activa**: instancia con `jid` configurado en
`bridge_instance` (alguna vez se pareó y sigue registrada).

**Instalación total**: cualquier instancia registrada en el proceso,
incluyendo las que aún están en `qr_pending` sin escanear.

**Polling**: técnica donde el cliente hace requests periódicas para
obtener datos actualizados. Las cards usan polling cada 10 s.

**Endpoint público**: HTTP endpoint accesible desde internet (no solo
desde la overlay LAN). qrsgen tiene uno opt-in para telemetría
agregada.

**Opt-in**: feature desactivada por default. Hay que activarla
explícitamente (env var).

**LocalStorage**: API del navegador para guardar datos persistentes
en el cliente. Las cards lo usan para recordar si el visitante eligió
ver telemetría o no.
