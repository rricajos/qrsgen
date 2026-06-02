package downstream

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeDownstream demuestra que cualquier tipo que satisfaga DownstreamAPI
// puede plugarse en su lugar — el contrato no asume *Client. Sirve a:
// (1) compile-time check de que la interfaz refleja métodos reales del
// Client, (2) ejemplo de cómo un adapter alternativo (zendesk, n8n,
// generic webhook) podría implementarse en el futuro.
type fakeDownstream struct {
	postCount int
	lastReq   PostMessageReq
}

func (f *fakeDownstream) FindContactByPhone(_ context.Context, _ string) (*Contact, error) {
	return &Contact{ID: 42, Name: "fake", PhoneNumber: "+1"}, nil
}
func (f *fakeDownstream) CreateContact(_ context.Context, _ CreateContactReq) (*Contact, error) {
	return &Contact{ID: 42}, nil
}
func (f *fakeDownstream) UpdateContactName(_ context.Context, _ int, _ string) error  { return nil }
func (f *fakeDownstream) UploadContactAvatar(_ context.Context, _ int, _ []byte, _ string) error {
	return nil
}
func (f *fakeDownstream) ListContactsByInbox(_ context.Context, _, _ int) ([]Contact, bool, error) {
	return nil, false, nil
}
func (f *fakeDownstream) FindOpenConversation(_ context.Context, _, _ int) (*Conversation, error) {
	return &Conversation{ID: 7, InboxID: 1, Status: "open"}, nil
}
func (f *fakeDownstream) CreateConversation(_ context.Context, _ CreateConversationReq) (*Conversation, error) {
	return &Conversation{ID: 7}, nil
}
func (f *fakeDownstream) SetTypingStatus(_ context.Context, _ int, _ bool) error      { return nil }
func (f *fakeDownstream) UpdateContactLastSeen(_ context.Context, _ int, _ time.Time) error {
	return nil
}
func (f *fakeDownstream) PostMessage(_ context.Context, req PostMessageReq) (*PostMessageResp, error) {
	f.postCount++
	f.lastReq = req
	return &PostMessageResp{ID: 1000 + f.postCount}, nil
}
func (f *fakeDownstream) PostMessageWithAttachment(_ context.Context, _ PostMessageAttachmentReq) (*PostMessageResp, error) {
	return &PostMessageResp{ID: 9999}, nil
}
func (f *fakeDownstream) UpdateMessageContent(_ context.Context, _, _ int, _ string) error {
	return nil
}
func (f *fakeDownstream) UpdateMessageSourceID(_ context.Context, _, _ int, _ string) error {
	return nil
}
func (f *fakeDownstream) DownloadBlob(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", errors.New("fake: download not supported")
}

// Compile-time assertion: fakeDownstream cumple DownstreamAPI. Si en el
// futuro la interfaz crece, este test falla aquí y obliga a actualizar
// el fake — recordatorio de mantenerlo realista para tests del bridge.
var _ DownstreamAPI = (*fakeDownstream)(nil)

func TestDownstreamAPI_FakeAdapterWorks(t *testing.T) {
	// Demuestra que un adapter no-Chatwoot satisface la interfaz y se
	// puede usar sin httptest.Server. Esto es el principal beneficio
	// del refactor: tests del bridge más rápidos + clarity de contrato.
	var ds DownstreamAPI = &fakeDownstream{}
	resp, err := ds.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 7,
		Content:        "hello",
		MessageType:    "outgoing",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.ID == 0 {
		t.Errorf("resp = %+v, want ID set", resp)
	}
}

func TestDownstreamAPI_RouterReturnsInterface(t *testing.T) {
	// El Router del package downstream devuelve DownstreamAPI, no *Client
	// concreto. Cualquier impl (incluyendo *Client default) se devuelve
	// como interfaz, permitiendo polimorfismo en el bridge.
	var r Router = New("http://example.test", "tok", 1)
	got := r.For(context.Background(), "anything")
	if got == nil {
		t.Fatal("For returned nil")
	}
	// Si esto compila, el Router devuelve DownstreamAPI (Go structural
	// typing lo verifica en tiempo de compilación). Runtime assertion
	// que cualquier método de la interfaz se puede llamar.
	if _, err := got.FindContactByPhone(context.Background(), "+1"); err == nil {
		// El Client real intentará HTTP a example.test y fallará — el
		// test solo confirma que el método es invocable.
		t.Log("(expected error from real http call)")
	}
}
