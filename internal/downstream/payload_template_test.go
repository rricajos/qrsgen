package downstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capturePostMessage levanta un servidor que captura el body del POST a
// /conversations/.../messages y devuelve un response stub con ID.
func capturePostMessage(t *testing.T, opts ...Option) (capturedBody func() []byte, c *Client) {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages") && r.Method == http.MethodPost {
			body, _ = io.ReadAll(r.Body)
		}
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	t.Cleanup(srv.Close)
	c = New(srv.URL, "tok-test", 7, opts...)
	return func() []byte { return body }, c
}

func TestPayloadTemplate_DisabledByDefault(t *testing.T) {
	// Sin template configurado → payload shape Chatwoot pre-v0.65.0.
	getBody, c := capturePostMessage(t)
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 100,
		Content:        "hola",
		MessageType:    "outgoing",
		SourceID:       "WAID:abc",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal body: %v\nbody: %s", err, getBody())
	}
	if got["content"] != "hola" || got["message_type"] != "outgoing" || got["source_id"] != "WAID:abc" {
		t.Errorf("default shape: got %+v", got)
	}
}

func TestPayloadTemplate_ChatwootShape_ProducesEquivalent(t *testing.T) {
	// Template que replica exactamente el shape Chatwoot default.
	// Sirve como sanity check de que el flujo template funciona.
	tpl := `{"content":{{printf "%q" .Content}},"message_type":{{printf "%q" .MessageType}},"source_id":{{printf "%q" .SourceID}}}`
	getBody, c := capturePostMessage(t, WithPayloadTemplate(tpl))
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 100,
		Content:        "hi",
		MessageType:    "outgoing",
		SourceID:       "WAID:x",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, getBody())
	}
	if got["content"] != "hi" || got["message_type"] != "outgoing" || got["source_id"] != "WAID:x" {
		t.Errorf("template Chatwoot-shape: got %+v", got)
	}
}

func TestPayloadTemplate_ZendeskLikeShape(t *testing.T) {
	// Template que produce un shape estilo Zendesk (ticket comment).
	tpl := `{"comment":{"html_body":{{printf "%q" .Content}},"public":true},"author_id":{{.ConversationID}}}`
	getBody, c := capturePostMessage(t, WithPayloadTemplate(tpl))
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 999,
		Content:        "hi from qrsgen",
		MessageType:    "outgoing",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, getBody())
	}
	comment, ok := got["comment"].(map[string]any)
	if !ok {
		t.Fatalf("expected comment{}: got %T", got["comment"])
	}
	if comment["html_body"] != "hi from qrsgen" || comment["public"] != true {
		t.Errorf("comment fields wrong: %+v", comment)
	}
	if got["author_id"] != float64(999) {
		t.Errorf("author_id = %v, want 999", got["author_id"])
	}
}

func TestPayloadTemplate_WithCreatedAtUnix(t *testing.T) {
	// El campo CreatedAtUnix se rellena cuando CreatedAt no es zero.
	tpl := `{"text":{{printf "%q" .Content}},"ts":{{.CreatedAtUnix}}}`
	getBody, c := capturePostMessage(t, WithPayloadTemplate(tpl))
	ts := time.Unix(1700000000, 0)
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1,
		Content:        "old msg",
		MessageType:    "incoming",
		CreatedAt:      ts,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, getBody())
	}
	if got["text"] != "old msg" || got["ts"] != float64(1700000000) {
		t.Errorf("got %+v, want text=old msg ts=1700000000", got)
	}
}

func TestPayloadTemplate_InvalidJSON_FallsBackToDefault(t *testing.T) {
	// Template sintácticamente Go-válido pero produce JSON inválido
	// (falta comma). El cliente debe fallback al payload default
	// Chatwoot — no romper el msg.
	tpl := `{"content": {{printf "%q" .Content}} "extra":"missing comma"}`
	getBody, c := capturePostMessage(t, WithPayloadTemplate(tpl))
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1,
		Content:        "fallback test",
		MessageType:    "outgoing",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal default fallback body: %v\nbody: %s", err, getBody())
	}
	// Fallback al Chatwoot shape: el body válido tiene "content" raw.
	if got["content"] != "fallback test" {
		t.Errorf("expected fallback to Chatwoot shape with content=fallback test; got %+v", got)
	}
}

