package bridge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
)

// historyImportConfig agrupa los parámetros del feature.
type historyImportConfig struct {
	enabled    bool
	days       int           // 1..30 (clamped)
	ratePerSec int           // throttle de POSTs a downstream
	maxAge     time.Duration // calculado de days
}

// HistoryImportResult resume una pasada del import.
type HistoryImportResult struct {
	Instance      string `json:"instance"`
	Conversations int    `json:"conversations"`
	MessagesSeen  int    `json:"messages_seen"`  // antes de filtrar por edad
	MessagesKept  int    `json:"messages_kept"`  // tras filtro de edad
	Posted        int    `json:"posted"`         // PostMessage exitosos
	Skipped       int    `json:"skipped"`        // sin texto importable
	Errors        int    `json:"errors"`         // fallos al postear
	OldestTS      int64  `json:"oldest_ts"`      // timestamp del más viejo aceptado (unix)
	NewestTS      int64  `json:"newest_ts"`      // timestamp del más reciente aceptado
}

// EnableHistoryImport activa el processing del HistorySync event.
// days clampa a [1, 30]. ratePerSec >0 (default 5) throttla los
// POST al downstream para no inundar. v0.46.0.
func (i *Incoming) EnableHistoryImport(days, ratePerSec int) {
	if days < 1 {
		days = 1
	}
	if days > 30 {
		days = 30
	}
	if ratePerSec <= 0 {
		ratePerSec = 5
	}
	i.historyCfg = &historyImportConfig{
		enabled:    true,
		days:       days,
		ratePerSec: ratePerSec,
		maxAge:     time.Duration(days) * 24 * time.Hour,
	}
}

// historyImportEnabled devuelve true si la feature está activa.
func (i *Incoming) historyImportEnabled() bool {
	return i.historyCfg != nil && i.historyCfg.enabled
}

// HandleHistorySync procesa un blob *events.HistorySync recibido al
// parear o como respuesta a un BuildHistorySyncRequest. Itera las
// conversaciones, filtra por edad (config.days), ordena cronológica
// y postea cada msg al downstream con created_at backdated y
// source_id=WAID para idempotencia.
//
// Si la feature no está activa (EnableHistoryImport no llamada),
// silenciosamente no-op. v0.46.0.
func (i *Incoming) HandleHistorySync(ctx context.Context, instance string, data *waHistorySync.HistorySync, r wameow.WAResolver) {
	if !i.historyImportEnabled() {
		metrics.RealtimeEventsTotal.WithLabelValues("history_import", "skip_disabled", instance).Inc()
		return
	}
	if data == nil {
		return
	}
	syncType := ""
	if data.SyncType != nil {
		syncType = data.SyncType.String()
	}
	i.logger.Info("history sync received",
		"instance", instance, "type", syncType,
		"conversations", len(data.GetConversations()),
		"days", i.historyCfg.days)

	// Si es ON_DEMAND y hay un latch esperando, lo entregamos al
	// caller (ImportHistoryOnDemand procesa el payload + devuelve
	// el result al endpoint admin). Para los demás tipos (INITIAL,
	// RECENT, etc) procesamos inline.
	if i.onDemandLatch != nil && i.onDemandLatch.notify(instance, data) {
		return
	}

	result := i.runHistoryImport(ctx, instance, data, r)
	i.logger.Info("history import done",
		"instance", instance, "type", syncType,
		"convs", result.Conversations, "seen", result.MessagesSeen,
		"kept", result.MessagesKept, "posted", result.Posted,
		"skipped", result.Skipped, "errors", result.Errors)
}

