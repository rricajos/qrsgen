// Package errcode define los códigos de error públicos de qrsgen.
//
// Los códigos siguen el patrón `QRSGEN_<CATEGORY>_<REASON>` (uppercase,
// snake_case). El integrador puede pattern-matchear contra el campo
// `error_code` del body de las respuestas 4xx/5xx para reaccionar
// programáticamente, sin parsear strings.
//
// Ejemplo:
//
//	resp, _ := http.Post(...)
//	if resp.StatusCode == 422 {
//	    var body struct{ ErrorCode string `json:"error_code"` }
//	    json.NewDecoder(resp.Body).Decode(&body)
//	    if body.ErrorCode == errcode.SpamguardBlocked {
//	        // ... lógica específica
//	    }
//	}
package errcode

// String literals — public stable contract. NEVER rename existing codes
// (breaks integrators). Add new ones only.
const (
	// SpamguardBlocked: outgoing rechazado por la política spamguard
	// (duplicado de uno de los 2 últimos al mismo contacto). Webhook
	// devuelve 422.
	SpamguardBlocked = "QRSGEN_SPAMGUARD_BLOCKED"

	// HMACMismatch: header X-Qrsgen-Signature ausente o no coincide
	// con HMAC-SHA256(secret, raw_body). Webhook devuelve 401.
	HMACMismatch = "QRSGEN_HMAC_MISMATCH"

	// PayloadInvalid: JSON malformado o esquema incorrecto. Webhook
	// devuelve 400.
	PayloadInvalid = "QRSGEN_PAYLOAD_INVALID"

	// QueueFull: outbox alcanzó MaxQueueDepth para la instancia (default
	// 200 mensajes pendientes). Devuelve 503.
	QueueFull = "QRSGEN_QUEUE_FULL"

	// InstanceNotFound: la instancia referenciada en la URL no existe.
	// Devuelve 404.
	InstanceNotFound = "QRSGEN_INSTANCE_NOT_FOUND"

	// TenantNotFound: el owner_tag referenciado no tiene config en
	// bridge_tenant. Devuelve 404.
	TenantNotFound = "QRSGEN_TENANT_NOT_FOUND"

	// Internal: fallo inesperado del servidor — bug de qrsgen o
	// dependencia externa caída (DB, whatsmeow). Devuelve 500.
	Internal = "QRSGEN_INTERNAL"
)

// HumanText devuelve una descripción humana del código (en español, para
// mostrar en UI de integradores). El integrador puede traducir si tiene
// localización propia.
func HumanText(code string) string {
	switch code {
	case SpamguardBlocked:
		return "Bloqueado por QRsGEN — duplicado de uno de los 2 últimos mensajes al mismo contacto. No se entregó al cliente."
	case HMACMismatch:
		return "Firma HMAC del webhook entrante inválida o ausente."
	case PayloadInvalid:
		return "Payload del webhook inválido (JSON malformado o campos requeridos faltantes)."
	case QueueFull:
		return "Cola de outbox llena para esta instancia. Esperar a que drene o aumentar MaxQueueDepth."
	case InstanceNotFound:
		return "La instancia no existe."
	case TenantNotFound:
		return "El tenant (owner_tag) no tiene config en bridge_tenant."
	case Internal:
		return "Error interno del servidor."
	default:
		return code
	}
}
