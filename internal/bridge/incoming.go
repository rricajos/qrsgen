package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// InboxResolver resuelve el inbox_id de downstream para una instancia dada.
type InboxResolver func(instance string) int

// Incoming sincroniza un mensaje observado en WhatsApp (incoming o fromMe outgoing)
// a downstream.
//
// El `ds` es un Router (interfaz) — puede ser un *downstream.Client directo
// (single-downstream) o un *downstream.Registry (multi-downstream con
// routing por owner_tag). El código de los handlers usa `ds.For(ctx, instance)`
// uniformemente.
type Incoming struct {
	ds      downstream.Router
	dedup   *Deduper
	logger  *slog.Logger
	resolve InboxResolver
	usage   UsageRecorder

	// groupPrefixSender controla si los mensajes incoming de un grupo se
	// posteán a downstream con prefijo de remitente ("**~Name** · _+CC ..._").
	// Default true porque sin él, multi-sender en una misma conv del downstream
	// son indistinguibles. Se puede desactivar con QRSGEN_GROUP_PREFIX_SENDER=false.
	groupPrefixSender bool

	// groupTracker decide si emitir el header en cada mensaje del grupo.
	// Suprime el header si el sender es el mismo del mensaje anterior
	// (dentro del TTL configurable). nil = desactivado, header siempre.
	groupTracker *groupSenderTracker

	// avatarSync controla si qrsgen intenta sincronizar la foto de perfil
	// de WhatsApp al avatar del downstream. Aplica tanto al crear el
	// contact (primer sync) como al re-chequear contactos existentes
	// (refresh tras cambio en WA). Fire-and-forget, no bloquea el
	// flujo del mensaje. Default true.
	avatarSync bool

	// avatarTracker decide cuándo re-chequear el avatar de un JID
	// (TTL por defecto 24h) y evita races entre goroutines concurrentes
	// que reciben mensajes del mismo grupo en burst. nil = solo se sincroniza
	// al crear el contacto (sin refresh, modo v0.31.0).
	avatarTracker *avatarTracker

	// reactionsSync controla si las reacciones a mensajes de WhatsApp se
	// propagan al downstream como mensajes nuevos con formato
	// "**~Name** reaccionó con 👍". Default true. Desde v0.33.0.
	reactionsSync bool

	// typingSync controla si los eventos *events.ChatPresence (composing/
	// paused) se propagan al downstream como toggle_typing_status.
	// Default true. Desde v0.34.0.
	typingSync bool

	// typingTracker dedupea calls al downstream — eventos ChatPresence
	// pueden llegar varios por segundo durante typing activo. Default
	// minInterval 4s (Chatwoot UI normalmente expira typing tras ~5s).
	typingTracker *typingTracker

	// readReceiptsSync controla si los read receipts WhatsApp (cliente
	// abrió el chat y vio el msg del agente) actualizan el last_seen
	// del downstream. Default true. Desde v0.34.1.
	readReceiptsSync bool

	// waids es el tracker de WAIDs incoming por conv. Se popula desde
	// sync() y se drena desde el outgoing handler cuando llega un
	// evento conversation_updated del downstream. Si nil, no se rastrea
	// (feature mark-as-read desactivado). Desde v0.39.0.
	waids *waidTracker

	// msgHistory tracker de mensajes posteados para retroactive name
	// update cuando un contacto se añade a la agenda (v0.40.0). nil =
	// feature desactivado, no se rastrea ni se reescribe.
	msgHistory *msgHistoryTracker

	// retroactiveWG cuenta goroutines en vuelo aplicando PATCHes del
	// retroactive name update (v0.40.1). Permite a tests + graceful
	// shutdown esperar a que terminen — el work corre fuera del event
	// loop de wameow para no bloquearlo durante PATCHes secuenciales.
	retroactiveWG sync.WaitGroup

	// headerTemplate controla el render del header de sender (group
	// prefix + reactions). Tokens: $phone, $name. v0.45.0. Vacío = usa
	// el default `$phone · $name` (en backticks).
	headerTemplate string

	// reactionSep es el separador entre el header y el verb en las
	// reacciones. Default `\n` (single newline) — más compacto que el
	// `\n\n` del group prefix porque la reacción es visualmente más
	// atómica que un msg con cuerpo arbitrario. v0.45.1. Configurable
	// vía QRSGEN_REACTION_HEADER_SEP (mismos alias que GROUP_HEADER_SEP).
	reactionSep string

	// historyCfg configura el feature de history import (v0.46.0).
	// nil = feature desactivado (EnableHistoryImport no llamada).
	historyCfg *historyImportConfig

	// mentionTemplate controla el render de @-menciones inline. Vacío
	// desactiva la resolución (text con `@<jid_user>` raw). Default
	// `@$name`. Desde v0.53.0.
	mentionTemplate string

	// lidRefresher rastrea cuándo se refrescó el LID store de cada
	// grupo. Evita spammear GetGroupInfo cuando llegan muchas
	// menciones LID sin resolver. Desde v0.53.1.
	lidRefresher *lidRefreshTracker

	// reactionAsReply controla si las reacciones se postean al
	// downstream con `content_attributes.in_reply_to` apuntando al
	// msg target (visualmente queda como quote-reply en Chatwoot).
	// Permite saber a qué msg se reaccionó sin context extra.
	// Default true (chatwoot lo aprovecha). Desactivable si tu
	// downstream no soporta in_reply_to bien. Desde v0.53.2.
	reactionAsReply bool

	// chatAnchor trackea el último incoming conocido por chat (no
	// por sender). Distinto del msg_history tracker — indexa por
	// (instance, chatJID). Usado por history import on-demand como
	// fallback cuando el msg_history no tiene anchor para el chat.
	// nil = no inicializado (no rompe, solo desactiva la mejora).
	// Desde v0.49.0.
	chatAnchor *chatAnchorTracker

	// groupEventsEnabled activa el render de *events.GroupInfo,
	// JoinedGroup e IdentityChange como activity msgs en Chatwoot.
	// Default false (opt-in via env QRSGEN_GROUP_EVENTS_ENABLED). v0.47.0.
	groupEventsEnabled bool

	// onDemandLatch sincroniza requests on-demand de history sync con
	// el HandleHistorySync callback. Cada request adquiere un latch
	// (chan) indexado por (instance, chat) y libera al recibir el
	// payload ON_DEMAND correspondiente.
	onDemandLatch *onDemandLatchSet

	// headerSep es el separador entre el header (`+phone · name`) y el
	// body en mensajes posteados al downstream. Configurable porque
	// ningún renderer markdown se comporta igual:
	//   - v0.39.7 "\n":      soft break en Chatwoot → inline
	//   - v0.39.8 "  \n":    CommonMark hard break, Chatwoot lo ignora
	//   - v0.39.9 "\n\n":    paragraph break, funciona pero deja aire
	//   - v0.39.10 "<br>":   Chatwoot lo trata como autolink, render
	//                        sale como <code>br</code>
	// Default "\n\n" en v0.40.1 porque es lo único que renderiza
	// fiable. Configurable vía SetHeaderSep + env QRSGEN_GROUP_HEADER_SEP.
	headerSep string
}

// NewIncomingDynamic crea un handler con resolución dinámica de inbox por instancia.
func NewIncomingDynamic(ds downstream.Router, dedup *Deduper, logger *slog.Logger, resolve InboxResolver) *Incoming {
	return &Incoming{
		ds: ds, dedup: dedup, logger: logger, resolve: resolve,
		groupPrefixSender: true,
		avatarSync:        true,
		reactionsSync:     true,
		typingSync:        true,
		typingTracker:     newTypingTracker(4 * time.Second),
		readReceiptsSync:  true,
		headerSep:         GroupHeaderSepParagraph,
		headerTemplate:    GroupHeaderTemplateDefault,
		reactionSep:       GroupHeaderSepSoftNL, // `\n` por defecto (v0.45.1)
		onDemandLatch:     newOnDemandLatchSet(),
		chatAnchor:        newChatAnchorTracker(),
		mentionTemplate:   MentionTemplateDefault,
		lidRefresher:      newLIDRefreshTracker(),
		reactionAsReply:   true,
	}
}

// SetReactionAsReply controla si las reacciones se postean con
// `content_attributes.in_reply_to` apuntando al msg target.
// v0.53.2.
func (i *Incoming) SetReactionAsReply(v bool) { i.reactionAsReply = v }

// SetMentionTemplate cambia el template usado para resolver
// @-menciones inline. Vacío desactiva la feature (texto raw).
// Tokens: $name, $phone. v0.53.0.
func (i *Incoming) SetMentionTemplate(template string) {
	i.mentionTemplate = template
}

// SetChatAnchorPool habilita persistencia DB del chat_anchor tracker
// (v0.49.0). Llamar tras EnsureChatAnchorSchema.
func (i *Incoming) SetChatAnchorPool(pool *pgxpool.Pool, logger *slog.Logger) {
	if i.chatAnchor == nil {
		return
	}
	i.chatAnchor.SetPool(pool, logger)
}

