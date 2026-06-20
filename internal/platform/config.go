// Package platform holds cross-cutting concerns: configuration, logging, and
// (later) OTEL wiring. It has no dependencies on the domain packages.
package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MemorySourceKind selects which memory backend adapter Baseline uses. See spec §11.
type MemorySourceKind string

const (
	MemoryMem0 MemorySourceKind = "mem0"
	MemoryZep  MemorySourceKind = "zep"
	MemoryLetta MemorySourceKind = "letta"
	MemoryNone MemorySourceKind = "none" // standards-only mode (§11.2)
)

// Config is the fully-resolved runtime configuration, parsed from the environment
// (spec §16). Parse it once at startup and pass it down explicitly.
type Config struct {
	// HTTP
	Addr string // listen address, e.g. ":8080"

	// Database
	DatabaseURL string

	// Memory source (§11)
	MemorySource MemorySourceKind
	Mem0URL      string // required when MemorySource == mem0
	Mem0APIKey   string // optional bearer token (OSS server needs none)

	// Embeddings (§11.1) — EmbedderDims MUST equal the facts.embedding vector(N) dimension.
	EmbedderURL   string
	EmbedderModel string
	EmbedderDims  int

	// AuthN (§13) — at least one mechanism should be configured in production.
	OIDCIssuer   string
	OIDCAudience string
}

// Load reads configuration from the environment and validates it. It fails closed:
// an unknown memory source, a mem0 source without MEM0_URL, or a non-positive
// embedder dimension is a hard error rather than a silent default.
func Load() (Config, error) {
	c := Config{
		Addr:          envOr("BASELINE_ADDR", ":8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		MemorySource:  MemorySourceKind(strings.ToLower(envOr("MEMORY_SOURCE", string(MemoryMem0)))),
		Mem0URL:       os.Getenv("MEM0_URL"),
		Mem0APIKey:    os.Getenv("MEM0_API_KEY"),
		EmbedderURL:   os.Getenv("EMBEDDER_URL"),
		EmbedderModel: envOr("EMBEDDER_MODEL", "nomic-embed-text"),
		OIDCIssuer:    os.Getenv("OIDC_ISSUER"),
		OIDCAudience:  os.Getenv("OIDC_AUDIENCE"),
	}

	dims, err := strconv.Atoi(envOr("EMBEDDER_DIMS", "768"))
	if err != nil || dims <= 0 {
		return Config{}, fmt.Errorf("EMBEDDER_DIMS must be a positive integer, got %q", os.Getenv("EMBEDDER_DIMS"))
	}
	c.EmbedderDims = dims

	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	switch c.MemorySource {
	case MemoryMem0:
		if c.Mem0URL == "" {
			return Config{}, fmt.Errorf("MEM0_URL is required when MEMORY_SOURCE=mem0")
		}
	case MemoryZep, MemoryLetta, MemoryNone:
		// ok — zep/letta adapters land later; none is standards-only mode.
	default:
		return Config{}, fmt.Errorf("unknown MEMORY_SOURCE %q (want mem0|zep|letta|none)", c.MemorySource)
	}

	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
