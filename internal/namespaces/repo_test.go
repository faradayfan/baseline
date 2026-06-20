package namespaces_test

import (
	"context"
	"testing"

	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/storetest"
)

func TestRepo_CreateGetList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	repo := namespaces.NewRepo(db)
	ctx := context.Background()

	// Create with no policy -> default for kind applied.
	org, err := repo.Create(ctx, namespaces.Namespace{Name: "org", Kind: namespaces.KindOrg})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if org.ID.String() == "" {
		t.Fatal("expected generated id")
	}
	if org.Policy.RequiredApprovals != 2 {
		t.Errorf("org default approvals = %d, want 2", org.Policy.RequiredApprovals)
	}

	team, err := repo.Create(ctx, namespaces.Namespace{Name: "team:platform", Kind: namespaces.KindTeam, ParentID: &org.ID})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if team.Policy.AutoPromote == nil || team.Policy.AutoPromote.Engine != "simple/v1" {
		t.Errorf("team should default to simple/v1, got %+v", team.Policy.AutoPromote)
	}

	// Get round-trips policy + parent.
	got, err := repo.Get(ctx, team.ID)
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != org.ID {
		t.Errorf("parent_id = %v, want %v", got.ParentID, org.ID)
	}
	if got.Policy.AutoPromote == nil || got.Policy.AutoPromote.Engine != "simple/v1" {
		t.Errorf("policy did not round-trip: %+v", got.Policy)
	}

	// List returns both, ordered by name.
	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("list len = %d, want 2", len(all))
	}
	if all[0].Name != "org" || all[1].Name != "team:platform" {
		t.Errorf("list order = [%s, %s], want [org, team:platform]", all[0].Name, all[1].Name)
	}
}

func TestRepo_GetNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	repo := namespaces.NewRepo(db)

	_, err := repo.Create(context.Background(), namespaces.Namespace{Name: "org", Kind: namespaces.KindOrg})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Random uuid that doesn't exist.
	_, err = repo.Get(context.Background(), mustUUID(t))
	if err != namespaces.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