// WarmupChatAnchor carga el cache desde DB. Llamar al boot.
func (i *Incoming) WarmupChatAnchor(ctx context.Context, keep time.Duration) error {
	if i.chatAnchor == nil {
		return nil
	}
	return i.chatAnchor.Warmup(ctx, keep)
}

// CleanupChatAnchorOld borra entries DB más viejas que `keep`.
func (i *Incoming) CleanupChatAnchorOld(ctx context.Context, keep time.Duration) (int64, error) {
	if i.chatAnchor == nil {
		return 0, nil
	}
	return i.chatAnchor.CleanupOld(ctx, keep)
}

// SetReactionSep cambia el separador entre header y verb en
// reacciones (v0.45.1). Pasar "" mantiene el default (`\n`).
func (i *Incoming) SetReactionSep(sep string) {
	if sep == "" {
		i.reactionSep = GroupHeaderSepSoftNL
		return
	}
	i.reactionSep = sep
}

// SetHeaderTemplate cambia el template del header de sender (group
// prefix + reactions). v0.45.0. Pasar "" mantiene el default.
// Tokens: $phone, $name. El `~` para no-saved es automático.
func (i *Incoming) SetHeaderTemplate(template string) {
	if template == "" {
		i.headerTemplate = GroupHeaderTemplateDefault
		return
	}
	i.headerTemplate = template
}

// Variantes del separador header/body para SetHeaderSep. Cada una se
// comporta diferente según el renderer markdown del downstream.
// Probar varias si el default (`paragraph`) no es satisfactorio.
const (
	// GroupHeaderSepParagraph = "\n\n". Paragraph break estándar
	// markdown — funciona en Chatwoot pero deja aire entre header y
	// body (separación clara de párrafos). Default en v0.40.1.
	GroupHeaderSepParagraph = "\n\n"

	// GroupHeaderSepBr = "<br>". HTML inline break. En Chatwoot
	// observado: el parser lo trata como autolink y renderiza
	// como `<code>br</code>`. NO RECOMENDADO salvo que tu downstream
	// soporte HTML allowlist.
	GroupHeaderSepBr = "<br>"

	// GroupHeaderSepBrSelf = "<br/>". Self-closing XHTML. Algunos
	// sanitizadores tratan distinto que `<br>`. Probar si br no
	// funciona pero quieres line break sin paragraph.
	GroupHeaderSepBrSelf = "<br/>"

	// GroupHeaderSepLSep = " ". Unicode LINE SEPARATOR (U+2028).
	// Bypaseas markdown — el browser renderiza nativamente como salto.
	// Probar si markdown-based fallan; soporte por navegador es amplio.
	GroupHeaderSepLSep = " "

	// GroupHeaderSepSoftNL = "\n". Soft break markdown. En renderers
	// con `breaks: true` (modo chat) sale como <br>. En Chatwoot
	// observado (v0.39.7): inline (no break).
	GroupHeaderSepSoftNL = "\n"

	// GroupHeaderSepSlashNL = "\\\n". Trailing backslash hard break,
	// alternativa CommonMark a "  \n". Algunos parsers solo
	// implementan una de las dos. Probar si "  \n" no funcionó.
	GroupHeaderSepSlashNL = "\\\n"

	// GroupHeaderSepSpacedBr = " <br> ". Br con espacios — intenta
	// evitar que el parser autolink-matchee `<br>` pegado al
	// backtick de cierre del code block.
	GroupHeaderSepSpacedBr = " <br> "
)

// SetHeaderSep cambia el separador header/body. Llamar al boot tras
// NewIncomingDynamic. Pasar "" mantiene el default (\n\n).
// Usar las constantes GroupHeaderSep* para legibilidad.
func (i *Incoming) SetHeaderSep(sep string) {
	if sep == "" {
		i.headerSep = GroupHeaderSepParagraph
		return
	}
	i.headerSep = sep
}

// ResolveHeaderSep mapea el alias env-friendly al valor literal. Si el
// alias no matchea, devuelve el alias tal cual (permite pasar un
// separador arbitrario directamente vía env).
func ResolveHeaderSep(alias string) string {
	switch alias {
	case "paragraph", "p", "":
		return GroupHeaderSepParagraph
	case "br":
		return GroupHeaderSepBr
	case "br_self", "br/":
		return GroupHeaderSepBrSelf
	case "lsep", "u2028":
		return GroupHeaderSepLSep
	case "nl", "soft":
		return GroupHeaderSepSoftNL
	case "slash", "slash_nl":
		return GroupHeaderSepSlashNL
	case "spaced_br":
		return GroupHeaderSepSpacedBr
	default:
		return alias
	}
}

// SetReactionsSync activa o desactiva la propagación de reacciones WhatsApp
// al downstream. Default true. Setear a false ignora todos los eventos
// de reacción — no se postea nada en la conv del downstream.
func (i *Incoming) SetReactionsSync(v bool) { i.reactionsSync = v }

// SetTypingSync activa o desactiva la propagación de ChatPresence (typing
// indicators) al downstream. Default true. Setear a false ignora todos
// los eventos — la UI del downstream no muestra "está escribiendo".
func (i *Incoming) SetTypingSync(v bool) { i.typingSync = v }

// SetReadReceiptsSync activa o desactiva la propagación de read receipts
// WhatsApp (cliente abrió el chat) al downstream. Default true.
func (i *Incoming) SetReadReceiptsSync(v bool) { i.readReceiptsSync = v }

// EnableMarkAsRead activa el tracker de WAIDs y devuelve un puntero al
// mismo para que el outgoing handler pueda drenarlo cuando llegan los
// eventos conversation_updated del downstream. Llamar antes de procesar
// mensajes — si no se llama, el tracker queda nil y los WAIDs no se
// rastrean (feature mark-as-read desactivado).
func (i *Incoming) EnableMarkAsRead() *waidTracker {
	if i.waids == nil {
		i.waids = newWAIDTracker(50)
	}
	return i.waids
}

// EnableRetroactiveNameUpdate activa el tracker de mensajes posteados
// para reescribir su content cuando el sender pase de no-saved a saved
// (o cambie de nombre). cap es el número máximo de mensajes que
// recordamos por sender; >100 da margen razonable para que el
// retroactive update encuentre msgs viejos cuando el contacto se añade
// tras horas/días.
func (i *Incoming) EnableRetroactiveNameUpdate(capPerSender int) {
	if i.msgHistory == nil {
		i.msgHistory = newMsgHistoryTracker(capPerSender)
	}
}

// SetRetroactivePool habilita persistencia DB del tracker (v0.41.0).
// El estado sobrevive a restarts. Llamar tras EnableRetroactiveNameUpdate
// + EnsureMsgHistorySchema. Sin esto, el tracker queda in-memory only.
func (i *Incoming) SetRetroactivePool(pool *pgxpool.Pool, logger *slog.Logger) {
	if i.msgHistory == nil {
		return
	}
	i.msgHistory.SetPool(pool, logger)
}

// WarmupRetroactive carga el histórico tracked desde DB. Llamar al
// boot tras SetRetroactivePool. keep limita qué tan viejas pueden ser
// las entries cargadas — más antiguas se ignoran (no se borran de DB,
// para eso está CleanupRetroactiveOld).
func (i *Incoming) WarmupRetroactive(ctx context.Context, keep time.Duration) error {
	if i.msgHistory == nil {
		return nil
	}
	return i.msgHistory.Warmup(ctx, keep)
}

// CleanupRetroactiveOld borra entries DB más viejas que `keep`. Devuelve
// el número de filas eliminadas. Llamar periódicamente vía cron.
func (i *Incoming) CleanupRetroactiveOld(ctx context.Context, keep time.Duration) (int64, error) {
	if i.msgHistory == nil {
		return 0, nil
	}
	return i.msgHistory.CleanupOld(ctx, keep)
}

// DropInstanceTracking borra todo el estado tracked en msg_history para
// la instancia dada. Llamar desde Manager.Delete para evitar leak: las
// entries in-memory + filas DB se acumulan si las instancias churn.
// v0.53.3.
func (i *Incoming) DropInstanceTracking(ctx context.Context, instance string) (int64, error) {
	if i.msgHistory == nil {
		return 0, nil
	}
	return i.msgHistory.DropInstance(ctx, instance)
}

// SetUsage attaches a usage recorder. Pass nil to disable.
func (i *Incoming) SetUsage(u UsageRecorder) { i.usage = u }

// SetGroupPrefixSender controla el prefijo de remitente en mensajes de grupo.
func (i *Incoming) SetGroupPrefixSender(v bool) { i.groupPrefixSender = v }

// SetGroupHeaderTTL activa la supresión de header en mensajes consecutivos
// del mismo sender dentro del TTL dado. ttl=0 desactiva la feature
// (header siempre).
func (i *Incoming) SetGroupHeaderTTL(ttl time.Duration) {
	if ttl <= 0 {
		i.groupTracker = nil
		return
	}
	i.groupTracker = newGroupSenderTracker(ttl)
}

