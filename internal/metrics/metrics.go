// Package metrics define los contadores Prometheus de qrsgen.
// Se exponen en GET /metrics (sin auth — métricas son operacionales, no PII).
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// MessagesTotal contabiliza cada mensaje que pasa por el bridge.
	// direction: "in" (recibido de WhatsApp y propagado a downstream) o
	//            "out" (enviado a WhatsApp desde downstream).
	// owner_tag: tenant correlator si la instancia lo tiene; "" para
	// single-downstream o instancias sin tenant configurado.
	MessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_messages_total",
		Help: "Total de mensajes procesados por el bridge.",
	}, []string{"direction", "instance", "owner_tag"})

	// SpamguardBlocks: nº de outgoings bloqueados por la política last-2.
	SpamguardBlocks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_spamguard_blocks_total",
		Help: "Mensajes salientes bloqueados por el filtro spamguard.",
	}, []string{"instance", "owner_tag"})

	// LifecycleEvents: transiciones emitidas al webhook (connected, qr_generated, etc.)
	LifecycleEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_lifecycle_events_total",
		Help: "Eventos de ciclo de vida emitidos por instancia.",
	}, []string{"instance", "event", "owner_tag"})

	// MessageDispatchErrors: fallos al despachar mensajes (incoming downstream POST,
	// outgoing whatsmeow SendText, etc.). Útil para alertas.
	MessageDispatchErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_message_dispatch_errors_total",
		Help: "Errores al despachar mensajes.",
	}, []string{"direction", "instance", "kind", "owner_tag"})

	// LifecycleWebhookRetries: cuenta reintentos de webhooks lifecycle críticos.
	// outcome: "success" si el reintento entregó, "exhausted" si se acabaron los
	// attempts y el mensaje se descartó.
	LifecycleWebhookRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_lifecycle_webhook_retries_total",
		Help: "Reintentos de webhooks lifecycle críticos (strike, ban_risk, etc.).",
	}, []string{"event", "outcome"})

	// ActiveInstances: gauge con nº de instancias en estado "ready" (conectadas).
	ActiveInstances = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qrsgen_active_instances",
		Help: "Número de instancias actualmente conectadas a WhatsApp.",
	})

	// TotalInstances: gauge con nº TOTAL de instancias gestionadas
	// (conectadas + desconectadas + qr_pending).
	TotalInstances = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qrsgen_total_instances",
		Help: "Número total de instancias gestionadas.",
	})

	// VersionInfo: gauge fijo a 1 con el tag del binario como label. Estándar
	// "info metric" — los dashboards lo joinean para mostrar versión activa.
	// Ejemplo PromQL: `qrsgen_version_info{version="0.28.2"}`.
	VersionInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qrsgen_version_info",
		Help: "Versión del binario qrsgen (gauge fijo a 1 con label version).",
	}, []string{"version"})

	// RealtimeEventsTotal: contador unificado de eventos "real-time" del
	// bridge — avatares sincronizados, reacciones, typing, read receipts,
	// retroactive name update (v0.40.1).
	// labels:
	//   feature: "avatar" | "reaction" | "typing" | "read_receipt" |
	//            "retroactive_name"
	//   result:  "ok"               (operación completada exitosamente)
	//            "no_contact"       (contacto no existe en downstream)
	//            "no_conv"          (conv no encontrada / no abierta)
	//            "throttled"        (filtrado por anti-spam, ej. typing tracker)
	//            "filtered"         (descartado por tipo, ej. receipt delivered)
	//            "wa_miss"          (WA no tiene la info — foto privada, etc.)
	//            "wa_error"         (whatsmeow falló)
	//            "ds_error"         (downstream rechazó el POST/PATCH)
	//            "skip_disabled"    (feature off vía env — retroactive_name)
	//            "skip_fullsync"    (whatsmeow fromFullSync — retroactive_name)
	//            "skip_empty_name"  (contacto sin nombre — retroactive_name)
	//            "skip_no_entries"  (sender sin mensajes tracked — retroactive_name)
	//
	// Desde v0.35.0 (v0.40.1 añade feature retroactive_name). Permite
	// calcular tasas de éxito y detectar regresiones en producción con
	// PromQL como:
	//   sum by (feature) (rate(qrsgen_realtime_events_total{result="ds_error"}[5m]))
	RealtimeEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_realtime_events_total",
		Help: "Eventos real-time procesados por el bridge (avatar/reaction/typing/receipt).",
	}, []string{"feature", "result", "instance"})

	// DownstreamTemplateFallbacks: cuenta cuántas veces el payload_template
	// per-tenant cayó al shape Chatwoot default. Permite detectar templates
	// rotos en producción sin tener que inspeccionar logs manualmente.
	// reason:
	//   "execute_failed"   → el template ejecutó error (variable inexistente, etc.)
	//   "invalid_json"     → el output del template no era JSON sintácticamente válido
	// PromQL útil:
	//   rate(qrsgen_downstream_template_fallbacks_total[5m])
	// Desde v0.65.2.
	DownstreamTemplateFallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qrsgen_downstream_template_fallbacks_total",
		Help: "Veces que el payload_template cayó al shape Chatwoot default por error de execute o JSON inválido.",
	}, []string{"reason"})
)
