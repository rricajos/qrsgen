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
)