// SetAvatarSync controla si tras crear un contacto en downstream qrsgen
// dispara una sincronización (background) de la foto WhatsApp como avatar
// del contacto. Default true.
func (i *Incoming) SetAvatarSync(v bool) { i.avatarSync = v }

// SetAvatarRefreshTTL activa el refresh periódico del avatar (mismo TTL
// para todos los JIDs). Si > 0, contactos existentes se re-chequean
// cada TTL para detectar cambios de foto en WhatsApp; 0 = sin refresh
// (solo sync al crear contact, como en v0.31.0).
func (i *Incoming) SetAvatarRefreshTTL(ttl time.Duration) {
	if ttl <= 0 {
		i.avatarTracker = nil
		return
	}
	i.avatarTracker = newAvatarTracker(ttl)
}

// ResyncResult resume el bulk-resync de avatars.
type ResyncResult struct {
	Instance string `json:"instance"`
	Scanned  int    `json:"scanned"`  // contactos totales iterados
	Skipped  int    `json:"skipped"`  // identifier no parseable como JID
	Queued   int    `json:"queued"`   // syncs lanzados en background
	Pages    int    `json:"pages"`    // páginas consultadas en downstream
}

// ResyncInstanceAvatars itera todos los contactos del inbox asociado a la
// instancia y dispara un avatar sync por cada uno que tenga identifier
// parseable como JID. BYPASS del tracker — cada contacto se chequea sí o sí.
//
// Útil para backfillear contactos viejos (creados antes de v0.31.0) que
// todavía tienen letter-avatar autogenerado y no han recibido mensajes
// recientes que disparen el sync vía sync().
//
// La iteración bloquea hasta terminar pero los syncs por contacto son
// goroutines fire-and-forget. Para inboxes grandes (>1k contactos)
// considera invocar con cuidado — N goroutines concurrentes pueden
// estresar tanto al server WA como al downstream.
func (i *Incoming) ResyncInstanceAvatars(ctx context.Context, instance string, r wameow.WAResolver, inboxID int) (ResyncResult, error) {
	result := ResyncResult{Instance: instance}
	if !i.avatarSync || r == nil {
		return result, fmt.Errorf("avatar sync disabled")
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return result, fmt.Errorf("downstream not configured for instance %s", instance)
	}
	if inboxID <= 0 {
		return result, fmt.Errorf("inbox id required (got %d)", inboxID)
	}

	page := 1
	for {
		contacts, hasMore, err := ds.ListContactsByInbox(ctx, inboxID, page)
		if err != nil {
			return result, fmt.Errorf("list contacts page %d: %w", page, err)
		}
		result.Pages++
		for _, contact := range contacts {
			result.Scanned++
			jid, parseErr := types.ParseJID(contact.Identifier)
			if parseErr != nil {
				result.Skipped++
				continue
			}
			// Forzar sync ignorando tracker — copiar contact.ID a variable
			// local para que la goroutine no comparta el loop var (Go 1.22+
			// ya no lo necesita pero por claridad).
			cid := contact.ID
			j := jid
			if i.avatarTracker != nil {
				// Forzar re-check: limpiar LastID para que syncAvatar
				// detecte cambio y descargue.
				i.avatarTracker.UpdateID(instance, j.String(), "")
			}
			// #nosec G118 -- async avatar sync goroutine intentionally outlives request frame; ctx is request-scoped wallet
			go i.syncAvatar(ds, r, cid, j, instance)
			result.Queued++
		}
		if !hasMore {
			break
		}
		page++
		// Safety: no más de 200 páginas (3000 contactos). Si tienes más,
		// implementa cursor en lugar de page-based.
		if page > 200 {
			i.logger.Warn("resync avatars: page cap reached, stopping",
				"instance", instance, "pages", page, "scanned", result.Scanned)
			break
		}
	}
	i.logger.Info("resync avatars done",
		"instance", instance, "scanned", result.Scanned,
		"skipped", result.Skipped, "queued", result.Queued, "pages", result.Pages)
	return result, nil
}

// handleReaction extraído a incoming_reactions.go en v0.56.0.

// HandleReceipt reacciona a *events.Receipt. Solo procesamos kind="read"
// (cliente abrió el chat y vio los msgs enviados por el agente). Otros
// tipos (delivered, played, sender, read-self) los logueamos pero no
// los propagamos al downstream — son menos accionables.
//
// Para read, actualizamos contact_last_seen_at del conv en el downstream
// usando el timestamp del receipt. La UI del downstream renderiza el
// "doble check azul" en los mensajes del agente que están dentro de
// ese rango temporal.
func (i *Incoming) HandleReceipt(ctx context.Context, instance string, chat types.JID, sender types.JID, kind string, messageIDs []string, ts time.Time, r wameow.WAResolver) {
	if !i.readReceiptsSync {
		return
	}
	// Solo procesamos read y read-self. Los demás (delivered, played,
	// sender) tienen menos valor accionable para el agente.
	if kind != string(types.ReceiptTypeRead) && kind != string(types.ReceiptTypeReadSelf) {
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "filtered", instance).Inc()
		return
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}

	identifier := chat.ToNonAD().String()
	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil || contact == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "no_contact", instance).Inc()
		return
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "no_conv", instance).Inc()
		return
	}

	if err := ds.UpdateContactLastSeen(ctx, conv.ID, ts); err != nil {
		i.logger.Warn("read receipt: update_last_seen failed",
			"err", err, "conv_id", conv.ID, "ts", ts)
		metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "ds_error", instance).Inc()
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("read_receipt", "ok", instance).Inc()
	i.logger.Debug("read receipt synced",
		"conv_id", conv.ID, "ts", ts, "messages_count", len(messageIDs),
		"kind", kind, "instance", instance)
}

// HandleChatPresence reacciona a *events.ChatPresence (typing/paused).
// Propaga al downstream el toggle_typing_status correspondiente, con
// throttle del typingTracker para no saturar.
//
// chat es el JID del chat/grupo. sender es el JID del participante que
// tipea (en grupo) o igual a chat (en 1-on-1). En grupos puede haber
// múltiples participantes typing a la vez — el downstream solo soporta
// un indicator por conv, así que se reciben todos como "alguien está
// escribiendo".
//
// Para JIDs sin contact registrado, no hace nada (no podemos resolver
// la conv).
func (i *Incoming) HandleChatPresence(ctx context.Context, instance string, chat types.JID, sender types.JID, composing bool, media string, r wameow.WAResolver) {
	if !i.typingSync {
		return
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}

	identifier := chat.ToNonAD().String()
	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil || contact == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "no_contact", instance).Inc()
		return
	}
	inboxID := i.resolve(instance)
	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil || conv == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "no_conv", instance).Inc()
		return
	}

	if i.typingTracker != nil && !i.typingTracker.ShouldEmit(conv.ID, composing) {
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "throttled", instance).Inc()
		return
	}

	if err := ds.SetTypingStatus(ctx, conv.ID, composing); err != nil {
		i.logger.Warn("typing sync: SetTypingStatus failed",
			"err", err, "conv_id", conv.ID, "composing", composing)
		metrics.RealtimeEventsTotal.WithLabelValues("typing", "ds_error", instance).Inc()
		return
	}
	metrics.RealtimeEventsTotal.WithLabelValues("typing", "ok", instance).Inc()
	i.logger.Debug("typing synced",
		"conv_id", conv.ID, "composing", composing, "media", media,
		"chat", chat.String(), "sender", sender.String(), "instance", instance)
}

// HandlePictureChange reacciona a *events.Picture: alguien (user o grupo)
// cambió su foto de perfil. Encuentra el contact correspondiente en el
// downstream y dispara avatar sync. A diferencia de maybeAvatarSync,
// IGNORA el TTL — el evento es la señal canónica de que cambió.
//
// Si el contact no existe en downstream todavía, no hace nada: al primer
// mensaje del JID, sync()→CreateContact→maybeAvatarSync hará el sync inicial.
func (i *Incoming) HandlePictureChange(ctx context.Context, instance string, jid types.JID, pictureID string, removed bool, r wameow.WAResolver) {
	if !i.avatarSync || r == nil {
		return
	}
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return
	}
	identifier := jid.ToNonAD().String()

	contact, err := findContactByIdentifier(ctx, ds, identifier, "")
	if err != nil {
		i.logger.Warn("picture event: find contact failed",
			"err", err, "jid", identifier)
		return
	}
	if contact == nil {
		// El contact no existe en downstream. Al primer mensaje de este
		// JID, sync() lo creará y disparará el avatar sync inicial.
		return
	}

	// Reset del LastID en el tracker para forzar re-descarga: el evento
	// nos dice que cambió, así que el cached ID está stale.
	if i.avatarTracker != nil {
		i.avatarTracker.UpdateID(instance, identifier, "")
	}

	i.logger.Info("picture changed event — forcing avatar resync",
		"jid", identifier, "new_picture_id", pictureID, "removed", removed,
		"contact_id", contact.ID)

	// #nosec G118 -- async avatar sync goroutine intentionally outlives event-handler frame
	go i.syncAvatar(ds, r, contact.ID, jid, instance)
}

