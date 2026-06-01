package downstream

import (
	"strings"
	"testing"
)

// Tests del SSRF protection en DownloadBlob — v0.64.6 cierra CodeQL
// alert "Uncontrolled data used in network request" (Critical).
func TestValidateBlobURL_SameHost(t *testing.T) {
	c := New("https://omnia.example.com", "tok", 1)
	cases := []string{
		"https://omnia.example.com/rails/active_storage/blobs/xxx/file.jpg",
		"https://omnia.example.com/api/v1/contacts/1/avatar",
		"http://omnia.example.com/foo", // http vs https mismatch ok per validation (no scheme strictness)
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			if err := c.validateBlobURL(u); err != nil {
				t.Errorf("expected OK, got error: %v", err)
			}
		})
	}
}

func TestValidateBlobURL_RejectExternalHost(t *testing.T) {
	c := New("https://omnia.example.com", "tok", 1)
	cases := []struct {
		name string
		url  string
	}{
		{"different host", "https://evil.com/payload"},
		{"subdomain phishing", "https://omnia.example.com.attacker.com/foo"},
		{"empty host", "https:///foo"},
		{"unsupported scheme ftp", "ftp://omnia.example.com/foo"},
		{"unsupported scheme file", "file:///etc/passwd"},
		{"malformed", "://not-a-url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.validateBlobURL(tc.url)
			if err == nil {
				t.Errorf("expected reject, got nil")
			}
		})
	}
}

func TestValidateBlobURL_HostCaseInsensitive(t *testing.T) {
	c := New("https://Omnia.Example.com", "tok", 1)
	if err := c.validateBlobURL("https://omnia.example.com/foo"); err != nil {
		t.Errorf("host comparison should be case-insensitive: %v", err)
	}
}

func TestValidateBlobURL_HostMatchErrorMentionsSSRF(t *testing.T) {
	c := New("https://omnia.example.com", "tok", 1)
	err := c.validateBlobURL("https://evil.com/foo")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("error should mention SSRF for operator clarity: %v", err)
	}
}
