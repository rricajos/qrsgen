package downstream

import (
	"context"
	"time"
)

// DownstreamAPI es el contrato que qrsgen espera de cualquier downstream
// (sistema al que se sincronizan los mensajes WhatsApp). El Client de
// este package lo implementa para downstreams shape Chatwoot api_channel
// (default); adapters futuros (Zendesk, Freshdesk, n8n directo, etc.)
// pueden implementar esta misma interfaz y enchufarse en su lugar.
//
// Reservado en v0.65.0. La selección de adapter vía DOWNSTREAM_TYPE
// queda diferida a versiones futuras — hoy siempre se usa Client.
//
// Diseño: la interfaz refleja exactamente lo que el bridge llama. Los
// métodos `UpdateMessage*`, `UpdateContactName`, `UploadContactAvatar`,
// `SetTypingStatus`, `UpdateContactLastSeen` son optional features —
// adapters que no las soporten pueden devolver nil o un error
// específico y el bridge los degrada gracioso (ver flags
// QRSGEN_AVATAR_SYNC, QRSGEN_TYPING_SYNC, etc.).
type DownstreamAPI interface {
	// Contacts
	FindContactByPhone(ctx context.Context, phone string) (*Contact, error)
	CreateContact(ctx context.Context, req CreateContactReq) (*Contact, error)
	UpdateContactName(ctx context.Context, contactID int, name string) error
	UploadContactAvatar(ctx context.Context, contactID int, data []byte, mime string) error
	ListContactsByInbox(ctx context.Context, inboxID, page int) ([]Contact, bool, error)

	// Conversations
	FindOpenConversation(ctx context.Context, contactID, inboxID int) (*Conversation, error)
	CreateConversation(ctx context.Context, req CreateConversationReq) (*Conversation, error)
	SetTypingStatus(ctx context.Context, convID int, typing bool) error
	UpdateContactLastSeen(ctx context.Context, convID int, ts time.Time) error

	// Messages
	PostMessage(ctx context.Context, req PostMessageReq) (*PostMessageResp, error)
	PostMessageWithAttachment(ctx context.Context, req PostMessageAttachmentReq) (*PostMessageResp, error)
	UpdateMessageContent(ctx context.Context, convID, msgID int, content string) error
	UpdateMessageSourceID(ctx context.Context, conversationID, messageID int, sourceID string) error

	// Blobs (active_storage URLs de Chatwoot, o equivalente firmado)
	DownloadBlob(ctx context.Context, rawURL string) ([]byte, string, error)
}

// Compile-time assertion: Client cumple DownstreamAPI. Si en el futuro
// el bridge añade un método a la interfaz, el build falla aquí hasta
// que el adapter implemente el nuevo método.
var _ DownstreamAPI = (*Client)(nil)
