// Package downstream expone un cliente HTTP minimal para el downstream (sistema al que se sincronizan los mensajes).
package downstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// Client encapsula las llamadas HTTP al downstream. Mantiene la base
// URL, el token de cuenta y un http.Client con timeout de 15s. Los
// campos `authHeader`, `authScheme` y `apiPrefix` son configurables vía
// Options (default: api_access_token + raw + /api/v1/accounts/{id},
// que reproduce el shape de Chatwoot api_channel — comportamiento
// pre-v0.65.0). Stateless aparte de la conexión TCP que reusa el
// http.Client.
type Client struct {
	baseURL    string
	token      string
	accountID  int
	authHeader string
	authScheme string
	apiPrefix  string
	http       *http.Client

	// payloadTpl es el template (parsed) que sobreescribe el body
	// JSON de POST messages. Si nil → comportamiento default
	// Chatwoot-shape. Aplicar template falla → fallback default +
	// warning log. v0.65.0.
	payloadTpl *template.Template

	// tplFallbackHook se invoca cuando payloadTpl está set pero falla
	// (execute error o output JSON inválido) y caemos al default.
	// Permite cablear métricas/alerting sin acoplar a metrics package.
	// nil → no se invoca. v0.65.2.
	tplFallbackHook func(reason string)
}

// Defaults para los campos configurables. Reproducen exactamente el
// comportamiento pre-v0.65.0 (shape Chatwoot api_channel).
const (
	DefaultAuthHeader = "api_access_token"
	DefaultAuthScheme = "raw"
	DefaultAPIPrefix  = "/api/v1/accounts/{account_id}"
)

// Option configura un Client construido vía New. Permite mantener
// New backward-compatible (3 args) mientras se exponen campos
// avanzados como auth scheme o path prefix vía variadic opts.
type Option func(*Client)

// WithAuthHeader sobreescribe el nombre del header donde se envía el
// token. Default "api_access_token" (Chatwoot). Combinable con
// WithAuthScheme para downstreams tipo Zendesk (Authorization Bearer),
// Slack (Authorization Bearer), Twilio (Authorization Basic), etc.
func WithAuthHeader(name string) Option {
	return func(c *Client) { c.authHeader = name }
}

// WithAuthScheme controla el prefijo del valor del header. "raw" (default)
// envía el token literal. "Bearer" antepone "Bearer " (RFC 6750). "Basic"
// antepone "Basic " (el token debe estar ya en formato base64(user:pass)).
// Cualquier otro string se usa como prefijo literal + espacio + token.
func WithAuthScheme(scheme string) Option {
	return func(c *Client) { c.authScheme = scheme }
}

// WithAPIPathPrefix cambia el prefijo aplicado a todas las rutas. El
// token literal {account_id} se substituye por el accountID configurado
// al construir el Client. Default "/api/v1/accounts/{account_id}".
// Pasar "" para no añadir prefijo (paths absolutos desde baseURL).
func WithAPIPathPrefix(prefix string) Option {
	return func(c *Client) { c.apiPrefix = prefix }
}

// WithHTTPClient sustituye el http.Client por defecto. Útil para tests
// que ajustan timeouts/transports.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithHTTPTimeout sobreescribe el timeout del http.Client default
// (15s pre-v1.1.0). No-op si previamente se pasó WithHTTPClient con
// un client custom — en ese caso el operador controla su propio
// timeout. Para downstreams lentos (cold start, latencia regional)
// subir; para failing-fast en producción crítica bajar.
// Desde v1.1.0.
func WithHTTPTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.http != nil {
			c.http.Timeout = d
		}
	}
}

// WithTemplateFallbackHook registra un callback que se invoca cuando un
// `payload_template` configurado cae al shape default Chatwoot (por
// error de execute o JSON inválido del output). Útil para que el operador
// cablee métricas/alerting sin acoplar el package downstream al package
// metrics. v0.65.2.
//
// reason values:
//   - "execute_failed": template.Execute devolvió error (variable
//     inexistente, función inválida, etc.).
//   - "invalid_json":   el output del template no era JSON parseable.
//
// El hook se llama sincrónicamente desde PostMessage — debe ser barato
// (un counter increment). Si nil, no se invoca nada (default).
func WithTemplateFallbackHook(fn func(reason string)) Option {
	return func(c *Client) { c.tplFallbackHook = fn }
}

