package platform

import (
	"log/slog"
	"os"
)

// NewLogger returns the process-wide structured logger. JSON to stdout so it
// drops cleanly into the existing OTEL/log stack (spec §13).
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
