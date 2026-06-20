package namespaces_test

import (
	"testing"

	"github.com/google/uuid"
)

func mustUUID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("new uuid: %v", err)
	}
	return id
}
