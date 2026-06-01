package outbox

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.TTL != 5*time.Minute {
		t.Errorf("TTL: got %v, want 5m", c.TTL)
	}
	if c.MaxAttempts != 5 {
		t.Errorf("MaxAttempts: got %d, want 5", c.MaxAttempts)
	}
	if c.DrainInterval != 5*time.Second {
		t.Errorf("DrainInterval: got %v, want 5s", c.DrainInterval)
	}
	if c.ExpireInterval != 30*time.Second {
		t.Errorf("ExpireInterval: got %v, want 30s", c.ExpireInterval)
	}
	if c.MaxQueueDepth != 200 {
		t.Errorf("MaxQueueDepth: got %d, want 200", c.MaxQueueDepth)
	}
}

func TestOutbox_SetEncryptionKey(t *testing.T) {
	o := &Outbox{}

	// nil/empty → opt-out, no error.
	if err := o.SetEncryptionKey(nil); err != nil {
		t.Errorf("nil key should be no-op, got err: %v", err)
	}
	if o.encryptionKey != nil {
		t.Error("nil key should clear encryption")
	}

	if err := o.SetEncryptionKey([]byte{}); err != nil {
		t.Errorf("empty key should be no-op, got err: %v", err)
	}

	// Wrong length → rejected.
	if err := o.SetEncryptionKey([]byte("too-short")); err == nil {
		t.Error("expected error for short key, got nil")
	}
	if o.encryptionKey != nil {
		t.Error("rejected key should not be stored")
	}

	// Right length → accepted.
	good := make([]byte, EncryptionKeySize)
	for i := range good {
		good[i] = byte(i)
	}
	if err := o.SetEncryptionKey(good); err != nil {
		t.Errorf("valid key should accept, got err: %v", err)
	}
	if len(o.encryptionKey) != EncryptionKeySize {
		t.Errorf("key stored len=%d, want %d", len(o.encryptionKey), EncryptionKeySize)
	}

	// Switching back to opt-out.
	if err := o.SetEncryptionKey(nil); err != nil {
		t.Errorf("re-clear: %v", err)
	}
	if o.encryptionKey != nil {
		t.Error("re-clear should null the field")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"", 5, ""},
		{"hi", 5, "hi"},
		{"hello world", 5, "hello"},
		{"exactly10x", 10, "exactly10x"},
		{"😀😀😀😀😀😀", 4, "😀"}, // ASCII byte count — truncate is byte-based
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := truncate(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncate(%q,%d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestPreviewFromPayload(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", `{}`, ""},
		{"short content", `{"content":"hola"}`, "hola"},
		{"exactly60", `{"content":"` + strings.Repeat("a", 60) + `"}`, strings.Repeat("a", 60)},
		{"over60", `{"content":"` + strings.Repeat("a", 70) + `"}`, strings.Repeat("a", 60) + "…"},
		{"invalid json", `not json`, ""},
		{"missing content", `{"other":"x"}`, ""},
		{"emoji content over60", `{"content":"` + strings.Repeat("😀", 70) + `"}`, strings.Repeat("😀", 60) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := previewFromPayload([]byte(tc.in))
			if got != tc.want {
				t.Errorf("previewFromPayload(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