// maybeAvatarSync decide si lanzar el sync de avatar para un JID. Si el
// tracker dice que toca (primera vez, o TTL expirado), spawnea goroutine
// fire-and-forget. Si no, no hace nada.
//
// Llamarlo tanto al crear contacto nuevo COMO al encontrar contacto
// existente — el tracker se encarga de la lógica de "cuándo".
func (i *Incoming) maybeAvatarSync(ds *downstream.Client, r wameow.WAResolver, contactID int, jid types.JID, instance string) {
	if !i.avatarSync || r == nil || ds == nil {
		return
	}
	// Sin tracker = modo v0.31.0 sin refresh — solo cuando se crea contacto
	// (este callsite). Si el caller distingue create/found, gate ahí.
	if i.avatarTracker != nil && !i.avatarTracker.ShouldCheck(instance, jid.String()) {
		return
	}
	go i.syncAvatar(ds, r, contactID, jid, instance)
}

// syncAvatar es el worker que corre en goroutine. Lógica:
//   1. Get current ID via GetProfilePictureID (cheap, solo metadata).
//   2. Si ID == lastKnownID → no descarga, solo registra "checked".
//   3. Si ID == "" → no hay foto. Cachea y termina.
//   4. Si ID distinto → download + upload + update tracker.
//
// Errores se loguean como warning; el tracker ya marcó el timestamp
// en ShouldCheck para no retry inmediato.
func (i *Incoming) syncAvatar(ds *downstream.Client, r wameow.WAResolver, contactID int, jid types.JID, instance string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	currentID, err := r.GetProfilePictureID(ctx, jid)
	if err != nil {
		i.logger.Warn("avatar sync: get id failed",
			"err", err, "contact_id", contactID, "jid", jid.String())
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_error", instance).Inc()
		return
	}
	// Sin foto WA: nada que subir, cacheamos el "" para no chequear hasta TTL.
	if currentID == "" {
		if i.avatarTracker != nil {
			i.avatarTracker.UpdateID(instance, jid.String(), "")
		}
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_miss", instance).Inc()
		return
	}
	// Avatar idéntico al último sincronizado: no re-descargar.
	if i.avatarTracker != nil {
		if last := i.avatarTracker.LastID(instance, jid.String()); last == currentID {
			metrics.RealtimeEventsTotal.WithLabelValues("avatar", "throttled", instance).Inc()
			return
		}
	}

	data, mime, err := r.GetProfilePicture(ctx, jid)
	if err != nil {
		i.logger.Warn("avatar sync: download failed",
			"err", err, "contact_id", contactID, "jid", jid.String())
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_error", instance).Inc()
		return
	}
	if len(data) == 0 {
		// Edge: tenía ID pero descarga vacía. Cacheamos como sin foto.
		if i.avatarTracker != nil {
			i.avatarTracker.UpdateID(instance, jid.String(), "")
		}
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "wa_miss", instance).Inc()
		return
	}
	if err := ds.UploadContactAvatar(ctx, contactID, data, mime); err != nil {
		i.logger.Warn("avatar sync: upload to downstream failed",
			"err", err, "contact_id", contactID, "size", len(data))
		metrics.RealtimeEventsTotal.WithLabelValues("avatar", "ds_error", instance).Inc()
		return
	}
	if i.avatarTracker != nil {
		i.avatarTracker.UpdateID(instance, jid.String(), currentID)
	}
	metrics.RealtimeEventsTotal.WithLabelValues("avatar", "ok", instance).Inc()
	i.logger.Info("avatar synced",
		"contact_id", contactID, "size", len(data), "mime", mime,
		"avatar_id", currentID, "jid", jid.String())
}

func (i *Incoming) incUsageIn(instance string) {
	if i.usage != nil {
		i.usage.IncIn(instance)
	}
}

// resolvedSender captura el shape real del remitente WhatsApp Multi-Device:
// puede llegar como PN (teléfono) o LID (identificador anónimo). Conservamos
// ambos sin enmascarar para que downstream refleje la realidad.
type resolvedSender struct {
	addressingMode string // "pn" | "lid" | ""
	primaryJID     string // el JID por el que enrutamos (canonical: PN si lo conocemos, si no LID)
	pnJID          string // JID PN (..@s.whatsapp.net) si está disponible
	lidJID         string // JID LID (..@lid) si está disponible
	phone          string // teléfono REAL en E.164 (sin +) si existe PN, vacío en caso LID-only
}

func resolveSender(info *events.Message, r wameow.WAResolver) resolvedSender {
	src := info.Info.MessageSource
	chat := src.Chat
	alt := src.SenderAlt

	mode := string(src.AddressingMode)

	var pn, lid types.JID
	switch chat.Server {
	case types.DefaultUserServer: // s.whatsapp.net
		pn = chat.ToNonAD()
		if alt.Server == types.HiddenUserServer {
			lid = alt.ToNonAD()
		}
	case types.HiddenUserServer: // lid
		lid = chat.ToNonAD()
		if alt.Server == types.DefaultUserServer {
			pn = alt.ToNonAD()
		}
	default:
		return resolvedSender{addressingMode: mode, primaryJID: chat.String()}
	}

	// Si solo conocemos una forma, intentar mapearla a la otra vía LIDStore.
	// Esto da identidad estable entre direcciones (DEVOPS→Ricard llega solo con LID,
	// Ricard→DEVOPS llega con LID+PN; sin este mapeo creamos contactos duplicados).
	if r != nil {
		if pn.IsEmpty() && !lid.IsEmpty() {
			if mapped, ok := r.PNForLID(lid); ok {
				pn = mapped.ToNonAD()
			}
		}
		if lid.IsEmpty() && !pn.IsEmpty() {
			if mapped, ok := r.LIDForPN(pn); ok {
				lid = mapped.ToNonAD()
			}
		}
	}

	rs := resolvedSender{addressingMode: mode}
	// Preferimos PN como identificador canónico cuando lo conocemos.
	if !pn.IsEmpty() {
		rs.pnJID = pn.String()
		rs.primaryJID = rs.pnJID
		rs.phone = pn.User
	} else {
		rs.primaryJID = lid.String()
	}
	if !lid.IsEmpty() {
		rs.lidJID = lid.String()
	}
	return rs
}