func TestPayloadTemplate_ExecuteFailure_FallsBackToDefault(t *testing.T) {
	// Template con referencia a un campo inexistente → execute falla
	// → fallback al shape default.
	tpl := `{"content":{{.NoSuchField}}}`
	getBody, c := capturePostMessage(t, WithPayloadTemplate(tpl))
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1,
		Content:        "execute fail test",
		MessageType:    "outgoing",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal fallback: %v body=%s", err, getBody())
	}
	if got["content"] != "execute fail test" {
		t.Errorf("expected default Chatwoot shape after execute fail; got %+v", got)
	}
}

func TestPayloadTemplate_ParseFailure_NoOp(t *testing.T) {
	// Template con sintaxis inválida Go template → WithPayloadTemplate
	// loguea warning y NO asigna template. Client opera en modo default.
	tpl := `{{ this is not valid template syntax`
	getBody, c := capturePostMessage(t, WithPayloadTemplate(tpl))
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1,
		Content:        "parse fail test",
		MessageType:    "outgoing",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, getBody())
	}
	if got["content"] != "parse fail test" {
		t.Errorf("expected default Chatwoot shape after parse fail; got %+v", got)
	}
}

func TestPayloadTemplate_FallbackHook_InvalidJSON(t *testing.T) {
	// El hook debe invocarse con reason="invalid_json" cuando el template
	// ejecuta OK pero el output no es JSON parseable.
	tpl := `{"content": {{printf "%q" .Content}} "extra":"missing comma"}`
	var hookReasons []string
	hook := func(r string) { hookReasons = append(hookReasons, r) }
	_, c := capturePostMessage(t, WithPayloadTemplate(tpl), WithTemplateFallbackHook(hook))
	_, _ = c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1, Content: "x", MessageType: "outgoing",
	})
	if len(hookReasons) != 1 || hookReasons[0] != "invalid_json" {
		t.Errorf("hookReasons = %v, want [invalid_json]", hookReasons)
	}
}

func TestPayloadTemplate_FallbackHook_ExecuteFailed(t *testing.T) {
	// El hook debe invocarse con reason="execute_failed" cuando el template
	// Execute() falla (e.g. variable inexistente).
	tpl := `{"content":{{.NoSuchField}}}`
	var hookReasons []string
	hook := func(r string) { hookReasons = append(hookReasons, r) }
	_, c := capturePostMessage(t, WithPayloadTemplate(tpl), WithTemplateFallbackHook(hook))
	_, _ = c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1, Content: "x", MessageType: "outgoing",
	})
	if len(hookReasons) != 1 || hookReasons[0] != "execute_failed" {
		t.Errorf("hookReasons = %v, want [execute_failed]", hookReasons)
	}
}

func TestPayloadTemplate_FallbackHook_NotCalledOnSuccess(t *testing.T) {
	// El hook NO debe invocarse cuando el template funciona OK.
	tpl := `{"content":{{printf "%q" .Content}}}`
	var hookReasons []string
	hook := func(r string) { hookReasons = append(hookReasons, r) }
	_, c := capturePostMessage(t, WithPayloadTemplate(tpl), WithTemplateFallbackHook(hook))
	_, _ = c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1, Content: "ok", MessageType: "outgoing",
	})
	if len(hookReasons) != 0 {
		t.Errorf("hookReasons = %v, expected no calls", hookReasons)
	}
}

func TestValidatePayloadTemplate(t *testing.T) {
	cases := []struct {
		name      string
		tpl       string
		expectErr bool
	}{
		{"empty string ok (no-op)", "", false},
		{"valid simple template", `{"text":{{printf "%q" .Content}}}`, false},
		{"valid with multiple vars", `{"a":{{.Content}},"b":{{.ConversationID}}}`, false},
		{"unclosed action", `{{ .Content`, true},
		{"unknown function", `{{ this_is_not_a_function .Content }}`, true},
		{"malformed if", `{{if .Content}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePayloadTemplate(tc.tpl)
			if (err != nil) != tc.expectErr {
				t.Errorf("ValidatePayloadTemplate(%q) err=%v, expectErr=%v", tc.tpl, err, tc.expectErr)
			}
		})
	}
}

func TestPayloadTemplate_EmptyString_NoOp(t *testing.T) {
	// Template vacío = no aplicar template = comportamiento default.
	getBody, c := capturePostMessage(t, WithPayloadTemplate(""))
	_, err := c.PostMessage(context.Background(), PostMessageReq{
		ConversationID: 1,
		Content:        "empty tpl",
		MessageType:    "outgoing",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(getBody(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, getBody())
	}
	if got["content"] != "empty tpl" {
		t.Errorf("expected default with empty tpl; got %+v", got)
	}
}
