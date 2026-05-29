package bridge

import (
	"sync"
	"time"
)

// waidTracker recuerda los WAIDs (WhatsApp message IDs) de mensajes
// incoming por conversación. Permite que cuando el agente lea la conv
// en el downstream (evento `conversation_updated`), qrsgen tenga la
// lista de qué mensajes marcar como leídos en WhatsApp via MarkRead.
//
// Estado en memoria — restart de qrsgen pierde el historial. Worst case:
// el cliente no ve doble check azul por msgs leídos durante el restart.
// Cosmético, no funcional.
//
// Cap por conv: últimos N WAIDs (default 50). Para casos típicos de
// conv 1-on-1 esto es más que suficiente; chats con burst muy intensos
// pueden perder mensajes antiguos del tracker, pero MarkRead solo
// importa para mensajes recientes (los viejos ya tienen "delivered"
// y al cliente le da igual).
type waidTracker struct {
	mu        sync.Mutex
	capPerKey int

	data map[string][]waidEntry // key = instance + "|" + chatJID
}

type waidEntry struct {
	waid     string
	senderJID string // quién envió el msg original (para MarkRead en grupos)
	at        time.Time
}

func newWAIDTracker(capPerKey int) *waidTracker {
	if capPerKey <= 0 {
		capPerKey = 50
	}
	return &waidTracker{
		capPerKey: capPerKey,
		data:      make(map[string][]waidEntry),
	}
}

// RecordIncoming registra un WAID nuevo para una conv. Si la conv supera
// capPerKey, se descartan los más viejos (FIFO).
func (t *waidTracker) RecordIncoming(instance, chatJID, waid, senderJID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + chatJID
	entries := t.data[key]
	entries = append(entries, waidEntry{waid: waid, senderJID: senderJID, at: at})
	if len(entries) > t.capPerKey {
		entries = entries[len(entries)-t.capPerKey:]
	}
	t.data[key] = entries
}

// DrainBefore devuelve y elimina del tracker todos los WAIDs registrados
// para la conv ANTES (o exactamente igual que) cutoffTS. Sirve para
// llamar MarkRead con todos los msgs que el agente ya leyó.
//
// El segundo valor es el senderJID típico de esos msgs — para 1-on-1 es
// el contacto, para grupos puede variar. Solo devolvemos uno; en grupos
// con varios senders el caller debe agrupar por sí mismo (no nos
// preocupa ahora porque MarkRead acepta un único senderJID).
func (t *waidTracker) DrainBefore(instance, chatJID string, cutoffTS time.Time) ([]string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := instance + "|" + chatJID
	entries := t.data[key]
	if len(entries) == 0 {
		return nil, ""
	}
	var drained []string
	var kept []waidEntry
	senderJID := ""
	for _, e := range entries {
		if !e.at.After(cutoffTS) {
			drained = append(drained, e.waid)
			if senderJID == "" {
				senderJID = e.senderJID
			}
		} else {
			kept = append(kept, e)
		}
	}
	t.data[key] = kept
	return drained, senderJID
}