// Handle procesa un evento Message de whatsmeow.
func (i *Incoming) Handle(ctx context.Context, instance string, msg *events.Message, r wameow.WAResolver) {
	// Filtro defensivo: solo procesamos chats 1-on-1 (PN/LID) y grupos.
	// Broadcasts (status), newsletter (canales), bot — se ignoran porque
	// no son conversaciones agente↔cliente y crearían contactos basura.
	if !isSupportedChatServer(msg.Info.Chat.Server) {
		i.logger.Debug("event from unsupported chat server — ignorado",
			"id", msg.Info.ID, "instance", instance, "server", msg.Info.Chat.Server)
		return
	}
	// Broadcast: status@broadcast es un caso especial (el sender publica un
	// estado que llega como event aunque chat.Server=="s.whatsapp.net"). Lo
	// detectamos por el método del SDK.
	if msg.Info.IsIncomingBroadcast() {
		i.logger.Debug("incoming broadcast — ignorado",
			"id", msg.Info.ID, "instance", instance, "chat", msg.Info.Chat.String())
		return
	}

	// Reactions: si el mensaje es una reacción a otro mensaje, lo manejamos
	// por un path distinto (postea actividad en la conv, no mensaje normal).
	if msg.Message != nil && msg.Message.GetReactionMessage() != nil {
		i.handleReaction(ctx, instance, msg, r)
		return
	}

	text := extractTextContent(msg)
	media := extractMedia(msg)
	if text == "" && media == nil {
		i.logger.Debug("event sin texto ni media — ignorado", "id", msg.Info.ID, "instance", instance)
		return
	}
	// content efectivo = texto plano O caption del media. Si solo es media sin
	// caption, content queda vacío y downstream muestra solo el adjunto.
	content := text
	if content == "" && media != nil {
		content = media.caption
	}

	// v0.53.0: resolver menciones @<jid_user> a @<nombre> usando el
	// ContextInfo.MentionedJID array. Sin esto, el agente ve
	// `@148855681191942` en lugar de `@~Ivan Madrid`. La feature está
	// activa por default (template `@$name`); desactivable poniendo
	// QRSGEN_MENTION_TEMPLATE="".
	//
	// v0.53.1: si llega una mención LID que no tiene PN mapeado en
	// el LID store local, hacemos un GetGroupInfo on-demand (con
	// TTL 1h por grupo) que side-effects la actualización del store.
	// Después de eso, PNForLID puede resolver y mostramos `@+34...`
	// en lugar del LID raw.
	if content != "" && i.mentionTemplate != "" {
		if ci := extractContextInfo(msg.Message); ci != nil {
			mentioned := ci.GetMentionedJID()
			if len(mentioned) > 0 {
				maybeRefreshLIDs(msg.Info.Chat, mentioned, r, i.lidRefresher)
				content = resolveMentions(content, mentioned, r, i.mentionTemplate)
			}
		}
	}

	// v0.42.0: si el mensaje es un reply (ContextInfo con QuotedMessage),
	// prefijar el body con un blockquote del mensaje citado. Da contexto
	// al agente para saber a qué se está respondiendo sin tener que
	// buscar el msg original arriba en la conv.
	// v0.44.2: separador entre el blockquote y el reply body baja a
	// "\n" (era "\n\n") — el doble salto generaba un gap excesivo
	// entre el citado y la respuesta. Chatwoot cierra el blockquote
	// con el primer \n no-> y mantiene el reply pegado debajo.
	if quoted := formatQuotedBlock(msg, r); quoted != "" {
		if content == "" {
			content = quoted
		} else {
			content = quoted + "\n" + content
		}
	}

	rs := resolveSender(msg, r)
	fromMe := msg.Info.IsFromMe
	isGroup := msg.Info.Chat.Server == types.GroupServer

	otherJID := msg.Info.Chat.ToNonAD()
	contactName := ""
	if r != nil {
		if isGroup {
			// Para grupos el "contact" sintético en downstream representa al
			// grupo entero — su nombre visible debe ser el subject del grupo,
			// no el push name del primer participante que mande mensaje.
			if subj, ok := r.GroupSubject(otherJID); ok {
				contactName = subj
			}
		} else {
			contactName = r.ContactName(otherJID)
			// Si chat es LID, intenta también el PN para sacar el nombre (a veces
			// el contact store está poblado solo en una de las dos formas).
			if contactName == "" && otherJID.Server == types.HiddenUserServer {
				if pn, ok := r.PNForLID(otherJID); ok {
					contactName = r.ContactName(pn)
				}
			}
		}
	}
	if contactName == "" && !fromMe && !isGroup {
		// Sin group subject conocido caemos a "" (sync usa pickName con phone/LID).
		// Solo aplicamos push name a 1-on-1 — en grupo el push name es del
		// participante, no del grupo, y daría título incorrecto a la conv.
		contactName = msg.Info.PushName
	}

	// rawBody preserva el body sin prefix para guardarlo en msgHistory
	// (retroactive name update, v0.40.0). Si el prefix no se emite,
	// rawBody == content.
	rawBody := content
	var emittedSenderInfo senderInfo
	emittedPrefix := false

	// En grupos, prefijar la identidad del participante al body para que
	// múltiples senders dentro de la misma conv del downstream sean
	// distinguibles. Solo aplica a mensajes incoming (los fromMe del agente
	// no necesitan prefijo).
	//
	// Si groupTracker está activo, suprime el header cuando el sender es
	// el mismo del mensaje anterior dentro del TTL — replica el "burst"
	// visual de WhatsApp donde solo el primer msg del grupo lleva header.
	// Siempre registramos el sender (incluyendo fromMe, usando "_bot" como
	// JID sintético) para que el siguiente mensaje real reciba header
	// correctamente tras una intervención del agente.
	if i.groupPrefixSender && isGroup {
		senderKey := msg.Info.Sender.String()
		if fromMe {
			senderKey = "_bot"
		}
		shouldEmit := true
		if i.groupTracker != nil {
			shouldEmit = i.groupTracker.RecordAndCheck(instance, msg.Info.Chat.String(), senderKey)
		}
		if !fromMe && shouldEmit {
			si := resolveSenderInfo(msg, r)
			if prefix, ok := renderSenderHeader(si, i.headerTemplate); ok {
				if content == "" {
					content = prefix
				} else {
					content = prefix + i.headerSep + content
				}
				emittedSenderInfo = si
				emittedPrefix = true
			}
		}
	}

	metrics.MessagesTotal.WithLabelValues("in", instance, i.ds.OwnerTagFor(ctx, instance)).Inc()
	i.incUsageIn(instance)
	i.logger.Info("incoming whatsapp",
		"instance", instance,
		"fromMe", fromMe,
		"mode", rs.addressingMode,
		"pn", rs.pnJID,
		"lid", rs.lidJID,
		"chat", msg.Info.Chat.String(),
		"pushNameEvent", msg.Info.PushName,
		"resolvedName", contactName,
		"waID", msg.Info.ID,
	)

	if fromMe {
		drop, err := i.dedup.ShouldDrop(ctx, instance, rs.primaryJID, content)
		if err != nil {
			i.logger.Error("dedup check failed", "err", err)
		} else if drop {
			i.logger.Warn("dropped duplicate (likely LID twin)",
				"instance", instance, "primaryJID", rs.primaryJID, "content_len", len(content))
			return
		}
	}

	inboxID := i.resolve(instance)
	var postedMsgID, postedConvID int
	if err := i.sync(ctx, instance, inboxID, rs, content, fromMe, contactName, msg.Info.ID, media, r, msg.Message, &postedMsgID, &postedConvID); err != nil {
		i.logger.Error("sync to downstream failed", "err", err, "instance", instance, "primaryJID", rs.primaryJID)
		return
	}

	// v0.40.0: si emitimos prefix de grupo, registramos en msgHistory
	// para poder reescribir el header si el sender pasa de no-saved a
	// saved (o cambia de nombre) más tarde.
	// v0.44.0: ahora registramos TODOS los incoming (no solo los con
	// prefix) para mapear Chatwoot msgID ↔ WAID — necesario para
	// reply-to outgoing. hasPrefix discrimina cuáles aplican al
	// retroactive PATCH loop.
	if i.msgHistory != nil && postedMsgID != 0 && postedConvID != 0 && !fromMe {
		trackerKey := canonicalSenderKey(msg.Info.Sender, r)
		tm := trackedMsg{
			convID:    postedConvID,
			msgID:     postedMsgID,
			body:      rawBody,
			postedAt:  time.Now(),
			waid:      msg.Info.ID,
			hasPrefix: emittedPrefix,
		}
		if emittedPrefix {
			tm.phone = emittedSenderInfo.phoneFmt
			tm.nameUsed = emittedSenderInfo.name
			tm.wasSaved = emittedSenderInfo.saved
		}
		i.msgHistory.Record(instance, trackerKey, tm)
	}

	// v0.49.0: chat_anchor tracker — registramos el último msg conocido
	// por chat (no por sender). Cubre TODOS los incoming, también 1:1,
	// y sirve como fuente de anchor para el history import on-demand.
	if i.chatAnchor != nil && !fromMe {
		chatKey := msg.Info.Chat.ToNonAD().String()
		ts := msg.Info.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		i.chatAnchor.Record(instance, chatKey, msg.Info.ID, ts)
	}
}

// canonicalSenderKey devuelve el JID que se usará como índice en
// msgHistory. Cuando el sender es LID, intentamos resolver al PN
// (events.Contact llega como PN, no como LID — para que el lookup
// retroactivo encuentre los mensajes). Si no hay PN resoluble, cae
// al primary JID (LID a secas).
func canonicalSenderKey(sender types.JID, r wameow.WAResolver) string {
	s := sender.ToNonAD()
	if s.Server == types.HiddenUserServer && r != nil {
		if pn, ok := r.PNForLID(s); ok {
			return pn.String()
		}
	}
	return s.String()
}

// replyToTracker expone el tracker de msg_history a outras packages
// internas del bridge (outgoing.go usa esto para resolver Chatwoot
// msgID → WAID al recibir un webhook con in_reply_to). Devuelve nil
// si la feature retroactive name update no fue habilitada (en cuyo
// caso el reply-to outgoing también queda desactivado).
func (i *Incoming) replyToTracker() *msgHistoryTracker { return i.msgHistory }

// MarkBotSentInGroup registra al "bot" como último sender de un
// grupo en el groupTracker. Llamar desde Outgoing tras un send
// exitoso a un grupo — sin esto, el bot reply NO resetea el burst
// (whatsmeow no emite *events.Message para envíos del mismo cliente,
// así que el flow de Incoming.Handle nunca ve el bot como un msg
// más del grupo). Sin este reset, el siguiente msg del usuario tras
// el bot reply heredaba la supresión del header en lugar de mostrar
// "X · Nombre" como nuevo burst. Bug observado en v0.30.0..v0.44.0.
//
// No-op si groupTracker no está activo (TTL=0).
func (i *Incoming) MarkBotSentInGroup(instance, chatJID string) {
	if i.groupTracker == nil {
		return
	}
	i.groupTracker.RecordAndCheck(instance, chatJID, "_bot")
}

// WaitRetroactivePatches bloquea hasta que todas las goroutines en
// vuelo de retroactive name update hayan terminado. Útil en tests
// (deterministic assertions) y en graceful shutdown (no perder
// PATCHes a medio aplicar). Si la feature está desactivada o nunca
// se disparó, devuelve inmediatamente.
func (i *Incoming) WaitRetroactivePatches() {
	i.retroactiveWG.Wait()
}

