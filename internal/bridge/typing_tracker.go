package bridge

import (
	"sync"
	"time"
)

// typingTracker evita repostear typing events idénticos consecutivos al
// downstream. Mantiene per-conv un (estado, timestamp) y solo propaga
// si cambia el estado o si el throttleWindow ha pasado.
//
// La frecuencia de eventos ChatPresence de WhatsApp varía: puede llegar
// varias veces por segundo durante una sesión de escritura. Sin
// throttle, saturamos al downstream con calls que dicen lo mismo.
type typingTracker struct {
	mu sync.Mutex

	// minInterval entre llamadas para el mismo (conv, state). Si el
	// usuario tipea continuamente, refrescamos el typing indicator del
	// downstream cada minInterval para que no expire visualmente.
	minInterval time.Duration

	data map[int]typingEntry // key = downstream convID
}

type typingEntry struct {
	composing bool
	at        time.Time
}

func newTypingTracker(minInterval time.Duration) *typingTracker {
	return &typingTracker{
		minInterval: minInterval,
		data:        make(map[int]typingEntry),
	}
}

// ShouldEmit devuelve true si debe propagarse el estado al downstream.
// Atómicamente registra el nuevo estado/timestamp. Lógica:
//   - estado distinto al anterior (composing↔paused) → emitir
//   - mismo estado pero ha pasado minInterval → emitir (refresh)
//   - mismo estado dentro de minInterval → no emitir (anti-spam)
func (t *typingTracker) ShouldEmit(convID int, composing bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	e, ok := t.data[convID]
	if !ok {
		t.data[convID] = typingEntry{composing: composing, at: now}
		return true
	}
	if e.composing != composing {
		t.data[convID] = typingEntry{composing: composing, at: now}
		return true
	}
	if now.Sub(e.at) >= t.minInterval {
		t.data[convID] = typingEntry{composing: composing, at: now}
		return true
	}
	return false
}
