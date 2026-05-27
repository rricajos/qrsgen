package downstream

import (
	"context"
	"testing"
)

// Client.For es la implementación trivial usada en single-downstream: el
// Client se devuelve a sí mismo. Esto permite que código que solo tiene un
// *Client implemente Router sin cambios.
func TestClient_For_ReturnsSelf(t *testing.T) {
	c := New("http://example.test", "tok", 1)
	got := c.For(context.Background(), "any-instance")
	if got != c {
		t.Errorf("Client.For should return self, got %p (want %p)", got, c)
	}
}

// Registry.For con nil receiver o fallback nil cae al fallback (que también
// puede ser nil). Garantiza que el callsite no haga panic con un Registry
// no-inicializado.
func TestRegistry_For_NilReceiver_ReturnsFallback(t *testing.T) {
	var r *Registry
	if got := r.For(context.Background(), "instance"); got != nil {
		t.Errorf("nil Registry should return nil, got %v", got)
	}
}

// Registry.For sin pool ni tenants pero con fallback: si el instance está
// vacío (no owner_tag resolvable), devolvemos el fallback global.
func TestRegistry_For_EmptyInstance_UsesFallback(t *testing.T) {
	fallback := New("http://fallback.test", "tok", 1)
	r := &Registry{
		fallback:    fallback,
		clients:     map[string]*Client{},
		instanceTag: map[string]instanceTagEntry{},
	}
	got := r.For(context.Background(), "")
	if got != fallback {
		t.Errorf("empty instance should fall back to global, got %p (want %p)", got, fallback)
	}
}

// OwnerTagFor en *Client siempre devuelve "" (single-downstream sin tenants).
func TestClient_OwnerTagFor_AlwaysEmpty(t *testing.T) {
	c := New("http://example.test", "tok", 1)
	if got := c.OwnerTagFor(context.Background(), "any"); got != "" {
		t.Errorf("Client.OwnerTagFor should always be empty, got %q", got)
	}
}

// OwnerTagFor en *Registry con nil receiver devuelve "" (defensa).
func TestRegistry_OwnerTagFor_NilReceiver(t *testing.T) {
	var r *Registry
	if got := r.OwnerTagFor(context.Background(), "instance"); got != "" {
		t.Errorf("nil Registry.OwnerTagFor should be empty, got %q", got)
	}
}

// OwnerTagFor en *Registry con instance vacío devuelve "" (vía ownerTagFor interno).
func TestRegistry_OwnerTagFor_EmptyInstance(t *testing.T) {
	r := &Registry{
		clients:     map[string]*Client{},
		instanceTag: map[string]instanceTagEntry{},
	}
	if got := r.OwnerTagFor(context.Background(), ""); got != "" {
		t.Errorf("OwnerTagFor with empty instance should be empty, got %q", got)
	}
}
