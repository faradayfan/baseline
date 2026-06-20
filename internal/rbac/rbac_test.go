package rbac

import "testing"

// TestPermissionMatrix asserts every cell of the §7.2 matrix, positively and
// negatively (conformance §14.10, unit-level half).
func TestPermissionMatrix(t *testing.T) {
	// allow[action] = set of roles that may perform it, per §7.2.
	allow := map[Action]map[Role]bool{
		ActionReadFacts: {
			RoleReader: true, RoleContributor: true, RoleReviewer: true, RoleAdmin: true,
		},
		ActionPropose: {
			RoleReader: false, RoleContributor: true, RoleReviewer: true, RoleAdmin: true,
		},
		ActionApproveReject: {
			RoleReader: false, RoleContributor: false, RoleReviewer: true, RoleAdmin: true,
		},
		ActionRevoke: {
			RoleReader: false, RoleContributor: false, RoleReviewer: true, RoleAdmin: true,
		},
		ActionManage: {
			RoleReader: false, RoleContributor: false, RoleReviewer: false, RoleAdmin: true,
		},
	}

	roles := []Role{RoleReader, RoleContributor, RoleReviewer, RoleAdmin}
	actions := []Action{ActionReadFacts, ActionPropose, ActionApproveReject, ActionRevoke, ActionManage}

	for _, a := range actions {
		for _, r := range roles {
			want := allow[a][r]
			if got := r.Can(a); got != want {
				t.Errorf("%s.Can(%s) = %v, want %v", r, a, got, want)
			}
		}
	}
}

func TestCan_UnknownRoleOrActionDenies(t *testing.T) {
	if Role("superuser").Can(ActionReadFacts) {
		t.Error("unknown role must be denied")
	}
	if RoleAdmin.Can(Action("delete_everything")) {
		t.Error("unknown action must be denied")
	}
}

func TestCanAny(t *testing.T) {
	if !CanAny([]Role{RoleReader, RoleReviewer}, ActionApproveReject) {
		t.Error("reviewer in the set should grant approve")
	}
	if CanAny([]Role{RoleReader, RoleContributor}, ActionManage) {
		t.Error("neither reader nor contributor can manage")
	}
	if CanAny(nil, ActionReadFacts) {
		t.Error("empty role set grants nothing")
	}
}

func TestValidRole(t *testing.T) {
	for _, r := range []Role{RoleReader, RoleContributor, RoleReviewer, RoleAdmin} {
		if !ValidRole(r) {
			t.Errorf("%s should be valid", r)
		}
	}
	if ValidRole("platform_admin") {
		t.Error("platform_admin is global, not a namespace role")
	}
	if ValidRole("") {
		t.Error("empty role invalid")
	}
}
