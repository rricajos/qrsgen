package bridge

import (
	"sync"
	"time"
)

// avatarTracker mantiene, per-instancia + per-JID, el ID de la última
// foto sincronizada y cuándo fue la última vez que la chequeamos.
//
// Sirve para:
//   - Evitar re-chequeo frecuente (TTL): cada N horas como mucho.
//   - Detectar cambios reales: si el ID actual difiere del cacheado,
//     toca re-sincronizar.
//   - Evitar races: ShouldCheck es atómica — bumpea timestamp en `true`
//     para que dos goroutines concurrentes no spawnen ambas el sync.
//
// Estado en memoria — un restart de qrsgen reinicia los timestamps,
// así que todos los contactos vuelven a chequear en la siguiente
// pasada. Worst case: una tormenta de GetProfilePictureInfo tras
// restart (~1 call por contacto activo, no descargas full).
type avatarTracker struct {
	mu  sync.Mutex
	ttl time.Duration

	data map[string]avatarEntry // key = instance + "|" + jid
}

type avatarEntry struct {
	avatarID string    // ID (version/hash) de la última foto conocida; "" si no había
	syncedAt time.Time // timestamp del último chequeo (no del último download real)
}

func newAvatarTracker(ttl time.Duration) *avatarTracker {
	return &avatarTracker{
		ttl:  ttl,
		data: make(map[string]avatarEntry),
	}
}

// ShouldCheck devuelve true si debemos chequear el avatar para este JID.
// Si devuelve true, ATÓMICAMENTE bumpea el timestamp — esto previene
// que múltiples goroutines concurrentes (ej. burst de mensajes en grupo)
// spawnen el mismo sync varias veces.
//
// Trade-off: si el chequeo falla, no re-intentamos hasta el siguiente TTL.
// Es aceptable para avatares — vale más no machacar que tener UI perfecto.
func (t *avatarTracker) ShouldCheck(instance, jid string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + jid
	if e, ok := t.data[key]; ok && time.Since(e.syncedAt) <= t.ttl {
		return false
	}
	e := t.data[key]
	e.syncedAt = time.Now()
	t.data[key] = e
	return true
}

// LastID devuelve el último ID de avatar cacheado para este JID.
// Devuelve "" si nunca chequeamos o si el último chequeo dijo "sin foto".
func (t *avatarTracker) LastID(instance, jid string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + jid
	if e, ok := t.data[key]; ok {
		return e.avatarID
	}
	return ""
}

// UpdateID actualiza el ID cacheado tras un sync exitoso. Llamar SOLO
// si pudimos resolver el ID actual (incluso si dijo "sin foto" — un ""
// es una respuesta válida y queremos cachearla).
func (t *avatarTracker) UpdateID(instance, jid, avatarID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + jid
	e := t.data[key]
	e.avatarID = avatarID
	t.data[key] = e
}
