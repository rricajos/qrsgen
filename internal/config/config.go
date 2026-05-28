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
