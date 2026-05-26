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
    <div class="stat-value" id="stat-total">—</div>
    <div class="stat-label">Instalaciones totales</div>
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
| **QRs escaneados** | Total acumulado histórico de pairings exitosos. Sobrevive a borrar instancias (sale del audit log). |
| **Instalaciones activas** | Instancias en DB con `jid` configurado (alguna vez se pareron y siguen registradas). |
| **Instalaciones totales** | Total de instancias registradas en el proceso, incluyendo las que están en `qr_pending` o `disconnected`. |
| **Mensajes recibidos / enviados** | All-time totals agregados desde `bridge_usage_daily`. |

## ¿Cómo activar la telemetría en mi instalación?

La telemetría está apagada por defecto. Para activarla, sigue
[Deployment · Telemetría pública](../deployment/public-stats.md).