// ValidatePayloadTemplate parsea un Go text/template para confirmar
// que es sintácticamente correcto. Devuelve nil si vacío (no aplicar
// template) o si parsea OK. Devuelve error si la sintaxis es inválida —
// útil para validar at-set-time desde la API antes de persistir un
// template roto que se descubriría sólo al primer msg.
//
// Nota: no valida que el OUTPUT del template sea JSON válido (eso solo
// se sabe en runtime con un PostMessageReq concreto). Solo confirma
// que la sintaxis Go template parsea.
func ValidatePayloadTemplate(tpl string) error {
	if tpl == "" {
		return nil
	}
	_, err := template.New("payload_validate").Parse(tpl)
	return err
}

// WithPayloadTemplate parsea el template Go text/template suministrado
// y lo asocia al Client. Cuando PostMessage se llama, el template se
// ejecuta con el PostMessageReq como contexto y se POSTea el resultado
// como JSON (en lugar del shape Chatwoot por defecto).
//
// Si `tpl` es vacío, no se asocia template (comportamiento default
// Chatwoot). Si el template no parsea, se loguea warning y se ignora
// (el Client opera en modo default).
//
// Variables disponibles en el template:
//   - {{.Content}}        string  — contenido del mensaje
//   - {{.MessageType}}    string  — "incoming" | "outgoing" | "activity"
//   - {{.SourceID}}       string  — clave de idempotencia
//   - {{.ConversationID}} int     — id de la conv en el downstream
//   - {{.CreatedAtUnix}}  int64   — timestamp Unix (0 si no set)
//   - {{.InReplyTo}}      int     — id msg al que responde (0 si no set)
//
// El template debe producir JSON válido — qrsgen no valida la shape,
// solo la sintaxis JSON antes del POST. Desde v0.65.0.
func WithPayloadTemplate(tpl string) Option {
	return func(c *Client) {
		if tpl == "" {
			return
		}
		parsed, err := template.New("payload").Parse(tpl)
		if err != nil {
			slog.Warn("downstream: payload_template parse failed — using default", "err", err)
			return
		}
		c.payloadTpl = parsed
	}
}

// New construye un Client con timeout HTTP por defecto. `baseURL` debe
// incluir scheme + host (ej "https://omnia.example.com") y `token` es
// el token de auth contra el downstream. Opts adicionales (auth header,
// scheme, path prefix) sobreescriben los defaults Chatwoot.
func New(baseURL, token string, accountID int, opts ...Option) *Client {
	c := &Client{
		baseURL:    baseURL,
		token:      token,
		accountID:  accountID,
		authHeader: DefaultAuthHeader,
		authScheme: DefaultAuthScheme,
		apiPrefix:  DefaultAPIPrefix,
		http:       &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// setAuth aplica el header de autenticación al request según authHeader
// + authScheme. Centraliza la lógica para que TODAS las rutas (incluso
// las que construyen el http.Request a mano para multipart) la apliquen
// uniformemente.
func (c *Client) setAuth(req *http.Request) {
	var value string
	switch c.authScheme {
	case "Bearer":
		value = "Bearer " + c.token
	case "Basic":
		value = "Basic " + c.token
	case "raw", "":
		value = c.token
	default:
		value = c.authScheme + " " + c.token
	}
	req.Header.Set(c.authHeader, value)
}

// buildURL compone la URL absoluta para un path relativo aplicando el
// apiPrefix configurado. El placeholder {account_id} se reemplaza por
// el accountID numérico. Garantiza un único `/` entre prefix y path.
func (c *Client) buildURL(path string) string {
	prefix := strings.ReplaceAll(c.apiPrefix, "{account_id}", strconv.Itoa(c.accountID))
	return c.baseURL + prefix + path
}

// Contact es la proyección mínima de un contacto Chatwoot que qrsgen
// necesita. Otros campos del payload se ignoran al deserializar.
type Contact struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier"`
}

type contactSearchResponse struct {
	Payload []Contact `json:"payload"`
}

// RateLimitError indica que el downstream devolvió 429. Lleva el
// Retry-After parseado si el server lo envió. Los callers que quieran
// implementar exponential backoff con respeto al server pueden
// `errors.As` este error. v0.59.0.
type RateLimitError struct {
	RetryAfter time.Duration // 0 si el header no estaba o era inválido
	Body       string
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("downstream rate-limited (HTTP 429), retry after %s: %s", e.RetryAfter, e.Body)
	}
	return fmt.Sprintf("downstream rate-limited (HTTP 429): %s", e.Body)
}

