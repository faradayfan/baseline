package rbac_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/rbac"
	"github.com/faradayfan/baseline/internal/storetest"
)

// seedHierarchy creates org ◄ team ◄ project and returns their IDs.
func seedHierarchy(t *testing.T, db *pgxpool.Pool) (org, team, project uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	mustScan := func(sql string, args ...any) uuid.UUID {
		var id uuid.UUID
		if err := db.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	org = mustScan(`INSERT INTO namespaces (name, kind) VALUES ('org','org') RETURNING id`)
	team = mustScan(`INSERT INTO namespaces (name, kind, parent_id) VALUES ('team:eng','team',$1) RETURNING id`, org)
	project = mustScan(`INSERT INTO namespaces (name, kind, parent_id) VALUES ('project:x','project',$1) RETURNING id`, team)
	return
}

func TestResolve_ReadInheritsUpParentChain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	ctx := context.Background()

	org, team, project := seedHierarchy(t, db)
	repo := rbac.NewRepo(db)

	// Principal is a reviewer in the TEAM namespace only.
	if err := repo.Grant(ctx, "alice", team, rbac.RoleReviewer); err != nil {
		t.Fatalf("grant: %v", err)
	}

	ent, err := repo.Resolve(ctx, rbac.Principal{ID: "alice"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Read entitlement inherits UPWARD: team member can read team and org (§6).
	if !ent.CanRead(team) {
		t.Error("should read own namespace (team)")
	}
	if !ent.CanRead(org) {
		t.Error("team member should read ancestor org (upward inheritance)")
	}
	// But NOT downward: a team grant does not let you read a child project.
	if ent.CanRead(project) {
		t.Error("team member must NOT read descendant project")
	}

	// Write-ish actions do NOT inherit: alice is a reviewer in team, so she can
	// approve in team, but holds nothing in org and cannot approve there.
	if !ent.Can(team, rbac.ActionApproveReject) {
		t.Error("reviewer should approve in team")
	}
	if ent.Can(org, rbac.ActionApproveReject) {
		t.Error("reviewer in team must NOT approve in org (writes don't inherit)")
	}
	if ent.Can(team, rbac.ActionManage) {
		t.Error("reviewer cannot manage members")
	}
}

func TestResolve_MultipleRolesAndNamespaces(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	ctx := context.Background()

	org, team, _ := seedHierarchy(t, db)
	repo := rbac.NewRepo(db)

	// contributor in org, admin in team.
	if err := repo.Grant(ctx, "bob", org, rbac.RoleContributor); err != nil {
		t.Fatal(err)
	}
	if err := repo.Grant(ctx, "bob", team, rbac.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	ent, err := repo.Resolve(ctx, rbac.Principal{ID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if !ent.Can(org, rbac.ActionPropose) {
		t.Error("contributor proposes in org")
	}
	if ent.Can(org, rbac.ActionManage) {
		t.Error("contributor cannot manage in org")
	}
	if !ent.Can(team, rbac.ActionManage) {
		t.Error("admin manages in team")
	}
}

func TestResolve_PlatformAdminOverrides(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	ctx := context.Background()

	org, _, _ := seedHierarchy(t, db)
	repo := rbac.NewRepo(db)

	// No grants at all, but PlatformAdmin set.
	ent, err := repo.Resolve(ctx, rbac.Principal{ID: "root", PlatformAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ent.CanRead(org) || !ent.Can(org, rbac.ActionManage) {
		t.Error("platform admin overrides all checks")
	}
}

func TestGrant_InvalidRoleRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	ctx := context.Background()
	org, _, _ := seedHierarchy(t, db)
	repo := rbac.NewRepo(db)

	if err := repo.Grant(ctx, "x", org, rbac.Role("god")); err == nil {
		t.Error("invalid role should be rejected")
	}
}
