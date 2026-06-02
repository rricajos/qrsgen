package bridge

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/types"

	"github.com/rricajos/qrsgen/internal/downstream"
	"github.com/rricajos/qrsgen/internal/metrics"
	"github.com/rricajos/qrsgen/internal/wameow"
)


// HandleContactUpdate procesa un *events.Contact de whatsmeow cuando
// un contacto se añade/edita en la agenda local del dueño del bot
// (v0.40.0 retroactive name update).
//
// Si la feature está activa (i.msgHistory != nil) y hay mensajes
// tracked para este sender, reescribe el `content` de cada uno en el
// downstream con el nombre nuevo / sin tilde.
//
// Se ignora `fromFullSync=true`: al conectar, whatsmeow dispara un
// event por cada contacto de la agenda — saltarlos evita un burst de
// cientos/miles de PATCHes innecesarios (el state no cambió, es solo
// la propagación inicial).
func (i *Incoming) HandleContactUpdate(ctx context.Context, instance string, jid types.JID, fullName, firstName string, fromFullSync bool, r wameow.WAResolver) {
	if i.msgHistory == nil {
		metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "skip_disabled", instance).Inc()
		return
	}
	if fromFullSync {
		// El sync inicial al conectar emite uno por contacto de la agenda.
		// PATCHearlos todos sería un burst inútil — el state no cambió.
		metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "skip_fullsync", instance).Inc()
		return
	}

	// Nombre nuevo: preferimos FullName, fallback a FirstName. Ambos
	// vacíos = contacto eliminado de la agenda; no tenemos el PushName
	// original guardado, así que saltamos en lugar de romper el
	// histórico con un nombre vacío.
	newName := strings.TrimSpace(fullName)
	if newName == "" {
		newName = strings.TrimSpace(firstName)
	}
	if newName == "" {
		i.logger.Debug("retroactive update: empty name — skipping",
			"instance", instance, "jid", jid)
		metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "skip_empty_name", instance).Inc()
		return
	}

	key := jid.ToNonAD().String()
	entries := i.msgHistory.ListBySender(instance, key)

	ds := i.ds.For(ctx, instance)
	if ds == nil {
		i.logger.Warn("retroactive update: no downstream client",
			"instance", instance, "jid", jid)
		metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "ds_error", instance).Inc()
		return
	}

	// senderInfo para renderizar el header nuevo. events.Contact siempre
	// llega con PN como JID, así que jid.User es el teléfono directo.
	si := senderInfo{
		name:     newName,
		saved:    true,
		phone:    jid.User,
		phoneFmt: formatE164(jid.User),
	}
	newPrefix, ok := renderSenderHeader(si, i.headerTemplate)
	if !ok {
		return
	}

	// v0.43.0: tanto el rename del contacto en Chatwoot como el PATCH
	// loop de mensajes históricos van en la misma goroutine. El contact
	// rename aplica también al caso 1:1 (cuando no hay mensajes de
	// grupo tracked); el PATCH loop solo si hay entries.
	//
	// Si no hay entries Y no podemos encontrar contacto, salimos
	// silenciosos. La métrica skip_no_entries la registramos solo si
	// además no se intentó el rename — lo decide la goroutine.
	i.retroactiveWG.Add(1)
	go i.applyRetroactiveUpdates(ctx, instance, jid, key, newName, newPrefix, entries, ds)
}