// parseRetryAfter interpreta el header Retry-After según RFC 7231.
// Acepta tanto el formato `<delta-seconds>` (entero) como un
// `<HTTP-date>`. Devuelve 0 si no se puede parsear.
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	// Intento 1: entero de segundos.
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	// Intento 2: fecha HTTP.
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	url := c.buildURL(path)
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	// v0.59.0: 429 lleva error tipado RateLimitError con el Retry-After
	// parseado. Los callers pueden hacer errors.As para implementar
	// backoff que respete el server.
	if res.StatusCode == http.StatusTooManyRequests {
		return data, &RateLimitError{
			RetryAfter: parseRetryAfter(res.Header.Get("Retry-After")),
			Body:       string(data),
		}
	}
	if res.StatusCode >= 400 {
		return data, fmt.Errorf("downstream %s %s: HTTP %d body=%s", method, path, res.StatusCode, string(data))
	}
	return data, nil
}

// FindContactByPhone busca el primer contacto cuyo phone_number matchea
// el string indicado (E.164 con `+`). Devuelve nil sin error si no
// existe — el caller decide si crearlo. Usa el endpoint /contacts/search
// que tolera matches parciales: si necesitas exactitud filtra después.
func (c *Client) FindContactByPhone(ctx context.Context, phone string) (*Contact, error) {
	data, err := c.request(ctx, http.MethodGet, "/contacts/search?q="+phone, nil)
	if err != nil {
		return nil, err
	}
	var r contactSearchResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if len(r.Payload) == 0 {
		return nil, nil
	}
	return &r.Payload[0], nil
}

// CreateContactReq es el body para POST /contacts. `Identifier` es
// el JID en formato `<num>@s.whatsapp.net` o `<lid>@lid` — sirve
// como clave estable para de-duplicación al re-postear msgs del
// mismo sender.
type CreateContactReq struct {
	InboxID     int    `json:"inbox_id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier,omitempty"`
}

