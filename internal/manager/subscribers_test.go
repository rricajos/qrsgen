package manager

import (
	"testing"
)

func TestWebhookSubscriber_Matches(t *testing.T) {
	cases := []struct {
		name   string
		sub    WebhookSubscriber
		event  string
		expect bool
	}{
		{
			name:   "empty events list = wildcard, matches anything",
			sub:    WebhookSubscriber{URL: "http://x", Events: nil},
			event:  "connected",
			expect: true,
		},
		{
			name:   "explicit empty slice = wildcard",
			sub:    WebhookSubscriber{URL: "http://x", Events: []string{}},
			event:  "paired",
			expect: true,
		},
		{
			name:   "single event match",
			sub:    WebhookSubscriber{URL: "http://x", Events: []string{"connected"}},
			event:  "connected",
			expect: true,
		},
		{
			name:   "single event miss",
			sub:    WebhookSubscriber{URL: "http://x", Events: []string{"connected"}},
			event:  "disconnected",
			expect: false,
		},
		{
			name:   "multi-event match",
			sub:    WebhookSubscriber{URL: "http://x", Events: []string{"paired", "connected", "disconnected"}},
			event:  "disconnected",
			expect: true,
		},
		{
			name:   "multi-event miss",
			sub:    WebhookSubscriber{URL: "http://x", Events: []string{"paired", "connected"}},
			event:  "logged_out",
			expect: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sub.matches(tc.event); got != tc.expect {
				t.Errorf("matches(%q) = %v, want %v", tc.event, got, tc.expect)
			}
		})
	}
}

func TestResolveSubscribers_JSONBPriority(t *testing.T) {
	// Cuando JSONB tiene elementos válidos, gana sobre legacy URL.
	blob := []byte(`[{"url":"http://a"},{"url":"http://b","events":["connected"]}]`)
	subs, err := resolveSubscribers(blob, "http://legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("len(subs) = %d, want 2", len(subs))
	}
	if subs[0].URL != "http://a" || len(subs[0].Events) != 0 {
		t.Errorf("subs[0] = %+v, want url=http://a no events", subs[0])
	}
	if subs[1].URL != "http://b" || len(subs[1].Events) != 1 || subs[1].Events[0] != "connected" {
		t.Errorf("subs[1] = %+v, want url=http://b events=[connected]", subs[1])
	}
}

func TestResolveSubscribers_EmptyJSONBFallbackLegacy(t *testing.T) {
	// JSONB vacío → fallback legacy URL como single-subscriber.
	subs, err := resolveSubscribers([]byte(`[]`), "http://legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 1 || subs[0].URL != "http://legacy" {
		t.Errorf("subs = %+v, want single {url:legacy}", subs)
	}
}

func TestResolveSubscribers_NilJSONBFallbackLegacy(t *testing.T) {
	// Sin blob (NULL en DB) y con legacy → single subscriber legacy.
	subs, err := resolveSubscribers(nil, "http://legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 1 || subs[0].URL != "http://legacy" {
		t.Errorf("subs = %+v, want single {url:legacy}", subs)
	}
}

func TestResolveSubscribers_NoSubsNoLegacy(t *testing.T) {
	// Ningún subscriber configurado → nil. emitLifecycleWebhook saldrá temprano.
	subs, err := resolveSubscribers(nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subs != nil {
		t.Errorf("subs = %+v, want nil", subs)
	}
	subs, err = resolveSubscribers([]byte(`[]`), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subs != nil {
		t.Errorf("subs = %+v (with empty []), want nil", subs)
	}
}

func TestResolveSubscribers_InvalidJSON(t *testing.T) {
	// JSON malformado → error. El caller debe decidir si fallback o no.
	_, err := resolveSubscribers([]byte(`not json`), "http://legacy")
	if err == nil {
		t.Errorf("expected error on invalid JSON, got nil")
	}
}
