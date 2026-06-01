// Package lib utilidades transversales.
package lib

import (
	"log/slog"
	"os"
)

// NewLogger devuelve un *slog.Logger en formato JSON sobre stdout con el
// nivel mínimo indicado. Acepta "debug", "info", "warn", "error";
// cualquier otro valor se trata como "info".
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