// applyRetroactiveUpdates aplica retroactivamente el nuevo nombre del
// contacto al downstream:
//  1. Renombra el contact en Chatwoot (PUT /contacts/{id} con name).
//     v0.43.0: aplica también al caso 1:1, no solo a grupos.
//  2. PATCHea cada msg en `entries` con el header actualizado (cuando
//     el sender pasó de no-saved a saved, o cambió de nombre).
//
// Llamada vía goroutine desde HandleContactUpdate (no bloquear el
// event loop de wameow). El contact rename es best-effort: si falla,
// loguea warning y sigue con los msgs.
func (i *Incoming) applyRetroactiveUpdates(
	ctx context.Context,
	instance string,
	jid types.JID,
	key, newName, newPrefix string,
	entries []trackedMsg,
	ds downstream.DownstreamAPI,
) {
	defer i.retroactiveWG.Done()

	// v0.43.0: rename del contacto en Chatwoot. Buscamos por phone
	// (events.Contact siempre llega con PN). Si no existe (caso típico:
	// recibimos del LID y nunca creamos contact por PN), saltamos sin
	// error — se creará con el nombre correcto en el próximo msg
	// gracias al flujo normal de sync().
	contact, err := findContactByIdentifier(ctx, ds, jid.String(), jid.User)
	if err != nil {
		i.logger.Warn("retroactive update: find contact failed",
			"err", err, "instance", instance, "jid", jid)
		// No abortamos — los msgs PATCH no dependen del lookup.
	} else if contact != nil && contact.Name != newName {
		if err := ds.UpdateContactName(ctx, contact.ID, newName); err != nil {
			i.logger.Warn("retroactive contact rename failed",
				"err", err, "instance", instance,
				"contactID", contact.ID, "newName", newName)
			metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "ds_error", instance).Inc()
		} else {
			metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "ok", instance).Inc()
			i.logger.Info("retroactive contact rename applied",
				"instance", instance, "jid", jid,
				"contactID", contact.ID, "oldName", contact.Name, "newName", newName)
		}
	}

	// PATCH loop de mensajes históricos de grupo (v0.40.0).
	if len(entries) == 0 {
		metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "skip_no_entries", instance).Inc()
		return
	}
	patched := 0
	for _, e := range entries {
		// v0.44.0: solo aplicamos retroactive PATCH a msgs que se
		// postearon con prefix de grupo. Las 1:1 son tracked solo
		// por el mapeo Chatwoot↔WAID; no llevan prefix que reescribir.
		if !e.hasPrefix {
			continue
		}
		if e.nameUsed == newName && e.wasSaved {
			continue
		}
		newContent := newPrefix
		if e.body != "" {
			newContent = newPrefix + i.headerSep + e.body
		}
		if err := ds.UpdateMessageContent(ctx, e.convID, e.msgID, newContent); err != nil {
			i.logger.Warn("retroactive update PATCH failed",
				"err", err, "instance", instance,
				"convID", e.convID, "msgID", e.msgID)
			metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "ds_error", instance).Inc()
			continue
		}
		i.msgHistory.UpdateAfterPatch(instance, key, e.msgID, newName, true)
		metrics.RealtimeEventsTotal.WithLabelValues("retroactive_name", "ok", instance).Inc()
		patched++
	}
	if patched > 0 {
		i.logger.Info("retroactive name update applied",
			"instance", instance, "jid", jid,
			"newName", newName, "patched", patched, "scanned", len(entries))
	}
}

// ReconcileResult resume una pasada de bulk reconcile.
type ReconcileResult struct {
	Instance  string `json:"instance"`
	Scanned   int    `json:"scanned"`   // contactos saved iterados del store WA
	Triggered int    `json:"triggered"` // HandleContactUpdate calls disparados
}

// ReconcileSavedContacts itera el contact store local de whatsmeow
// y dispara un HandleContactUpdate por cada entry saved. Sirve para:
//   - Bootstrap inicial tras adoptar v0.40.0+ (la agenda WA ya tenía
//     contactos antes de que qrsgen empezara a rastrear).
//   - Backfill tras restart sin persistencia (v0.40.x sin v0.41.0
//     persistence).
//   - Reconciliación manual vía endpoint admin si el agente nota
//     contactos saved en WA pero no renombrados en Chatwoot.
//
// Cada HandleContactUpdate corre como si viniera de un events.Contact
// con fromFullSync=false (es decir, SÍ dispara PATCHes). Si hay 1000
// contactos saved y 50 con mensajes tracked, eso son 50 PATCH loops
// en goroutines. retroactiveWG las rastrea — WaitRetroactivePatches
// permite esperar al final.
func (i *Incoming) ReconcileSavedContacts(ctx context.Context, instance string, r wameow.WAResolver) (ReconcileResult, error) {
	result := ReconcileResult{Instance: instance}
	if i.msgHistory == nil {
		return result, fmt.Errorf("retroactive name update disabled")
	}
	if r == nil {
		return result, fmt.Errorf("resolver nil")
	}
	saved, err := r.GetSavedContacts(ctx)
	if err != nil {
		return result, fmt.Errorf("get saved contacts: %w", err)
	}
	for jid, name := range saved {
		result.Scanned++
		// HandleContactUpdate puede no disparar trabajo si:
		// - el contacto no tiene msgs tracked Y no hay contact en Chatwoot
		// Pero cuenta como triggered porque metabaja el orchestrator.
		i.HandleContactUpdate(ctx, instance, jid, name, "", false, r)
		result.Triggered++
	}
	return result, nil
}
