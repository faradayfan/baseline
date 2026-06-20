package store_test

import (
	"testing"

	"github.com/faradayfan/baseline/internal/storetest"
)

// TestMain boots one shared pgvector container for this package's integration
// tests and tears it down afterward. Under -short no container is booted and
// integration tests self-skip, so unit tests still run Docker-free.
func TestMain(m *testing.M) {
	storetest.Main(m)
}
