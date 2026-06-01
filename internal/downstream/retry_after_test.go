package downstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseRetryAfter_Seconds(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"5", 5 * time.Second},
		{"60", time.Minute},
		{"3600", time.Hour},
		{"not-a-number", 0},
		{"-1", 0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseRetryAfter(tc.in)
			if got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	// HTTP-date 30s in the future → ~30s.
	future := time.Now().Add(30 * time.Second).UTC()
	got := parseRetryAfter(future.Format(http.TimeFormat))
	// Tolerancia generosa (parsing y resta consumen tiempo).
	if got < 25*time.Second || got > 35*time.Second {
		t.Errorf("parseRetryAfter(HTTP-date +30s) = %v, want ~30s", got)
	}

	// HTTP-date en el pasado → 0 (no negativa).
	past := time.Now().Add(-1 * time.Minute).UTC()
	if got := parseRetryAfter(past.Format(http.TimeFormat)); got != 0 {
		t.Errorf("parseRetryAfter(past) = %v, want 0", got)
	}
}

func TestClient_429_ReturnsRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"too many requests"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", 1)
	_, err := c.request(context.Background(), http.MethodGet, "/contacts", nil)
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *RateLimitError, got %T: %v", err, err)
	}
	if rl.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter = %v, want 5s", rl.RetryAfter)
	}
	if rl.Body == "" {
		t.Error("Body should include response payload")
	}
}

func TestClient_429_NoRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", 1)
	_, err := c.request(context.Background(), http.MethodGet, "/contacts", nil)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if rl.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (no header)", rl.RetryAfter)
	}
}

func TestClient_500_NotRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", 1)
	_, err := c.request(context.Background(), http.MethodGet, "/contacts", nil)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		t.Errorf("500 should NOT be RateLimitError, got %v", err)
	}
}
