package wameow

import (
	"strings"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// noisyWarnPatterns son substrings de mensajes WARN que vienen del cliente
// whatsmeow upstream y que son benignos para nuestro caso de uso pero se
// emiten para cada chat durante history sync — generando cientos de líneas
// de ruido sin valor operativo.
//
// El filtro es per-substring y case-sensitive. Mantén la lista corta y
// específica: si necesitamos un pattern más sutil, mejor abrir un issue
// upstream que enmascarar más cosas aquí. v0.53.3.
var noisyWarnPatterns = []string{
	// whatsmeow intenta DELETE el media adjunto en el server tras
	// procesarlo localmente; falla con 400 cuando el media ya no
	// existe (TTL expirado, ya recolectado, etc). No es accionable.
	"Failed to delete history sync media from server",
}

// filteredWALog implementa waLog.Logger envolviendo una base y suprimiendo
// los WARN que matchean noisyWarnPatterns. El resto pasa sin modificación.
type filteredWALog struct {
	base waLog.Logger
}

func newFilteredWALog(base waLog.Logger) waLog.Logger {
	return &filteredWALog{base: base}
}

func (l *filteredWALog) Warnf(msg string, args ...interface{}) {
	for _, p := range noisyWarnPatterns {
		if strings.Contains(msg, p) {
			return
		}
	}
	l.base.Warnf(msg, args...)
}

func (l *filteredWALog) Errorf(msg string, args ...interface{}) { l.base.Errorf(msg, args...) }
func (l *filteredWALog) Infof(msg string, args ...interface{})  { l.base.Infof(msg, args...) }
func (l *filteredWALog) Debugf(msg string, args ...interface{}) { l.base.Debugf(msg, args...) }
func (l *filteredWALog) Sub(module string) waLog.Logger {
	return &filteredWALog{base: l.base.Sub(module)}
}