// CreateContact crea un contacto en Chatwoot y devuelve la versión
// persistida (con ID asignado). El downstream wrap-ea el payload
// dentro de {"payload":{"contact":{...}}} — esta función lo aplana.
func (c *Client) CreateContact(ctx context.Context, req CreateContactReq) (*Contact, error) {
	data, err := c.request(ctx, http.MethodPost, "/contacts", req)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Payload struct {
			Contact Contact `json:"contact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &wrap.Payload.Contact, nil
}

// PostMessageReq es el body para POST a /conversations/:id/messages.
// `ConversationID` viaja en la URL (de ahí el `json:"-"`); el resto
// va serializado. Cuando `CreatedAt` o `InReplyTo` están set, el
// marshal cambia a un map ad-hoc para añadir los campos extra.
type PostMessageReq struct {
	ConversationID int    `json:"-"`
	Content        string `json:"content"`
	MessageType    string `json:"message_type"` // "incoming" | "outgoing"
	SourceID       string `json:"source_id,omitempty"`
	// CreatedAt permite backdated messages (v0.46.0 history import).
	// Cuando se setea, qrsgen lo POSTea como `external_created_at` y
	// `created_at` para máxima compatibilidad entre versiones de
	// Chatwoot. Cero (zero value) lo omite y el downstream usa now().
	CreatedAt time.Time `json:"-"`
	// InReplyTo permite que el msg aparezca como quote-reply visual
	// del msg con ese ID en Chatwoot. Usado por reactions (v0.53.2)
	// para que el agente vea a qué msg se reaccionó. 0 = omitir.
	// Se POSTea como `content_attributes.in_reply_to`.
	InReplyTo int `json:"-"`
}

// PostMessageResp captura el `id` del msg recién creado en Chatwoot.
// Otros campos del payload (inbox_id, conversation_id, etc.) se
// ignoran porque el caller ya los conoce.
type PostMessageResp struct {
	ID int `json:"id"`
}

// PostMessage envía un mensaje a una conversación existente. Si
// CreatedAt o InReplyTo están set, se incluyen en el body — Chatwoot
// los guarda en `content_attributes.external_created_at` y
// `content_attributes.in_reply_to` respectivamente. Nota: el
// `created_at` que viaja en el body suele ser ignorado por Chatwoot
// salvo que el token sea super-admin; el backdate worker (v0.54.0)
// arregla esto post-hoc.
func (c *Client) PostMessage(ctx context.Context, req PostMessageReq) (*PostMessageResp, error) {
	path := fmt.Sprintf("/conversations/%d/messages", req.ConversationID)

	// v0.65.0: si hay payload template configurado, lo aplicamos antes
	// del shape default. El template recibe un struct con los campos
	// del req + CreatedAtUnix (Unix int64 para facilitar el uso desde
	// template). Si execute o JSON parse del resultado fallan, fallback
	// al payload default + warning log.
	if c.payloadTpl != nil {
		ctxVars := struct {
			Content        string
			MessageType    string
			SourceID       string
			ConversationID int
			CreatedAtUnix  int64
			InReplyTo      int
		}{
			Content:        req.Content,
			MessageType:    req.MessageType,
			SourceID:       req.SourceID,
			ConversationID: req.ConversationID,
			InReplyTo:      req.InReplyTo,
		}
		if !req.CreatedAt.IsZero() {
			ctxVars.CreatedAtUnix = req.CreatedAt.Unix()
		}
		var buf bytes.Buffer
		if err := c.payloadTpl.Execute(&buf, ctxVars); err == nil {
			// Validar que el output es JSON sintácticamente válido antes
			// de pasarlo a request(). Un JSON malformado da error 400 del
			// downstream con mensaje confuso; mejor detectarlo aquí.
			var probe any
			if jerr := json.Unmarshal(buf.Bytes(), &probe); jerr == nil {
				// Ruta template: bypassamos el Chatwoot-shape del default.
				data, err := c.request(ctx, http.MethodPost, path, json.RawMessage(buf.Bytes()))
				if err != nil {
					return nil, err
				}
				var resp PostMessageResp
				if err := json.Unmarshal(data, &resp); err != nil {
					return nil, fmt.Errorf("unmarshal post msg (templated): %w", err)
				}
				return &resp, nil
			}
			slog.Warn("downstream: payload_template produced invalid JSON — using default shape")
			if c.tplFallbackHook != nil {
				c.tplFallbackHook("invalid_json")
			}
		} else {
			slog.Warn("downstream: payload_template execute failed — using default shape", "err", err)
			if c.tplFallbackHook != nil {
				c.tplFallbackHook("execute_failed")
			}
		}
	}

	// v0.46.0: si CreatedAt está set, postamos un map ad-hoc que añade
	// `created_at` y `external_created_at` (compat entre versiones
	// Chatwoot). Para el flujo normal (CreatedAt zero) marshal directo
	// del struct via la ruta de siempre.
	var body any = req
	// v0.46.0: created_at backdated, v0.53.2: in_reply_to.
	// Si cualquiera de los 2 está set, construir el body como map
	// ad-hoc (struct tag JSON tendría omitempty issues con time
	// zero + int 0).
	if !req.CreatedAt.IsZero() || req.InReplyTo > 0 {
		m := map[string]any{
			"content":      req.Content,
			"message_type": req.MessageType,
		}
		if req.SourceID != "" {
			m["source_id"] = req.SourceID
		}
		if !req.CreatedAt.IsZero() {
			ts := req.CreatedAt.Unix()
			m["created_at"] = ts
			m["external_created_at"] = ts
		}
		if req.InReplyTo > 0 {
			m["content_attributes"] = map[string]any{
				"in_reply_to": req.InReplyTo,
			}
		}
		body = m
	}
	data, err := c.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var resp PostMessageResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal post msg: %w", err)
	}
	return &resp, nil
}

// PostMessageAttachmentReq describe un mensaje multimedia (imagen, audio, video, doc).
type PostMessageAttachmentReq struct {
	ConversationID int
	Content        string // caption (puede ser "")
	MessageType    string // "incoming" | "outgoing"
	SourceID       string
	FileName       string // p.ej. "image.jpg" — usado por downstream para mostrar nombre
	MimeType       string // p.ej. "image/jpeg"
	Data           []byte // bytes binarios del media en claro
}

// PostMessageWithAttachment sube un media como adjunto a una conversación.
// Usa multipart/form-data porque downstream lo requiere para attachments.
func (c *Client) PostMessageWithAttachment(ctx context.Context, req PostMessageAttachmentReq) (*PostMessageResp, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	if req.Content != "" {
		_ = mw.WriteField("content", req.Content)
	}
	_ = mw.WriteField("message_type", req.MessageType)
	if req.SourceID != "" {
		_ = mw.WriteField("source_id", req.SourceID)
	}

	// Header de la parte attachments[] con Content-Type del media (downstream
	// usa el mime para previsualizar correctamente imagen/audio/doc).
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachments[]"; filename=%q`, req.FileName))
	if req.MimeType != "" {
		header.Set("Content-Type", req.MimeType)
	}
	part, err := mw.CreatePart(header)
	if err != nil {
		return nil, fmt.Errorf("multipart create part: %w", err)
	}
	if _, err := part.Write(req.Data); err != nil {
		return nil, fmt.Errorf("multipart write data: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("multipart close: %w", err)
	}

	url := c.buildURL(fmt.Sprintf("/conversations/%d/messages", req.ConversationID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	c.setAuth(httpReq)
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("downstream multipart POST: HTTP %d body=%s", res.StatusCode, string(data))
	}

	var resp PostMessageResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal post msg w/ attachment: %w", err)
	}
	return &resp, nil
}

