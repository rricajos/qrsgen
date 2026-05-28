package bridge

import (
	"sync"
	"time"
)

// groupSenderTracker recuerda el último remitente que mandó mensaje a
// cada grupo por instancia. Permite suprimir el header de remitente en
// mensajes consecutivos del mismo participante, replicando la
// convención visual de WhatsApp (header en el primer msg del burst,
// nada en los siguientes).
//
// La key es "instance|chatJID" (grupo). Si el último sender es distinto
// O ha pasado más de TTL desde el último mensaje, se considera "nuevo
// burst" y se emite header.
//
// Estado en memoria — un restart de qrsgen reinicia los burst counters.
// Worst case: un header extra aparece tras restart (cosmético).
type groupSenderTracker struct {
	mu  sync.Mutex
	ttl time.Duration

	data map[string]groupSenderEntry
}

type groupSenderEntry struct {
	senderJID string
	at        time.Time
}

func newGroupSenderTracker(ttl time.Duration) *groupSenderTracker {
	return &groupSenderTracker{
		ttl:  ttl,
		data: make(map[string]groupSenderEntry),
	}
}

// RecordAndCheck registra al sender actual y devuelve true si debe
// emitirse el header (sender distinto al anterior, o ha pasado TTL,
// o no había sender previo registrado).
//
// SIEMPRE actualiza la entry — el último timestamp registrado es
// el de la llamada actual, no el del primer burst.
func (t *groupSenderTracker) RecordAndCheck(instance, chatJID, senderJID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := instance + "|" + chatJID
	now := time.Now()
	e, ok := t.data[key]
	isNewBurst := !ok || e.senderJID != senderJID || now.Sub(e.at) > t.ttl
	t.data[key] = groupSenderEntry{senderJID: senderJID, at: now}
	return isNewBurst
}