func (i *Incoming) sync(ctx context.Context, instance string, inboxID int, rs resolvedSender, content string, fromMe bool, contactName, waID string, media *mediaInfo, r wameow.WAResolver, rawMsg *waE2E.Message, postedMsgIDOut *int, postedConvIDOut *int) error {
	// Resolvemos el client downstream apropiado para esta instancia
	// (multi-tenant si está configurado, fallback global si no).
	ds := i.ds.For(ctx, instance)
	// La búsqueda usa el JID primario como identifier (LID o PN). Es lo único
	// estable a lo largo del tiempo para un usuario en su modo nativo.
	identifier := rs.primaryJID

	contact, err := findContactByIdentifier(ctx, ds, identifier, rs.phone)
	if err != nil {
		return fmt.Errorf("find contact: %w", err)
	}
	if contact == nil {
		req := downstream.CreateContactReq{
			InboxID:    inboxID,
			Name:       pickName(contactName, rs),
			Identifier: identifier,
		}
		// Solo poblamos phone_number cuando tenemos un PN real.
		if rs.phone != "" {
			req.PhoneNumber = "+" + rs.phone
		}
		contact, err = ds.CreateContact(ctx, req)
		if err != nil {
			return fmt.Errorf("create contact: %w", err)
		}
	}
	// Avatar sync — aplica tanto al contacto recién creado como al
	// existente. El tracker decide cuándo procede (primera vez o TTL).
	// Sin tracker (v0.31.0 mode): solo se llamará para creados nuevos
	// porque la rama de existing pasa sobre contact ya no-nil sin tocar
	// el flow original. Con tracker (v0.31.1+): se chequea en ambos casos.
	if chatJID, parseErr := types.ParseJID(identifier); parseErr == nil {
		i.maybeAvatarSync(ds, r, contact.ID, chatJID, instance)
	}

	conv, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil {
		return fmt.Errorf("find conversation: %w", err)
	}
	if conv == nil {
		conv, err = ds.CreateConversation(ctx, downstream.CreateConversationReq{
			SourceID:  identifier,
			InboxID:   inboxID,
			ContactID: contact.ID,
		})
		if err != nil {
			return fmt.Errorf("create conversation: %w", err)
		}
	}

	msgType := "incoming"
	if fromMe {
		msgType = "outgoing"
	}

	// Si hay media, descargar bytes y postear como multipart attachment.
	// Si la descarga falla, caemos a postear solo el caption + marker.
	if media != nil && r != nil && rawMsg != nil {
		data, err := r.DownloadAny(ctx, rawMsg)
		if err != nil {
			i.logger.Error("download media failed — posting text-only fallback",
				"err", err, "mimetype", media.mimetype, "waID", waID)
			fallback := content
			if fallback == "" {
				fallback = fmt.Sprintf("[adjunto %s no se pudo descargar: %s]", media.kind, media.mimetype)
			} else {
				fallback += fmt.Sprintf("\n\n[adjunto %s no se pudo descargar: %s]", media.kind, media.mimetype)
			}
			respFB, errFB := ds.PostMessage(ctx, downstream.PostMessageReq{
				ConversationID: conv.ID,
				Content:        fallback,
				MessageType:    msgType,
				SourceID:       "WAID:" + waID,
			})
			if respFB != nil && postedMsgIDOut != nil {
				*postedMsgIDOut = respFB.ID
			}
			if postedConvIDOut != nil {
				*postedConvIDOut = conv.ID
			}
			return errFB
		}
		respAtt, errAtt := ds.PostMessageWithAttachment(ctx, downstream.PostMessageAttachmentReq{
			ConversationID: conv.ID,
			Content:        content,
			MessageType:    msgType,
			SourceID:       "WAID:" + waID,
			FileName:       media.filename,
			MimeType:       media.mimetype,
			Data:           data,
		})
		if respAtt != nil && postedMsgIDOut != nil {
			*postedMsgIDOut = respAtt.ID
		}
		if postedConvIDOut != nil {
			*postedConvIDOut = conv.ID
		}
		return errAtt
	}

	resp, err := ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conv.ID,
		Content:        content,
		MessageType:    msgType,
		SourceID:       "WAID:" + waID,
	})
	if err == nil && !fromMe && i.waids != nil {
		// Tracker para mark-as-read outgoing (v0.39.0). Solo incoming
		// reales (NO los fromMe que el agente envió por la app móvil).
		i.waids.RecordIncoming(instance, identifier, waID, rs.primaryJID, time.Now())
	}
	// v0.40.0: registrar para retroactive name update. Lo hacemos via
	// recordPostedGroupMsg que se invoca desde Handle (donde tenemos
	// acceso al *events.Message). Aquí solo guardamos el msgID si se
	// devolvió, vía un puntero al postedMsgID que el caller pasa.
	if resp != nil && postedMsgIDOut != nil {
		*postedMsgIDOut = resp.ID
	}
	return err
}