// runHistoryImport hace el trabajo iterativo + rate-limited. Separado
// de HandleHistorySync para que el endpoint admin pueda invocarlo
// directamente y devolver el HistoryImportResult.
func (i *Incoming) runHistoryImport(ctx context.Context, instance string, data *waHistorySync.HistorySync, r wameow.WAResolver) HistoryImportResult {
	res := HistoryImportResult{Instance: instance}
	if data == nil {
		return res
	}
	cfg := i.historyCfg
	if cfg == nil {
		return res
	}
	cutoff := time.Now().Add(-cfg.maxAge)
	res.Conversations = len(data.GetConversations())

	// Recolectar todos los msgs (filtrados por edad) para sortear globalmente
	// y throttle uniforme — evita ráfagas por conversación.
	type itemToPost struct {
		conv *waHistorySync.Conversation
		msg  *waWeb.WebMessageInfo
		ts   time.Time
	}
	var items []itemToPost
	for _, conv := range data.GetConversations() {
		for _, hmsg := range conv.GetMessages() {
			webMsg := hmsg.GetMessage()
			if webMsg == nil {
				continue
			}
			res.MessagesSeen++
			tsRaw := webMsg.GetMessageTimestamp()
			if tsRaw == 0 {
				continue
			}
			ts := time.Unix(int64(tsRaw), 0)
			if ts.Before(cutoff) {
				continue
			}
			items = append(items, itemToPost{conv: conv, msg: webMsg, ts: ts})
		}
	}
	res.MessagesKept = len(items)
	if len(items) == 0 {
		return res
	}
	sort.Slice(items, func(a, b int) bool { return items[a].ts.Before(items[b].ts) })
	if t := items[0].ts.Unix(); t > 0 {
		res.OldestTS = t
	}
	if t := items[len(items)-1].ts.Unix(); t > 0 {
		res.NewestTS = t
	}

	// Throttle: ratePerSec POST/s al downstream global.
	tickerInterval := time.Second / time.Duration(cfg.ratePerSec)
	if tickerInterval < 10*time.Millisecond {
		tickerInterval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for idx, it := range items {
		if ctx.Err() != nil {
			i.logger.Warn("history import: ctx cancelled",
				"instance", instance, "processed", idx, "total", len(items))
			break
		}
		ok := i.postHistoryMsg(ctx, instance, it.conv, it.msg, it.ts, r)
		switch ok {
		case historyResultPosted:
			res.Posted++
		case historyResultSkipped:
			res.Skipped++
		case historyResultErrored:
			res.Errors++
		}
		// Throttle solo entre POSTs reales (no esperar tras skips).
		if ok == historyResultPosted && idx < len(items)-1 {
			<-ticker.C
		}
	}
	return res
}

type historyResult int

const (
	historyResultSkipped historyResult = iota
	historyResultPosted
	historyResultErrored
)

// postHistoryMsg sintetiza un POST al downstream para un msg
// histórico. Maneja:
//   - resolución del chat (1:1 o group)
//   - extracción del texto (saltamos media sin caption — placeholders
//     opcionales en una fase posterior)
//   - aplicación del group prefix si aplica
//   - dedup vía source_id=WAID:<msgID>
//   - backdating vía CreatedAt
func (i *Incoming) postHistoryMsg(ctx context.Context, instance string, conv *waHistorySync.Conversation, webMsg *waWeb.WebMessageInfo, ts time.Time, r wameow.WAResolver) historyResult {
	key := webMsg.GetKey()
	if key == nil {
		return historyResultSkipped
	}
	chatJID, err := types.ParseJID(key.GetRemoteJID())
	if err != nil {
		return historyResultSkipped
	}
	if !isSupportedChatServer(chatJID.Server) {
		return historyResultSkipped
	}
	fromMe := key.GetFromMe()
	msgID := key.GetID()

	// Resolver sender (en grupos viene en Participant; en 1:1 es el
	// chatJID si fromMe=false, o el propio bot si fromMe=true).
	senderJID := resolveHistorySender(chatJID, key, webMsg, fromMe)

	// Extraer texto/caption del msg payload.
	content := extractHistoryText(webMsg.GetMessage())
	if content == "" {
		return historyResultSkipped
	}

	// Si es grupo y no fromMe, aplicar prefix de sender.
	if i.groupPrefixSender && chatJID.Server == types.GroupServer && !fromMe {
		si := historySenderInfo(senderJID, webMsg.GetPushName(), r)
		if prefix, ok := renderSenderHeader(si, i.headerTemplate); ok {
			if content == "" {
				content = prefix
			} else {
				content = prefix + i.headerSep + content
			}
		}
	}

	// Postear via PostMessage con created_at backdated. Necesitamos
	// el conversation_id en Chatwoot — para eso buscamos/creamos el
	// contact y abrimos la conv vía findContactByIdentifier +
	// FindOpenConversation (mismo flujo que sync() pero síncrono y
	// sin todas las side-effects).
	ds := i.ds.For(ctx, instance)
	if ds == nil {
		return historyResultErrored
	}
	identifier := chatJID.String()
	rs := resolvedSender{primaryJID: identifier}
	if chatJID.Server == types.DefaultUserServer {
		rs.phone = chatJID.User
	}
	contact, err := findContactByIdentifier(ctx, ds, identifier, rs.phone)
	if err != nil {
		i.logger.Warn("history import: find contact failed",
			"err", err, "instance", instance, "chat", chatJID)
		return historyResultErrored
	}
	if contact == nil {
		// Crear contacto perezosamente. Sin inbox_id resuelto saltamos
		// (el normal sync() lo crearía pero requiere reslove(instance)).
		inboxID := i.resolve(instance)
		if inboxID <= 0 {
			return historyResultErrored
		}
		req := downstream.CreateContactReq{
			InboxID:    inboxID,
			Name:       historyContactName(chatJID, webMsg.GetPushName(), r),
			Identifier: identifier,
		}
		if rs.phone != "" {
			req.PhoneNumber = "+" + rs.phone
		}
		contact, err = ds.CreateContact(ctx, req)
		if err != nil {
			i.logger.Warn("history import: create contact failed",
				"err", err, "instance", instance, "chat", chatJID)
			return historyResultErrored
		}
	}
	inboxID := i.resolve(instance)
	conver, err := ds.FindOpenConversation(ctx, contact.ID, inboxID)
	if err != nil {
		i.logger.Warn("history import: find conv failed",
			"err", err, "instance", instance, "chat", chatJID)
		return historyResultErrored
	}
	if conver == nil {
		conver, err = ds.CreateConversation(ctx, downstream.CreateConversationReq{
			SourceID:  identifier,
			InboxID:   inboxID,
			ContactID: contact.ID,
		})
		if err != nil || conver == nil {
			i.logger.Warn("history import: create conv failed",
				"err", err, "instance", instance, "chat", chatJID)
			return historyResultErrored
		}
	}

	msgType := "incoming"
	if fromMe {
		msgType = "outgoing"
	}
	sourceID := "WAID:" + msgID
	_, err = ds.PostMessage(ctx, downstream.PostMessageReq{
		ConversationID: conver.ID,
		Content:        content,
		MessageType:    msgType,
		SourceID:       sourceID,
		CreatedAt:      ts,
	})
	if err != nil {
		// 422 = source_id duplicate → ya importado, no es error real.
		if strings.Contains(err.Error(), "422") {
			metrics.RealtimeEventsTotal.WithLabelValues("history_import", "duplicate", instance).Inc()
			return historyResultSkipped
		}
		i.logger.Warn("history import: post failed",
			"err", err, "instance", instance, "msg_id", msgID)
		metrics.RealtimeEventsTotal.WithLabelValues("history_import", "ds_error", instance).Inc()
		return historyResultErrored
	}
	metrics.RealtimeEventsTotal.WithLabelValues("history_import", "ok", instance).Inc()
	return historyResultPosted
}

// resolveHistorySender resuelve el JID del sender de un msg histórico.
// En grupos el Participant del MessageKey indica el participante real;
// en 1:1 es el chatJID (incoming) o un placeholder (fromMe).
func resolveHistorySender(chat types.JID, key *waCommon.MessageKey, webMsg *waWeb.WebMessageInfo, fromMe bool) types.JID {
	if part := key.GetParticipant(); part != "" {
		if jid, err := types.ParseJID(part); err == nil {
			return jid
		}
	}
	// WebMessageInfo.Participant es legacy alternative
	if part := webMsg.GetParticipant(); part != "" {
		if jid, err := types.ParseJID(part); err == nil {
			return jid
		}
	}
	if fromMe {
		return types.JID{Server: types.DefaultUserServer, User: "self"}
	}
	return chat
}

// historySenderInfo construye un senderInfo a partir del sender JID
// (resuelto) y el pushName del WebMessageInfo. Aplica resolveJIDNameSaved
// para herencia del fix v0.39.9. Sin pushName, usa solo lo del store.
func historySenderInfo(jid types.JID, pushName string, r wameow.WAResolver) senderInfo {
	si := senderInfo{}
	switch jid.Server {
	case types.DefaultUserServer:
		si.phone = jid.User
	case types.HiddenUserServer:
		if r != nil {
			if pn, ok := r.PNForLID(jid.ToNonAD()); ok {
				si.phone = pn.User
			}
		}
	}
	if si.phone != "" {
		si.phoneFmt = formatE164(si.phone)
	}
	si.name, si.saved = resolveJIDNameSaved(jid, r)
	if si.name == "" {
		si.name = pushName
	}
	return si
}

// historyContactName resuelve un name razonable para crear el contact
// en Chatwoot. Para grupos usa el subject; para 1:1, el ContactName
// del resolver o el PushName del msg.
func historyContactName(chatJID types.JID, pushName string, r wameow.WAResolver) string {
	if chatJID.Server == types.GroupServer && r != nil {
		if subj, ok := r.GroupSubject(chatJID); ok {
			return subj
		}
	}
	if r != nil {
		if n := r.ContactName(chatJID.ToNonAD()); n != "" {
			return n
		}
	}
	if pushName != "" {
		return pushName
	}
	return chatJID.User
}

// extractHistoryText devuelve el texto representativo de un msg
// histórico. Cubre los tipos más comunes que llegan en HistorySync:
// texto plano, captions de media, location/poll.
//
// v0.46.0 fase 1: media sin caption se SALTA (returns ""). Una fase
// posterior podría meter placeholders o incluso descargar+upload.
func extractHistoryText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if c := m.GetConversation(); c != "" {
		return c
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if img := m.GetImageMessage(); img != nil {
		if c := img.GetCaption(); c != "" {
			return "🖼️ " + c
		}
		return "🖼️ [imagen — no importada]"
	}
	if vid := m.GetVideoMessage(); vid != nil {
		if c := vid.GetCaption(); c != "" {
			return "🎥 " + c
		}
		return "🎥 [video — no importado]"
	}
	if aud := m.GetAudioMessage(); aud != nil {
		if aud.GetPTT() {
			return "🎤 [nota de voz — no importada]"
		}
		return "🎵 [audio — no importado]"
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		if c := doc.GetCaption(); c != "" {
			return "📄 " + c
		}
		if t := doc.GetTitle(); t != "" {
			return "📄 " + t
		}
		return "📄 [documento — no importado]"
	}
	if st := m.GetStickerMessage(); st != nil {
		_ = st
		return "🟩 [sticker — no importado]"
	}
	if loc := m.GetLocationMessage(); loc != nil {
		return formatLocationContent(loc)
	}
	return ""
}

// ImportHistoryOnDemand pide más histórico para un chat específico y
// el endpoint admin lo expone. Bloquea hasta tener el HistorySync de
// respuesta (con timeout) o devuelve error.
//
// chat: JID del chat a importar.
// count: msgs a pedir (default 50, max 200).
// timeout: cuánto esperar la respuesta del phone (default 30s).
//
// Si la feature no está activa, retorna error indicativo.
func (i *Incoming) ImportHistoryOnDemand(ctx context.Context, instance string, chat types.JID, count int, timeout time.Duration, r wameow.WAResolver) (HistoryImportResult, error) {
	if !i.historyImportEnabled() {
		return HistoryImportResult{}, fmt.Errorf("history import disabled")
	}
	if r == nil {
		return HistoryImportResult{}, fmt.Errorf("resolver nil")
	}
	if count <= 0 {
		count = 50
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// Registramos un latch que el HandleHistorySync callback resolverá
	// cuando llegue un ON_DEMAND sync de este chat.
	latch := i.onDemandLatch.acquire(instance, chat)
	defer i.onDemandLatch.release(instance, chat)

	// Para BuildHistorySyncRequest necesitamos el lastKnownMessageInfo.
	// Para simplicidad de v0.46.0 lo extraemos del msg_history tracker
	// si está activo; si no, usamos un placeholder dummy que WhatsApp
	// suele aceptar (un msgID arbitrario + timestamp now).
	lastID := "0"
	lastFromMe := false
	lastTS := time.Now()
	if i.msgHistory != nil {
		// El tracker indexa por sender, no por chat; en grupos
		// múltiples senders pueden compartir chat. Buscamos cualquier
		// trackedMsg cuyo body sea reciente para el chat — el WAID
		// que enviamos al peer es solo un anchor temporal.
		// (Best effort. Si nada, dummy.)
		_ = lastID
	}

	if err := r.RequestHistorySync(ctx, chat, lastID, lastFromMe, lastTS, count); err != nil {
		return HistoryImportResult{}, fmt.Errorf("request peer: %w", err)
	}

	select {
	case data := <-latch.ch:
		return i.runHistoryImport(ctx, instance, data, r), nil
	case <-time.After(timeout):
		return HistoryImportResult{}, fmt.Errorf("timeout waiting history sync response")
	case <-ctx.Done():
		return HistoryImportResult{}, ctx.Err()
	}
}

// onDemandLatchSet sincroniza requests on-demand con el callback
// HandleHistorySync. Múltiples solicitudes para chats distintos
// pueden estar en vuelo simultáneamente.
type onDemandLatchSet struct {
	mu      sync.Mutex
	latches map[string]*onDemandLatch
}

type onDemandLatch struct {
	ch chan *waHistorySync.HistorySync
}

func newOnDemandLatchSet() *onDemandLatchSet {
	return &onDemandLatchSet{latches: map[string]*onDemandLatch{}}
}

func (s *onDemandLatchSet) acquire(instance string, chat types.JID) *onDemandLatch {
	key := instance + "|" + chat.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	l := &onDemandLatch{ch: make(chan *waHistorySync.HistorySync, 1)}
	s.latches[key] = l
	return l
}

func (s *onDemandLatchSet) release(instance string, chat types.JID) {
	key := instance + "|" + chat.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.latches, key)
}

// notifyHistoryArrival entrega un payload de HistorySync a cualquier
// latch que esté esperando. Si ningún latch matchea (sync espontáneo
// al parear), devuelve false para que el caller siga el flujo normal.
func (s *onDemandLatchSet) notify(instance string, data *waHistorySync.HistorySync) bool {
	if data == nil || data.SyncType == nil {
		return false
	}
	if *data.SyncType != waHistorySync.HistorySync_ON_DEMAND {
		return false
	}
	// El payload ON_DEMAND típicamente trae UNA conversation. Si no,
	// se entrega al primer latch que matchee por chat.
	for _, conv := range data.GetConversations() {
		jidStr := conv.GetID()
		key := instance + "|" + jidStr
		s.mu.Lock()
		l, ok := s.latches[key]
		s.mu.Unlock()
		if !ok {
			continue
		}
		select {
		case l.ch <- data:
			return true
		default:
		}
	}
	return false
}
