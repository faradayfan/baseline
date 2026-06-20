package namespaces_test

import (
	"testing"

	"github.com/faradayfan/baseline/internal/storetest"
)

// TestMain boots one shared pgvector container for this package's integration
// tests. Under -short no container is booted and integration tests self-skip.
func TestMain(m *testing.M) {
	storetest.Main(m)
}