// findContactByIdentifier intenta primero por phone (si lo tenemos) y luego por
// el identifier exacto. Necesario porque /contacts/search no busca por identifier.
func findContactByIdentifier(ctx context.Context, ds *downstream.Client, identifier, phone string) (*downstream.Contact, error) {
	if phone != "" {
		c, err := ds.FindContactByPhone(ctx, phone)
		if err != nil {
			return nil, err
		}
		if c != nil {
			return c, nil
		}
	}
	// fallback: buscar por el "user" del JID (el identifier raw no es indexable en search)
	user := identifier
	for i := range identifier {
		if identifier[i] == '@' {
			user = identifier[:i]
			break
		}
	}
	c, err := ds.FindContactByPhone(ctx, user)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// mediaInfo describe el adjunto descubierto en un *events.Message.
type mediaInfo struct {
	kind     string // "image" | "audio" | "video" | "document" | "sticker"
	mimetype string
	filename string
	caption  string
}

// extractMedia inspecciona los tipos media conocidos. Devuelve nil si el
// mensaje no contiene ninguno.
func extractMedia(msg *events.Message) *mediaInfo {
	if msg == nil || msg.Message == nil {
		return nil
	}
	m := msg.Message
	if im := m.GetImageMessage(); im != nil {
		return &mediaInfo{
			kind:     "image",
			mimetype: im.GetMimetype(),
			filename: filenameFromMime(im.GetMimetype(), "image", "jpg"),
			caption:  im.GetCaption(),
		}
	}
	if am := m.GetAudioMessage(); am != nil {
		// Browser compat (v0.38.0):
		// - Mime se sanitiza para quitar el `; codecs=opus` que algunos
		//   downstreams confunden y los reproductores html5 audio
		//   no esperan en el header Content-Type.
		// - Voice notes (PTT) usan filename "voice-note.ogg" en lugar
		//   de "audio.opus" — la extensión .ogg activa el codec en
		//   más browsers que .opus.
		mime := sanitizeMime(am.GetMimetype())
		if mime == "" {
			mime = "audio/ogg"
		}
		prefix := "audio"
		if am.GetPTT() {
			prefix = "voice-note"
		}
		return &mediaInfo{
			kind:     "audio",
			mimetype: mime,
			filename: filenameFromMime(mime, prefix, "ogg"),
		}
	}
	if vm := m.GetVideoMessage(); vm != nil {
		return &mediaInfo{
			kind:     "video",
			mimetype: vm.GetMimetype(),
			filename: filenameFromMime(vm.GetMimetype(), "video", "mp4"),
			caption:  vm.GetCaption(),
		}
	}
	if dm := m.GetDocumentMessage(); dm != nil {
		fn := dm.GetFileName()
		if fn == "" {
			fn = filenameFromMime(dm.GetMimetype(), "document", "bin")
		}
		return &mediaInfo{
			kind:     "document",
			mimetype: dm.GetMimetype(),
			filename: fn,
			caption:  dm.GetCaption(),
		}
	}
	if sm := m.GetStickerMessage(); sm != nil {
		// Browser compat (v0.38.0): default mime "image/webp" si WA
		// devuelve vacío — necesario para que browsers detecten cómo
		// renderizar (WebP es WIDELY soportado en navegadores modernos).
		mime := sanitizeMime(sm.GetMimetype())
		if mime == "" {
			mime = "image/webp"
		}
		return &mediaInfo{
			kind:     "sticker",
			mimetype: mime,
			filename: filenameFromMime(mime, "sticker", "webp"),
		}
	}
	return nil
}

// sanitizeMime quita el parámetro de codec del Content-Type. Por
// ejemplo "audio/ogg; codecs=opus" → "audio/ogg". Algunos browsers y
// downstreams se confunden con el codec specifier en el header del
// upload. Devuelve el mime original si no hay `;`.
func sanitizeMime(mime string) string {
	if i := strings.Index(mime, ";"); i >= 0 {
		return strings.TrimSpace(mime[:i])
	}
	return mime
}

// filenameFromMime sintetiza un filename razonable a partir del mimetype.
// Para "image/jpeg" → "image.jpg", para "audio/ogg; codecs=opus" → "audio.ogg".
func filenameFromMime(mime, prefix, defaultExt string) string {
	ext := defaultExt
	if i := strings.Index(mime, "/"); i >= 0 && i+1 < len(mime) {
		sub := mime[i+1:]
		if j := strings.Index(sub, ";"); j >= 0 {
			sub = sub[:j]
		}
		sub = strings.TrimSpace(sub)
		if sub != "" {
			ext = sub
		}
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	return prefix + "." + ext
}

// senderInfo captura los datos resueltos del sender de un mensaje de
// grupo — útil tanto para construir el prefix como para registrar el
// mensaje en el history tracker (v0.40.0 retroactive name update).
type senderInfo struct {
	name      string // nombre resuelto (ContactName o PushName)
	saved     bool   // IsContactSaved al momento del resolve
	phone     string // teléfono raw (sin formatear) del sender
	phoneFmt  string // teléfono formateado E.164 (con +)
}

// resolveSenderInfo aplica la misma lógica de resolución que
// applyGroupSenderPrefix pero la devuelve como struct, sin generar el
// content. Permite al caller usar los datos para múltiples propósitos
// (prefix + recording).
func resolveSenderInfo(msg *events.Message, r wameow.WAResolver) senderInfo {
	si := senderInfo{}
	sender := msg.Info.Sender
	switch sender.Server {
	case types.DefaultUserServer:
		si.phone = sender.User
	case types.HiddenUserServer:
		if r != nil {
			if pn, ok := r.PNForLID(sender.ToNonAD()); ok {
				si.phone = pn.User
			}
		}
	}
	if si.phone != "" {
		si.phoneFmt = formatE164(si.phone)
	}
	si.name, si.saved = resolveJIDNameSaved(sender, r)
	if si.name == "" {
		// PushName del *events.Message NO cuenta como saved
		// (auto-asignado por el remitente).
		si.name = msg.Info.PushName
	}
	return si
}

// resolveJIDNameSaved resuelve el nombre canónico + saved status de
// un JID arbitrario. Aplica el fix v0.39.9 para LID→PN (si PN está
// saved y LID no, prefiere el ContactName del PN sobre el push name
// del LID). NO consulta msg.Info.PushName — solo el contact store.
//
// Devuelve ("", false) si el resolver es nil o no encuentra entry.
// Usado por resolveSenderInfo (msg-based) y formatQuotedBlock
// (jid-based desde ContextInfo.Participant).
func resolveJIDNameSaved(jid types.JID, r wameow.WAResolver) (string, bool) {
	if r == nil {
		return "", false
	}
	name := r.ContactName(jid.ToNonAD())
	saved := r.IsContactSaved(jid.ToNonAD())
	if jid.Server == types.HiddenUserServer && (name == "" || !saved) {
		if pn, ok := r.PNForLID(jid.ToNonAD()); ok {
			pnSaved := r.IsContactSaved(pn)
			if !saved && pnSaved {
				return r.ContactName(pn), true
			} else if name == "" {
				name = r.ContactName(pn)
				if !saved {
					saved = pnSaved
				}
			}
		}
	}
	return name, saved
}

// applyGroupSenderPrefix añade al body un prefijo identificando al
// remitente dentro del grupo. Formato actual:
//
//	**~Richard** `+34604021705`
//	<body>
//
// Tilde + nombre en bold (foreground, estilo WhatsApp), em-space
// (U+2003, ~4x un espacio normal) que markdown NO colapsa, teléfono
// en code block (background, monospace, fondo distintivo). El em-space
// da respiración visual entre el bold y el code sin necesitar
// separador char explícito.
//
// Degradaciones:
//   - sin teléfono: "**~<name>**:\n<body>"
//   - sin nombre:   "_<phone>_:\n<body>"
//   - sin ninguno:  devuelve el body sin tocar.
//
// El teléfono se formatea E.164 separando solo el CC (formatE164).
func applyGroupSenderPrefix(body string, msg *events.Message, r wameow.WAResolver) string {
	si := resolveSenderInfo(msg, r)
	prefix, ok := renderGroupSenderPrefix(si)
	if !ok {
		return body
	}
	if body == "" {
		return prefix
	}
	// Free function (sin acceso al field configurable) — usa el default
	// paragraph. Las rutas de producción (handleMessage,
	// applyRetroactivePatches) usan i.headerSep para respetar el
	// QRSGEN_GROUP_HEADER_SEP del operador.
	return prefix + GroupHeaderSepParagraph + body
}

// renderGroupSenderPrefix construye el prefix (code block) a partir de
// un senderInfo ya resuelto. Devuelve (prefix, true) si pudo
// identificar al sender; (—, false) si no hay ni phone ni nombre.
//
// Usa el default template GroupHeaderTemplateDefault cuando hay phone
// y name. Para custom template usar renderSenderHeader directamente.
//
// Reutilizable por retroactive name update (v0.40.0): el orchestrator
// resuelve el senderInfo actual y reusa este helper para producir el
// header nuevo.
func renderGroupSenderPrefix(si senderInfo) (string, bool) {
	return renderSenderHeader(si, GroupHeaderTemplateDefault)
}

// GroupHeaderTemplateDefault es el template usado cuando
// QRSGEN_HEADER_TEMPLATE no se setea. Tokens disponibles:
//   - $phone → E.164 con `+` (ej. `+34604021705`)
//   - $name  → nombre canónico, con `~` delante si NO está saved
//
// El `~` es automático según IsContactSaved — el usuario solo elige
// el wrapper (code block, bold, etc).
const GroupHeaderTemplateDefault = "`$phone · $name`"

// renderSenderHeader sustituye $phone y $name en el template para
// generar el header del sender. Solo aplica el template cuando hay
// tanto phone como name; los casos degradados usan fallback fijo
// (compatibilidad con código anterior).
//
// Desde v0.45.0. El `~` para no-saved está integrado automáticamente
// en $name — el template solo controla el envoltorio visual.
func renderSenderHeader(si senderInfo, template string) (string, bool) {
	nameMark := si.name
	if si.name != "" && !si.saved {
		nameMark = "~" + si.name
	}
	if template == "" {
		template = GroupHeaderTemplateDefault
	}
	switch {
	case si.phoneFmt != "" && si.name != "":
		out := strings.ReplaceAll(template, "$phone", si.phoneFmt)
		out = strings.ReplaceAll(out, "$name", nameMark)
		return out, true
	case si.name != "":
		return "`" + nameMark + ":`", true
	case si.phoneFmt != "":
		return "`" + si.phoneFmt + ":`", true
	}
	return "", false
}

// formatE164 toma "34604021705" → "+34604021705". Devuelve el número
// compacto con `+` delante. (Versiones anteriores separaban CC con
// espacio; resultaba ruido visual al lado del nombre — el `+` ya
// marca el inicio).
func formatE164(digits string) string {
	if digits == "" {
		return ""
	}
	return "+" + digits
}

// isSupportedChatServer indica si procesamos eventos de este tipo de chat.
// Solo aceptamos 1-on-1 (PN/LID) y grupos. Broadcasts, newsletter, bot, etc. se ignoran.
func isSupportedChatServer(server string) bool {
	switch server {
	case types.DefaultUserServer, // s.whatsapp.net
		types.HiddenUserServer, // lid
		types.GroupServer:      // g.us
		return true
	}
	return false
}

func extractTextContent(msg *events.Message) string {
	if msg.Message == nil {
		return ""
	}
	if msg.Message.GetConversation() != "" {
		return msg.Message.GetConversation()
	}
	if ext := msg.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText()
	}
	// Location messages se renderizan como link + label legible.
	// Solo aplicamos si el msg trae LocationMessage; live locations
	// (isLive=true) salen igual — el agente ve un link estático con
	// las coords del último update recibido.
	if loc := msg.Message.GetLocationMessage(); loc != nil {
		return formatLocationContent(loc)
	}
	// Polls (encuestas): formato como lista numerada de opciones para
	// que el agente vea la pregunta y opciones. Los votos llegan como
	// PollUpdateMessage y NO se procesan (Chatwoot no tiene widget de
	// polls para reflejarlos correctamente). v0.37.0.
	if poll := msg.Message.GetPollCreationMessage(); poll != nil {
		return formatPollContent(poll)
	}
	if poll := msg.Message.GetPollCreationMessageV3(); poll != nil {
		return formatPollContent(poll)
	}
	return ""
}

// extractContextInfo devuelve el ContextInfo del primer wrapper de
// mensaje que lo tenga. ExtendedText es lo más común para replies
// de texto; los reply de media usan el ContextInfo de la propia
// media message.
func extractContextInfo(m *waE2E.Message) *waE2E.ContextInfo {
	if m == nil {
		return nil
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		if ci := ext.GetContextInfo(); ci != nil {
			return ci
		}
	}
	if img := m.GetImageMessage(); img != nil {
		if ci := img.GetContextInfo(); ci != nil {
			return ci
		}
	}
	if vid := m.GetVideoMessage(); vid != nil {
		if ci := vid.GetContextInfo(); ci != nil {
			return ci
		}
	}
	if aud := m.GetAudioMessage(); aud != nil {
		if ci := aud.GetContextInfo(); ci != nil {
			return ci
		}
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		if ci := doc.GetContextInfo(); ci != nil {
			return ci
		}
	}
	if st := m.GetStickerMessage(); st != nil {
		if ci := st.GetContextInfo(); ci != nil {
			return ci
		}
	}
	return nil
}

// extractQuotedText obtiene una representación textual breve del
// mensaje citado (texto raw, caption de media, o un placeholder con
// emoji para tipos sin texto). Limita a la primera línea de texto
// representativa — el blockquote no debe ahogar al body real.
func extractQuotedText(qm *waE2E.Message) string {
	if qm == nil {
		return ""
	}
	if c := qm.GetConversation(); c != "" {
		return c
	}
	if ext := qm.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText()
	}
	if img := qm.GetImageMessage(); img != nil {
		if c := img.GetCaption(); c != "" {
			return "🖼️ " + c
		}
		return "🖼️ [imagen]"
	}
	if vid := qm.GetVideoMessage(); vid != nil {
		if c := vid.GetCaption(); c != "" {
			return "🎥 " + c
		}
		return "🎥 [video]"
	}
	if aud := qm.GetAudioMessage(); aud != nil {
		if aud.GetPTT() {
			return "🎤 [nota de voz]"
		}
		return "🎵 [audio]"
	}
	if doc := qm.GetDocumentMessage(); doc != nil {
		if c := doc.GetCaption(); c != "" {
			return "📄 " + c
		}
		if t := doc.GetTitle(); t != "" {
			return "📄 " + t
		}
		return "📄 [documento]"
	}
	if st := qm.GetStickerMessage(); st != nil {
		_ = st
		return "🟩 [sticker]"
	}
	if loc := qm.GetLocationMessage(); loc != nil {
		_ = loc
		return "📍 [ubicación]"
	}
	return "[mensaje]"
}

