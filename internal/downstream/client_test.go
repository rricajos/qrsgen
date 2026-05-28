package downstream

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient wires a Client to an httptest.Server. Returns the client and
// a per-request map populated by the handler so tests can assert what the
// client sent.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok-test", 7), srv
}

func TestRequest_AuthHeader(t *testing.T) {
	var gotToken, gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("api_access_token")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"payload":[]}`))
	})
	_, _ = c.FindContactByPhone(context.Background(), "34611")
	if gotToken != "tok-test" {
		t.Errorf("token = %q, want tok-test", gotToken)
	}
	if gotPath != "/api/v1/accounts/7/contacts/search" {
		t.Errorf("path = %q, want /api/v1/accounts/7/contacts/search", gotPath)
	}
}

func TestRequest_ErrorOn4xx(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	})
	_, err := c.FindContactByPhone(context.Background(), "34611")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want one containing 401", err)
	}
}

func TestFindContactByPhone_Found(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"payload":[{"id":42,"name":"Alice","phone_number":"+34611111111","identifier":"id-1"}]}`))
	})
	got, err := c.FindContactByPhone(context.Background(), "+34611111111")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 42 || got.Name != "Alice" {
		t.Errorf("got = %+v, want {ID:42 Name:Alice ...}", got)
	}
}

func TestFindContactByPhone_NotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"payload":[]}`))
	})
	got, err := c.FindContactByPhone(context.Background(), "no")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty payload, got %+v", got)
	}
}

func TestCreateContact_Roundtrip(t *testing.T) {
	var seenBody CreateContactReq
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		_, _ = w.Write([]byte(`{"payload":{"contact":{"id":99,"name":"Bob","phone_number":"+34622","identifier":"id-bob"}}}`))
	})
	got, err := c.CreateContact(context.Background(), CreateContactReq{
		InboxID: 3, Name: "Bob", PhoneNumber: "+34622", Identifier: "id-bob",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 99 {
		t.Fatalf("got = %+v, want id 99", got)
	}
	if seenBody.InboxID != 3 || seenBody.Name != "Bob" {
		t.Errorf("server saw %+v, want InboxID 3 Name Bob", seenBody)
	}
}

func TestPostMessage_PathAndPayload(t *testing.T) {
	var gotPath string
	var seen PostMessageReq
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seen)
		_, _ = w.Write([]byte(`{"id":1234}`))
	})
	resp, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 17, Content: "hola", MessageType: "incoming", SourceID: "WAID:abc",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.ID != 1234 {
		t.Errorf("id = %d, want 1234", resp.ID)
	}
	if gotPath != "/api/v1/accounts/7/conversations/17/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if seen.Content != "hola" || seen.MessageType != "incoming" || seen.SourceID != "WAID:abc" {
		t.Errorf("body = %+v", seen)
	}
}

func TestPostMessageWithAttachment_Multipart(t *testing.T) {
	var gotMime, gotContent, gotMsgType string
	var gotData []byte
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("expected multipart, got %q", ct)
		}
		_, params, _ := mime.ParseMediaType(ct)
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			switch part.FormName() {
			case "content":
				b, _ := io.ReadAll(part)
				gotContent = string(b)
			case "message_type":
				b, _ := io.ReadAll(part)
				gotMsgType = string(b)
			case "attachments[]":
				gotMime = part.Header.Get("Content-Type")
				gotData, _ = io.ReadAll(part)
			}
		}
		_, _ = w.Write([]byte(`{"id":555}`))
	})

	resp, err := c.PostMessageWithAttachment(context.Background(), PostMessageAttachmentReq{
		ConversationID: 9, Content: "look", MessageType: "outgoing",
		FileName: "x.jpg", MimeType: "image/jpeg", Data: []byte("PNGDATA"),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.ID != 555 {
		t.Errorf("id = %d", resp.ID)
	}
	if gotContent != "look" || gotMsgType != "outgoing" || gotMime != "image/jpeg" || string(gotData) != "PNGDATA" {
		t.Errorf("multipart payload mismatch: content=%q type=%q mime=%q data=%q", gotContent, gotMsgType, gotMime, gotData)
	}
}

func TestFindOpenConversation_PrefersOpenInInbox(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"payload":[
			{"id":1,"inbox_id":2,"status":"resolved"},
			{"id":2,"inbox_id":3,"status":"open"},
			{"id":3,"inbox_id":3,"status":"open"}
		]}`))
	})
	got, err := c.FindOpenConversation(context.Background(), 100, 3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 2 {
		t.Errorf("got = %+v, want first open conv in inbox 3 (id 2)", got)
	}
}

