package bridge

import (
	"testing"
	"time"
)

// Verifica el bug fix de v0.44.1: cuando el bot envía a un grupo, el
// groupTracker debe registrar "_bot" como último sender para que el
// siguiente msg del usuario lleve header. Pre-v0.44.1 los sends del
// bot no pasaban por groupTracker (no había events.Message), así que
// el burst del usuario continuaba sin reset.

func TestMarkBotSentInGroup_ResetsBurst(t *testing.T) {
	inc := &Incoming{
		groupTracker: newGroupSenderTracker(10 * time.Minute),
	}

	chat := "120363111@g.us"
	ricard := "34604021705@s.whatsapp.net"

	// 1) Ricard manda un msg al grupo → header (primer burst).
	if !inc.groupTracker.RecordAndCheck("inst1", chat, ricard) {
		t.Error("primer msg de Ricard debería emitir header")
	}

	// 2) Ricard manda otro → no header (mismo burst).
	if inc.groupTracker.RecordAndCheck("inst1", chat, ricard) {
		t.Error("segundo msg de Ricard NO debería emitir header (mismo burst)")
	}

	// 3) El bot manda algo → simula que llamamos MarkBotSentInGroup
	//    (esto es lo que hace v0.44.1 desde Outgoing).
	inc.MarkBotSentInGroup("inst1", chat)

	// 4) Ricard manda otro → AHORA sí debe emitir header (el bot
	//    rompió el burst).
	if !inc.groupTracker.RecordAndCheck("inst1", chat, ricard) {
		t.Error("tras bot reply, siguiente msg de Ricard debería emitir header")
	}
}

func TestMarkBotSentInGroup_NoOpWithoutTracker(t *testing.T) {
	// Sin groupTracker (TTL=0 en config), MarkBotSentInGroup es no-op
	// silencioso. No debe panic.
	inc := &Incoming{}
	inc.MarkBotSentInGroup("inst1", "120363111@g.us")
}

func TestMarkBotSentInGroup_DifferentChatNoCrossEffect(t *testing.T) {
	inc := &Incoming{
		groupTracker: newGroupSenderTracker(10 * time.Minute),
	}

	chatA := "120363AAA@g.us"
	chatB := "120363BBB@g.us"
	ricard := "34604021705@s.whatsapp.net"

	// Burst en chat A.
	inc.groupTracker.RecordAndCheck("inst1", chatA, ricard)
	inc.groupTracker.RecordAndCheck("inst1", chatA, ricard)

	// Bot manda en chat B → no debería afectar el burst de A.
	inc.MarkBotSentInGroup("inst1", chatB)

	// Siguiente msg de Ricard en chat A: mismo burst, NO header.
	if inc.groupTracker.RecordAndCheck("inst1", chatA, ricard) {
		t.Error("MarkBotSentInGroup en chat B no debería romper el burst de chat A")
	}
}