// Conversation es la proyección mínima de una conv Chatwoot que
// qrsgen necesita (ID, inbox al que pertenece, status). Otros campos
// del payload se ignoran al deserializar.
type Conversation struct {
	ID      int    `json:"id"`
	InboxID int    `json:"inbox_id"`
	Status  string `json:"status"` // "open" | "resolved" | "pending" | "snoozed"
}

type conversationListResp struct {
	Payload []Conversation `json:"payload"`
}

// FindOpenConversation devuelve la conversación más reciente "open" para el contacto en el inbox indicado,
// o nil si no existe ninguna.
func (c *Client) FindOpenConversation(ctx context.Context, contactID, inboxID int) (*Conversation, error) {
	path := fmt.Sprintf("/contacts/%d/conversations", contactID)
	data, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	// El endpoint devuelve {payload: [...]} o directamente un array según versión.
	var listed conversationListResp
	if err := json.Unmarshal(data, &listed); err != nil {
		var arr []Conversation
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			return nil, fmt.Errorf("unmarshal conversations: %w", err)
		}
		listed.Payload = arr
	}
	for _, conv := range listed.Payload {
		if conv.InboxID == inboxID && conv.Status == "open" {
			return &conv, nil
		}
	}
	return nil, nil
}

// CreateConversationReq es el body para POST /conversations.
// `SourceID` debe ser estable y único per inbox (sirve como clave
// idempotente — Chatwoot rechaza con 422 si ya existe).
type CreateConversationReq struct {
	SourceID  string `json:"source_id"`
	InboxID   int    `json:"inbox_id"`
	ContactID int    `json:"contact_id"`
	Status    string `json:"status,omitempty"` // default "open"
}

// CreateConversation crea una conv y devuelve la versión persistida.
// Idempotente vía SourceID — si ya existe, Chatwoot devuelve 422
// y este método propaga el error (el caller decide si recuperar la
// existente con FindOpenConversation).
func (c *Client) CreateConversation(ctx context.Context, req CreateConversationReq) (*Conversation, error) {
	data, err := c.request(ctx, http.MethodPost, "/conversations", req)
	if err != nil {
		return nil, err
	}
	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("unmarshal conv: %w", err)
	}
	return &conv, nil
}

