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

	// GroupEventsEnabled activa la propagación de *events.GroupInfo
	// (cambios de nombre/topic/miembros/lock/announce), *events.JoinedGroup
	// (bot añadido a grupo nuevo) e *events.IdentityChange (código de
	// seguridad cambia) como activity msgs en Chatwoot. Default false
	// (opt-in). Desde v0.47.0.
	GroupEventsEnabled bool `env:"QRSGEN_GROUP_EVENTS_ENABLED" envDefault:"false"`

	// HistoryImportEnabled activa el feature de importar mensajes
	// históricos al downstream (v0.46.0). Opt-in: implica POST
	// rate-limited a Chatwoot al recibir un HistorySync de whatsmeow
	// (pareo nuevo o respuesta a un endpoint admin on-demand).
	// Default false. Desde v0.46.0.
	HistoryImportEnabled bool `env:"QRSGEN_HISTORY_IMPORT_ENABLED" envDefault:"false"`

	// HistoryImportDays cuántos días hacia atrás importa. Clamped a
	// [1, 30]. Default 7. WhatsApp limita el histórico que devuelve
	// según ajustes del phone (típicamente 30/90/180 días) — si
	// pides 30 pero el phone solo guarda menos, da lo que tiene.
	// Desde v0.46.0.
	HistoryImportDays int `env:"QRSGEN_HISTORY_IMPORT_DAYS" envDefault:"7"`

	// HistoryImportRatePerSec límite de POST/s al downstream durante
	// el import. Default 5 — conservador para no estresar Chatwoot
	// con bursts del historical sync. Aumentar con cuidado. v0.46.0.
	HistoryImportRatePerSec int `env:"QRSGEN_HISTORY_IMPORT_RATE_PER_SEC" envDefault:"5"`

	// ReactionHeaderSep controla el separador entre el header y el
	// verb en reacciones (v0.45.1). Distinto del GroupHeaderSep porque
	// la reacción es visualmente más atómica que un msg con cuerpo
	// arbitrario. Default `nl` (`\n`) — formato compacto:
	//
	//   `+34611887663 · ~Agustina`
	//   reaccionó con 👍
	//
	// Mismos alias que QRSGEN_GROUP_HEADER_SEP. Desde v0.45.1.
	ReactionHeaderSep string `env:"QRSGEN_REACTION_HEADER_SEP" envDefault:"nl"`

	// HeaderTemplate controla el render del header de sender (group
	// prefix + reactions). Tokens: `$phone` (E.164 con `+`), `$name`
	// (nombre canónico con `~` delante si no saved — automático).
	// Default `` `$phone · $name` `` — code block con middle dot.
	// Ejemplos alternativos:
	//   - "`$phone` · **$name**"  → phone en code, nombre en bold separado
	//   - "$phone | $name"        → plano sin markdown
	//   - "[$phone] $name"        → con corchetes
	// El `~` para no-saved está integrado en $name automáticamente
	// (IsContactSaved del WAResolver) — el operador solo elige el wrapper.
	// Desde v0.45.0.
	HeaderTemplate string `env:"QRSGEN_HEADER_TEMPLATE"`

	// GroupHeaderSep controla el separador entre el header de remitente
	// y el body. Default "paragraph" (\n\n) porque es lo único que
	// renderiza fiable en Chatwoot. Alternativas (alias env-friendly):
	//   - "paragraph"/"p" → "\n\n"  (default, deja aire de párrafo)
	//   - "br"            → "<br>"  (Chatwoot lo trata como autolink y
	//                                renderiza como <code>br</code>;
	//                                NO recomendado salvo downstream
	//                                con HTML allowlist permisivo)
	//   - "br_self"/"br/" → "<br/>" (probar si br no funciona)
	//   - "lsep"/"u2028"  → U+2028  (Unicode LINE SEPARATOR, bypassa
	//                                markdown; soporte browser amplio)
	//   - "nl"/"soft"     → "\n"    (soft break; en Chatwoot inline)
	//   - "slash"         → "\\\n"  (trailing-backslash hard break)
	//   - "spaced_br"     → " <br> " (br con espacios)
	// Cualquier valor que no matchee alias se usa literal. Desde v0.40.1.
	GroupHeaderSep string `env:"QRSGEN_GROUP_HEADER_SEP" envDefault:"paragraph"`

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

	// RetroactiveNameUpdate: si true, qrsgen recuerda los mensajes
	// posteados al downstream con el formato "no saved" (tilde + push
	// name) y los reescribe vía PATCH cuando el dueño del bot añade el
	// contacto a su agenda WhatsApp. Estado in-memory por instancia —
	// un restart pierde el histórico tracked. Default true. Desde v0.40.0.
	RetroactiveNameUpdate bool `env:"QRSGEN_RETROACTIVE_NAME_UPDATE" envDefault:"true"`

	// RetroactiveCapPerSender: número máximo de mensajes recordados por
	// sender en el tracker de retroactive name update. Cuando se supera,
	// los más viejos caen FIFO. >100 da margen para que el update llegue
	// tras horas/días de mensajes acumulados. Default 200. Desde v0.40.0.
	RetroactiveCapPerSender int `env:"QRSGEN_RETROACTIVE_CAP_PER_SENDER" envDefault:"200"`

	// RetroactivePersist: si true, el tracker de retroactive name update
	// persiste sus entries en la tabla `bridge_msg_history` (Postgres).
	// El histórico sobrevive a restart y a deploys. false → modo in-memory
	// only (v0.40.0): un restart pierde los msgs tracked, retroactive
	// update no aplica a mensajes pre-restart. Default true. Desde v0.41.0.
	RetroactivePersist bool `env:"QRSGEN_RETROACTIVE_PERSIST" envDefault:"true"`

	// RetroactiveTTL: cuánto tiempo conservar las entries del retroactive
	// tracker en DB. Tras este TTL el cron de cleanup las borra (y al
	// próximo boot Warmup solo carga entries más recientes). Trade-off:
	// más TTL → más posibilidad de actualizar mensajes viejos cuando el
	// dueño finalmente añade el contacto a la agenda, pero más espacio
	// en DB. Default "720h" (30 días). Desde v0.41.0.
	RetroactiveTTL time.Duration `env:"QRSGEN_RETROACTIVE_TTL" envDefault:"720h"`
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
