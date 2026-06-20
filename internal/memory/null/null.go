// Package null is the no-op MemorySource: it returns empty results so Baseline
// runs in standards-only mode (§11.2) with no memory backend at all. That the
// system runs fully with the dependency removed is the coupling guarantee.
package null

import (
	"context"

	"github.com/faradayfan/baseline/internal/memory"
)

// Source is the null memory source. Selected via MEMORY_SOURCE=none.
type Source struct{}

func New() Source { return Source{} }

func (Source) List(context.Context, string, memory.ListOpts) ([]memory.Memory, error) {
	return nil, nil
}

func (Source) Search(context.Context, string, string, memory.SearchOpts) ([]memory.Memory, error) {
	return nil, nil
}

// Get returns ErrNotFound for any id — there are no memories in standards-only mode.
func (Source) Get(context.Context, string) (memory.Memory, error) {
	return memory.Memory{}, memory.ErrNotFound
}
