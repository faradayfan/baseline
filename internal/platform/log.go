package platform

import (
	"log/slog"
	"os"
)

// NewLogger returns the process-wide structured logger. JSON to STDERR: this is
// the conventional stream for logs (stdout is reserved for program data), and it
// is REQUIRED for the MCP-over-stdio mode, where stdout must carry only JSON-RPC
// frames — any log line on stdout there corrupts the protocol stream.
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
