package bridge

import (
	"sync"
	"time"
)

// msgHistoryTracker recuerda los mensajes incoming posteados al downstream,
// con todos los datos necesarios para reconstruir su contenido si la info
// del sender (nombre / saved status) cambia más tarde.
//
// Permite el feature de retroactive name update (v0.40.0): cuando el dueño
// del bot añade un contacto a su agenda de WhatsApp tras haber recibido
// mensajes de él, qrsgen puede ir y reescribir los mensajes ya posteados
// para que el nuevo nombre / sin tilde aparezca también en el histórico.
//
// Estado in-memory. Un restart de qrsgen pierde el histórico tracked, lo
// que significa que mensajes posteados antes del restart no se actualizarán
// retroactivamente cuando cambien sus senders. Aceptable como MVP.
type msgHistoryTracker struct {
	mu sync.Mutex

	// Cap por sender — los más viejos se descartan FIFO.
	capPerSender int

	// data: key = instance + "|" + senderJID (no-AD)
	data map[string][]trackedMsg
}

// trackedMsg captura toda la info necesaria para reconstruir el content
// del mensaje cuando el sender cambia de saved-status o de nombre.
type trackedMsg struct {
	convID     int       // conversation_id en el downstream
	msgID      int       // message_id en el downstream
	phone      string    // teléfono formateado (E.164 con +) usado en el header
	nameUsed   string    // nombre usado al postear
	wasSaved   bool      // saved status al postear
	body       string    // body del mensaje (sin el prefix de header)
	postedAt   time.Time // timestamp del post
}

func newMsgHistoryTracker(capPerSender int) *msgHistoryTracker {
	if capPerSender <= 0 {
		capPerSender = 100
	}
	return &msgHistoryTracker{
		capPerSender: capPerSender,
		data:         make(map[string][]trackedMsg),
	}
}

// Record persiste un mensaje en el tracker. Llamar tras un PostMessage
// exitoso. El cap por sender se enforce con FIFO — los más viejos caen
// primero al desbordar.
func (t *msgHistoryTracker) Record(instance, senderJID string, m trackedMsg) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + senderJID
	entries := t.data[key]
	entries = append(entries, m)
	if len(entries) > t.capPerSender {
		entries = entries[len(entries)-t.capPerSender:]
	}
	t.data[key] = entries
}

// ListBySender devuelve una copia de los mensajes tracked para un sender.
// Útil para el retroactive update: iteramos esta lista y por cada uno
// chequeamos si el name actual difiere del nameUsed al postear.
func (t *msgHistoryTracker) ListBySender(instance, senderJID string) []trackedMsg {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + senderJID
	entries := t.data[key]
	if len(entries) == 0 {
		return nil
	}
	out := make([]trackedMsg, len(entries))
	copy(out, entries)
	return out
}

// UpdateAfterPatch actualiza el nameUsed/wasSaved/etc de una entry tras
// haberla reescrito via PATCH en el downstream. Mantiene el tracker
// coherente para que un cambio futuro (otra rename) detecte la diff
// contra el nombre nuevo, no el antiguo.
func (t *msgHistoryTracker) UpdateAfterPatch(instance, senderJID string, msgID int, newName string, newSaved bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + senderJID
	entries := t.data[key]
	for i, e := range entries {
		if e.msgID == msgID {
			entries[i].nameUsed = newName
			entries[i].wasSaved = newSaved
			break
		}
	}
	t.data[key] = entries
}
