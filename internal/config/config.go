// Package config carga y valida la configuración desde variables de entorno.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port     int    `env:"PORT" envDefault:"3100"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	NodeEnv  string `env:"NODE_ENV" envDefault:"production"`

	PostgresHost     string `env:"POSTGRES_HOST,required"`
	PostgresPort     int    `env:"POSTGRES_PORT" envDefault:"5432"`
	PostgresDB       string `env:"POSTGRES_DB,required"`
	PostgresUser     string `env:"POSTGRES_USER,required"`
	PostgresPassword string `env:"POSTGRES_PASSWORD,required"`

	DownstreamBaseURL   string `env:"DOWNSTREAM_BASE_URL,required"`
	DownstreamAPIToken  string `env:"DOWNSTREAM_API_TOKEN,required"`
	DownstreamAccountID int    `env:"DOWNSTREAM_ACCOUNT_ID" envDefault:"1"`
	DownstreamInboxID   int    `env:"DOWNSTREAM_INBOX_ID,required"`

	InstanceName      string `env:"INSTANCE_NAME,required"`
	InstancePhoneHint string `env:"INSTANCE_PHONE_HINT"`

	DedupWindowMs int  `env:"DEDUP_WINDOW_MS" envDefault:"10000"`
	DedupEnabled  bool `env:"DEDUP_ENABLED" envDefault:"true"`

	// APIToken protege endpoints administrativos. Si está vacío, no se exige
	// auth (backward-compat). Si está configurado, los clientes deben mandar
	// `Authorization: Bearer <token>` en cada request a /api/instances/*.
	// La ruta /api/instances/:name/webhook está exenta (el downstream típicamente no manda headers
	// arbitrarios; usa su propia firma HMAC del webhook).
	APIToken string `env:"QRSGEN_API_TOKEN"`

	// WebhookHMACSecret protege el endpoint /api/instances/:name/webhook con
	// verificación HMAC-SHA256 del body crudo. Si vacío, no se exige firma
	// (backward-compat). Si configurado, el downstream debe mandar
	// `X-Qrsgen-Signature: sha256=<hex>` calculado como HMAC(secret, body).
	WebhookHMACSecret string `env:"WEBHOOK_HMAC_SECRET"`

	// PublicStatsEnabled habilita el endpoint /api/public/stats (sin auth)
	// que devuelve contadores agregados (instances connected/total, messages
	// in/out totales). Pensado para landing pages estáticas que muestren
	// telemetría en vivo. Default false — opt-in explícito.
	PublicStatsEnabled bool `env:"PUBLIC_STATS_ENABLED" envDefault:"false"`

	// PublicStatsAllowOrigin restringe el header CORS Access-Control-Allow-Origin
	// del endpoint público. Default vacío → sin CORS (otros origins no pueden
	// fetch via JS). Ejemplo: "https://rricajos.github.io".
	PublicStatsAllowOrigin string `env:"PUBLIC_STATS_ALLOW_ORIGIN"`

	// OutboxEncryptionKey es una key AES-256 (32 bytes) codificada en base64
	// estándar. Si está set, qrsgen cifra cada payload nuevo del outbox con
	// AES-GCM al persistirlo en `bridge_outgoing_queue`. Filas pre-encryption
	// (sin nonce) siguen entregándose en claro — backward compat. Si vacío
	// (default) no se cifra. Desde v0.27.0.
	OutboxEncryptionKey string `env:"OUTBOX_ENCRYPTION_KEY"`

	// GroupPrefixSender controla si qrsgen, al postear a downstream un
	// mensaje incoming de un grupo, prefija el body con la identidad del
	// participante (`+phone - Name:\n<body>`). Default true: sin él, los
	// mensajes de múltiples senders dentro de una misma conv del grupo se
	// ven idénticos. Desde v0.29.0.
	GroupPrefixSender bool `env:"QRSGEN_GROUP_PREFIX_SENDER" envDefault:"true"`

	// GroupHeaderTTL: si > 0, mensajes consecutivos del mismo sender en
	// un grupo dentro de este TTL se postean SIN el header de identidad
	// (replica el "burst" visual de WhatsApp donde solo el primer msg
	// muestra quién habla). 0 = feature desactivada, header siempre.
	// Default "10m". Desde v0.30.0.
	GroupHeaderTTL time.Duration `env:"QRSGEN_GROUP_HEADER_TTL" envDefault:"10m"`

	// AvatarSync: si true, qrsgen descarga la foto WhatsApp del contacto/
	// grupo al crear el contact en downstream y la sube como avatar.
	// Fire-and-forget (no bloquea el msg). false → contact queda con el
	// letter avatar autogenerado por el downstream. Default true.
	// Desde v0.31.0.
	AvatarSync bool `env:"QRSGEN_AVATAR_SYNC" envDefault:"true"`

	// AvatarRefreshTTL: si > 0, contactos existentes se re-chequean cada
	// este TTL para detectar cambios de foto WhatsApp. La comparación
	// usa el ID (cheap metadata, no descarga); solo se descarga + sube
	// si el ID cambió. 0 = sin refresh (modo v0.31.0: sync solo al crear).
	// Default "24h". Desde v0.31.1.
	AvatarRefreshTTL time.Duration `env:"QRSGEN_AVATAR_REFRESH_TTL" envDefault:"24h"`

	// ReactionsSync: si true, las reacciones a mensajes WhatsApp (emojis
	// que el cliente añade tocando largo el mensaje) se propagan al
	// downstream como mensajes incoming con formato
	// "**~Name** reaccionó con 👍". false los ignora silenciosamente.
	// Default true. Desde v0.33.0.
	ReactionsSync bool `env:"QRSGEN_REACTIONS_SYNC" envDefault:"true"`

	// TypingSync: si true, los eventos de typing (composing/paused) que
	// emite WhatsApp se propagan al downstream como toggle_typing_status.
	// El downstream renderiza "está escribiendo" en la UI del agente.
	// false los ignora. Default true. Desde v0.34.0.
	TypingSync bool `env:"QRSGEN_TYPING_SYNC" envDefault:"true"`

	// ReadReceiptsSync: si true, los read receipts WhatsApp (cliente
	// abrió el chat y vio los mensajes del agente) actualizan el
	// contact_last_seen_at del conv en el downstream. La UI del
	// downstream renderiza "leído" en los msgs correspondientes.
	// Default true. Desde v0.34.1.
	ReadReceiptsSync bool `env:"QRSGEN_READ_RECEIPTS_SYNC" envDefault:"true"`

	// MarkAsReadOutgoing: si true, qrsgen rastrea los WAIDs de mensajes
	// incoming y los marca como leídos en WhatsApp cuando el downstream
	// notifica via webhook `conversation_updated` con un nuevo
	// `agent_last_seen_at`. El cliente WA ve el doble check azul.
	// REQUIERE que el downstream esté configurado para enviar el evento
	// conversation_updated al webhook de qrsgen — sin esa config el
	// feature no hace nada. Default true. Desde v0.39.0.
	MarkAsReadOutgoing bool `env:"QRSGEN_MARK_AS_READ_OUTGOING" envDefault:"true"`
}

func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("parse env: %w", err)
	}
	return cfg, nil
}

func (c Config) PostgresDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.PostgresUser, c.PostgresPassword, c.PostgresHost, c.PostgresPort, c.PostgresDB)
}