// UpdateMessageContent reescribe el campo `content` de un mensaje ya
// posteado en el downstream. Usado para retroactive name update
// (v0.40.0): cuando el dueño del bot añade un contacto a su agenda
// tras haber recibido mensajes de él, qrsgen reescribe los mensajes
// históricos para que el nuevo nombre/sin tilde aparezca también.
//
// PATCH /api/v1/accounts/{a}/conversations/{c}/messages/{m}
// Body: {"content": "<nuevo content>"}
//
// Importante: la API de Chatwoot estándar solo permite editar `content`
// en ciertos tipos de mensaje (depende de la versión + permisos del
// token). Si el downstream rechaza el PATCH, el caller debe loguear
// warning y seguir — el feature degrada gracioso.
func (c *Client) UpdateMessageContent(ctx context.Context, convID, msgID int, content string) error {
	path := fmt.Sprintf("/conversations/%d/messages/%d", convID, msgID)
	_, err := c.request(ctx, http.MethodPatch, path, map[string]string{
		"content": content,
	})
	return err
}

// UpdateContactName actualiza el campo `name` del contacto en
// downstream. Usado por el retroactive name update extendido (v0.43.0):
// cuando el dueño del bot añade un contacto a su agenda WhatsApp,
// no solo reescribimos los mensajes históricos del grupo sino que
// también renombramos el contacto en Chatwoot para reflejar el
// nombre canónico.
//
// PUT /api/v1/accounts/{a}/contacts/{id}
// Body: {"name": "nuevo nombre"}
//
// La API de Chatwoot devuelve 200 con el contacto actualizado, o
// 4xx si el token no tiene permisos. El caller debe loguear warning
// y seguir — la feature degrada gracioso.
func (c *Client) UpdateContactName(ctx context.Context, contactID int, name string) error {
	path := fmt.Sprintf("/contacts/%d", contactID)
	_, err := c.request(ctx, http.MethodPut, path, map[string]string{
		"name": name,
	})
	return err
}

// UpdateContactLastSeen actualiza el timestamp "contact_last_seen_at"
// de la conversación en el downstream. En Chatwoot esto hace que los
// mensajes del agente queden marcados como leídos (icono check azul).
//
// POST /api/v1/accounts/{a}/conversations/{c}/update_last_seen
// Body opcional con timestamp; si vacío, el downstream usa el reloj
// del server. Pasar `ts > 0` para que coincida con el read receipt real.
func (c *Client) UpdateContactLastSeen(ctx context.Context, convID int, ts time.Time) error {
	path := fmt.Sprintf("/conversations/%d/update_last_seen", convID)
	body := map[string]any{}
	if !ts.IsZero() {
		body["agent_last_seen_at"] = ts.Unix()
		body["contact_last_seen_at"] = ts.Unix()
	}
	_, err := c.request(ctx, http.MethodPost, path, body)
	return err
}

// SetTypingStatus propaga al downstream un evento de typing (on/off)
// para una conversación. Implementa el toggle_typing_status del
// api_channel de Chatwoot.
//
// POST /api/v1/accounts/{a}/conversations/{c}/toggle_typing_status
// con body {"typing_status": "on"|"off"}.
//
// El downstream renderiza el indicador "está escribiendo" en la UI del
// agente. No es un mensaje persistente — si la conexión se cae o el
// receptor no está mirando la conv, no pasa nada.
func (c *Client) SetTypingStatus(ctx context.Context, convID int, typing bool) error {
	status := "off"
	if typing {
		status = "on"
	}
	path := fmt.Sprintf("/conversations/%d/toggle_typing_status", convID)
	_, err := c.request(ctx, http.MethodPost, path, map[string]string{
		"typing_status": status,
	})
	return err
}

// ListContactsByInbox devuelve una página de contactos asociados a un inbox.
// page es 1-based. hasMore indica si hay siguiente página (basándose en si
// la página actual está llena al límite del downstream — 15 por defecto en
// Chatwoot).
//
// Útil para bulk-resync: iterar páginas y disparar avatar sync para cada
// contacto. NO mantengas página abierta en lecturas largas — cada llamada
// es una request HTTP nueva.
//
// Nota: usa el endpoint canónico de Chatwoot
// `/contacts?inbox_id={id}&page={n}`. El path antiguo
// `/inboxes/{id}/contacts` (v0.31.3) devolvía 404 — esa ruta NO existe
// en la API de Chatwoot.
func (c *Client) ListContactsByInbox(ctx context.Context, inboxID, page int) ([]Contact, bool, error) {
	if page < 1 {
		page = 1
	}
	path := fmt.Sprintf("/contacts?inbox_id=%d&page=%d", inboxID, page)
	data, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, false, err
	}
	// Chatwoot devuelve {payload: [...], meta: {...}}. La paginación
	// se infiere comparando el tamaño de payload con el page_size típico
	// (15). Si es menor, no hay más páginas.
	var wrap struct {
		Payload []Contact `json:"payload"`
		Meta    struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		// Fallback: algunas versiones devuelven array directo.
		var arr []Contact
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			return nil, false, fmt.Errorf("unmarshal contacts: %w", err)
		}
		wrap.Payload = arr
	}
	hasMore := len(wrap.Payload) >= 15 // page_size típico de Chatwoot
	return wrap.Payload, hasMore, nil
}