func TestFindOpenConversation_BareArrayResponse(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":7,"inbox_id":5,"status":"open"}]`))
	})
	got, err := c.FindOpenConversation(context.Background(), 1, 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 7 {
		t.Errorf("got = %+v, want id 7", got)
	}
}

func TestCreateConversation_Roundtrip(t *testing.T) {
	var seen CreateConversationReq
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		_, _ = w.Write([]byte(`{"id":88,"inbox_id":4,"status":"open"}`))
	})
	got, err := c.CreateConversation(context.Background(), CreateConversationReq{
		SourceID: "src", InboxID: 4, ContactID: 99,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 88 {
		t.Errorf("got = %+v", got)
	}
	if seen.SourceID != "src" || seen.InboxID != 4 || seen.ContactID != 99 {
		t.Errorf("seen = %+v", seen)
	}
}

func TestUpdateMessageSourceID(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.UpdateMessageSourceID(context.Background(), 11, 22, "WAID:zz"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/api/v1/accounts/7/conversations/11/messages/22" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["source_id"] != "WAID:zz" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestDownloadBlob_OK(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGBYTES"))
	})
	data, ct, err := c.DownloadBlob(context.Background(), srv.URL+"/files/x.png")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(data) != "PNGBYTES" {
		t.Errorf("data = %q", data)
	}
	if ct != "image/png" {
		t.Errorf("ct = %q", ct)
	}
}

func TestDownloadBlob_4xx(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, _, err := c.DownloadBlob(context.Background(), srv.URL+"/x")
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestListContactsByInbox_URLShape(t *testing.T) {
	// Verifica que el endpoint usa el path canónico de Chatwoot
	// `/contacts?inbox_id=X&page=Y` y NO el `/inboxes/X/contacts` que
	// devolvía 404 (bug arreglado en v0.32.1).
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payload":[{"id":1,"name":"x","identifier":"34600000000@s.whatsapp.net"}],"meta":{"count":1}}`))
	})

	contacts, hasMore, err := c.ListContactsByInbox(context.Background(), 11, 3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	wantPath := "/api/v1/accounts/7/contacts"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	// El order de los query params no está garantizado, pero ambos deben estar.
	if !strings.Contains(gotQuery, "inbox_id=11") {
		t.Errorf("query missing inbox_id=11: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "page=3") {
		t.Errorf("query missing page=3: %q", gotQuery)
	}
	if len(contacts) != 1 {
		t.Errorf("got %d contacts, want 1", len(contacts))
	}
	if hasMore {
		t.Errorf("hasMore = true on single contact, want false")
	}
}

func TestListContactsByInbox_HasMoreOnFullPage(t *testing.T) {
	// 15 contactos en una página → hasMore=true (page_size típico de Chatwoot).
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Construir un payload con 15 contactos válidos.
		var sb strings.Builder
		sb.WriteString(`{"payload":[`)
		for i := 0; i < 15; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`{"id":1,"name":"x","identifier":"34600000000@s.whatsapp.net"}`)
		}
		sb.WriteString(`]}`)
		_, _ = w.Write([]byte(sb.String()))
	})
	_, hasMore, err := c.ListContactsByInbox(context.Background(), 11, 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !hasMore {
		t.Errorf("hasMore = false on full page (15), want true")
	}
}
