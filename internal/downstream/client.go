// Package downstream expone un cliente HTTP minimal para el downstream (sistema al que se sincronizan los mensajes).
package downstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

type Client struct {
	baseURL   string
	token     string
	accountID int
	http      *http.Client
}

func New(baseURL, token string, accountID int) *Client {
	return &Client{
		baseURL:   baseURL,
		token:     token,
		accountID: accountID,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

type Contact struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier"`
}

type contactSearchResponse struct {
	Payload []Contact `json:"payload"`
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
	url := fmt.Sprintf("%s/api/v1/accounts/%d%s", c.baseURL, c.accountID, path)
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("api_access_token", c.token)
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
	if res.StatusCode >= 400 {
		return data, fmt.Errorf("downstream %s %s: HTTP %d body=%s", method, path, res.StatusCode, string(data))
	}
	return data, nil
}

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

type CreateContactReq struct {
	InboxID     int    `json:"inbox_id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier,omitempty"`
}

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

type PostMessageReq struct {
	ConversationID int    `json:"-"`
	Content        string `json:"content"`
	MessageType    string `json:"message_type"` // "incoming" | "outgoing"
	SourceID       string `json:"source_id,omitempty"`
}

type PostMessageResp struct {
	ID int `json:"id"`
}

func (c *Client) PostMessage(ctx context.Context, req PostMessageReq) (*PostMessageResp, error) {
	path := fmt.Sprintf("/conversations/%d/messages", req.ConversationID)
	data, err := c.request(ctx, http.MethodPost, path, req)
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

	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations/%d/messages", c.baseURL, c.accountID, req.ConversationID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("api_access_token", c.token)
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

type CreateConversationReq struct {
	SourceID  string `json:"source_id"`
	InboxID   int    `json:"inbox_id"`
	ContactID int    `json:"contact_id"`
	Status    string `json:"status,omitempty"` // default "open"
}

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

	url := fmt.Sprintf("%s/api/v1/accounts/%d/contacts/%d", c.baseURL, c.accountID, contactID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, &body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("api_access_token", c.token)
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
func (c *Client) DownloadBlob(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("api_access_token", c.token)
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