// formatQuotedBlock renderiza el mensaje citado como blockquote
// markdown. Devuelve "" si el msg no es un reply o si el quoted no
// tiene representación textual.
//
// Formato:
//
//	> _↩️ respondiendo a Name:_
//	> texto citado (line 1)
//	> texto citado (line 2)
//
// La cabecera resuelve Name vía WAResolver (preferimos saved name
// canónico). Sin nombre cae a phone, sin phone queda "_↩️ respondiendo:_".
// El texto citado se trunca a 200 runas para no inflar la conv.
func formatQuotedBlock(msg *events.Message, r wameow.WAResolver) string {
	if msg.Message == nil {
		return ""
	}
	ci := extractContextInfo(msg.Message)
	if ci == nil {
		return ""
	}
	quoted := ci.GetQuotedMessage()
	if quoted == nil {
		return ""
	}
	text := extractQuotedText(quoted)
	if text == "" {
		return ""
	}

	// Truncar por runas para no romper UTF-8.
	const maxQuoteRunes = 200
	if runes := []rune(text); len(runes) > maxQuoteRunes {
		text = string(runes[:maxQuoteRunes]) + "…"
	}

	// v0.44.4: header del quoted al estilo del group prefix
	// (`↪ +phone · name` en code block), en su propia línea del
	// blockquote. La flecha unicode U+21AA (↪) sustituye al emoji
	// ↩️ — sale como glyph plano de la fuente, sin variation
	// selector que pueda romper en renderers que no soportan emoji
	// en line-height. El name respeta la convención `~` si no saved
	// (igual que group prefix v0.39.5 + fix v0.39.9 LID→PN canónico).
	//
	// En 1:1 (sin Participant) el header se omite — el contexto es
	// trivial (el author es el otro extremo del chat).
	header := buildQuoteHeader(ci, r)

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines)+1)
	if header != "" {
		out = append(out, "> "+header)
	}
	for _, l := range lines {
		out = append(out, "> "+l)
	}
	return strings.Join(out, "\n")
}

// buildQuoteHeader arma la cabecera del blockquote de quote/reply
// context al estilo del group prefix: code block con flecha + phone
// + middle dot + name (con `~` si no saved). Devuelve "" si no hay
// Participant resoluble (típico en 1:1).
func buildQuoteHeader(ci *waE2E.ContextInfo, r wameow.WAResolver) string {
	part := ci.GetParticipant()
	if part == "" {
		return ""
	}
	jid, err := types.ParseJID(part)
	if err != nil {
		return ""
	}

	// Resolver name + saved + phone con la misma cadena que el group
	// prefix. resolveJIDNameSaved hereda el fix v0.39.9.
	name, saved := resolveJIDNameSaved(jid, r)
	phone := ""
	switch jid.Server {
	case types.DefaultUserServer:
		phone = jid.User
	case types.HiddenUserServer:
		if r != nil {
			if pn, ok := r.PNForLID(jid.ToNonAD()); ok {
				phone = pn.User
			}
		}
	}
	phoneFmt := ""
	if phone != "" {
		phoneFmt = formatE164(phone)
	}
	nameMark := name
	if name != "" && !saved {
		nameMark = "~" + name
	}

	const arrow = "↪ " // U+21AA RIGHTWARDS ARROW WITH HOOK + espacio
	switch {
	case phoneFmt != "" && nameMark != "":
		return "`" + arrow + phoneFmt + " · " + nameMark + "`"
	case nameMark != "":
		return "`" + arrow + nameMark + "`"
	case phoneFmt != "":
		return "`" + arrow + phoneFmt + "`"
	}
	return ""
}

// formatPollContent serializa un PollCreationMessage a un body legible
// con la pregunta, opciones numeradas y un hint del modo (single vs
// multi-select). Los votos posteriores (PollUpdateMessage) NO se
// procesan — Chatwoot no tiene UI nativa para reflejarlos. v0.37.0.
func formatPollContent(poll *waE2E.PollCreationMessage) string {
	name := poll.GetName()
	if name == "" {
		// Sin pregunta no merece la pena propagar — el agente vería
		// solo opciones flotantes sin contexto.
		return ""
	}
	options := poll.GetOptions()
	if len(options) == 0 {
		return ""
	}

	parts := []string{"🗳️ **Encuesta:** " + name}
	for i, opt := range options {
		// Numeración 1-based, formato "N. opción".
		parts = append(parts, fmt.Sprintf("%d. %s", i+1, opt.GetOptionName()))
	}

	// Hint del modo: 1 = single, >1 = multi, 0 = unlimited.
	maxSel := poll.GetSelectableOptionsCount()
	switch {
	case maxSel == 1:
		parts = append(parts, "_(elige 1 opción)_")
	case maxSel > 1:
		parts = append(parts, fmt.Sprintf("_(elige hasta %d opciones)_", maxSel))
	}
	return strings.Join(parts, "\n")
}

// formatLocationContent serializa un LocationMessage a un body legible
// con link a Google Maps. Si el mensaje incluye Name/Address de WhatsApp
// (POI o lugar guardado), los antepone para contexto. Si IsLive, lo marca
// como live location en el header.
func formatLocationContent(loc *waE2E.LocationMessage) string {
	lat := loc.GetDegreesLatitude()
	lng := loc.GetDegreesLongitude()
	// Evita coordenadas 0,0 vacías — formato no útil.
	if lat == 0 && lng == 0 {
		return ""
	}
	header := "📍 Ubicación compartida"
	if loc.GetIsLive() {
		header = "📍 Ubicación en vivo"
	}

	parts := []string{header}
	if name := loc.GetName(); name != "" {
		parts = append(parts, "**"+name+"**")
	}
	if addr := loc.GetAddress(); addr != "" {
		parts = append(parts, addr)
	}
	// Link a Google Maps — formato universal que todos los browsers
	// abren bien. Lat/lng con 6 decimales (precisión ~10cm).
	link := fmt.Sprintf("https://maps.google.com/?q=%.6f,%.6f", lat, lng)
	parts = append(parts, link)

	if cmt := loc.GetComment(); cmt != "" {
		parts = append(parts, "_"+cmt+"_")
	}
	return strings.Join(parts, "\n")
}

func pickName(resolvedName string, rs resolvedSender) string {
	if resolvedName != "" {
		return resolvedName
	}
	// Sin info de contacto: NO inventamos identidad. Marcamos como anónimo,
	// dejando explícito si es LID o un teléfono sin asociar.
	if rs.phone != "" {
		return "WhatsApp " + rs.phone
	}
	if rs.lidJID != "" {
		return "WhatsApp LID " + lidShort(rs.lidJID)
	}
	return rs.primaryJID
}

func lidShort(lidJID string) string {
	user := lidJID
	for i := range lidJID {
		if lidJID[i] == '@' {
			user = lidJID[:i]
			break
		}
	}
	if len(user) > 6 {
		return "…" + user[len(user)-6:]
	}
	return user
}