// UploadContactAvatar sube una imagen como avatar del contacto en el downstream.
// Usa multipart/form-data con el campo `avatar` (formato esperado por la API
// PUT /api/v1/accounts/:account/contacts/:id del downstream). El mime
// determina la extensión del filename — image/png → avatar.png, otros →
// avatar.jpg. Si la respuesta es >= 400 devuelve error con el body.
func (c *Client) UploadContactAvatar(ctx context.Context, contactID int, data []byte, mime string) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	filename := "avatar.jpg"
	if mime == "image/png" {
		filename = "avatar.png"
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="avatar"; filename=%q`, filename))
	if mime != "" {
		header.Set("Content-Type", mime)
	}
	part, err := mw.CreatePart(header)
	if err != nil {
		return fmt.Errorf("multipart create part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("multipart write data: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}

	url := c.buildURL(fmt.Sprintf("/contacts/%d", contactID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, &body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	c.setAuth(httpReq)
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	res, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode >= 400 {
		respBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("PUT avatar: HTTP %d body=%s", res.StatusCode, string(respBody))
	}
	return nil
}

// UpdateMessageSourceID hace PATCH del source_id de un mensaje. Útil para enlazar
// el mensaje saliente con el WAID generado por whatsmeow.
func (c *Client) UpdateMessageSourceID(ctx context.Context, conversationID, messageID int, sourceID string) error {
	path := fmt.Sprintf("/conversations/%d/messages/%d", conversationID, messageID)
	_, err := c.request(ctx, http.MethodPatch, path, map[string]string{"source_id": sourceID})
	return err
}

// DownloadBlob descarga los bytes de una URL de downstream active_storage.
// Las URLs son firmadas (con TTL) por lo que no requieren token, pero lo
// incluimos por defensa por si downstream lo exigiera en alguna versión.
//
// v0.64.6: validación SSRF — la URL DEBE pertenecer al host del baseURL
// configurado para esta instancia del Client. CodeQL flaggeaba esto como
// "Uncontrolled data used in network request" (Critical) porque el URL
// venía del payload de webhook (potencialmente tainted). Ahora si un
// downstream malicioso (o MitM) inserta una URL apuntando a otro host
// arbitrario, esta función rechaza con error en lugar de fetchearla.
func (c *Client) DownloadBlob(ctx context.Context, rawURL string) ([]byte, string, error) {
	if err := c.validateBlobURL(rawURL); err != nil {
		return nil, "", fmt.Errorf("download blob: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("new request: %w", err)
	}
	c.setAuth(req)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("do: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download blob: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	contentType := res.Header.Get("Content-Type")
	return data, contentType, nil
}

// validateBlobURL rechaza URLs que no apunten al host configurado del
// downstream. Permite paths arbitrarios (Chatwoot active_storage usa
// rutas firmadas largas) pero NO permite cambio de scheme/host.
// Returns nil if the URL is safe to fetch.
func (c *Client) validateBlobURL(rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("scheme must be http(s); got %q", target.Scheme)
	}
	if target.Host == "" {
		return fmt.Errorf("empty host")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		// baseURL inválido es un bug de config (cero-day) — fallar fast.
		return fmt.Errorf("internal: invalid baseURL %q: %w", c.baseURL, err)
	}
	if !strings.EqualFold(target.Host, base.Host) {
		return fmt.Errorf("host mismatch: blob host %q != downstream host %q (SSRF protection)", target.Host, base.Host)
	}
	return nil
}
