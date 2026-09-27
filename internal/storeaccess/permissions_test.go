package storeaccess

import (
	"slices"
	"testing"
)

func TestNormalize_AddsDependenciesInCatalogueOrder(t *testing.T) {
	got, err := Normalize([]string{"inventory.update", "inventory.update"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Permission{CatalogView, InventoryView, InventoryUpdate}
	if !slices.Equal(got, want) {
		t.Fatalf("Normalize = %v, want %v", got, want)
	}
}

func TestNormalize_RejectsUnknownPermission(t *testing.T) {
	if _, err := Normalize([]string{"bank.manage"}); err == nil {
		t.Fatal("expected an error for an unknown permission")
	}
}

func TestPresets_AreClosedUnderDependencies(t *testing.T) {
	for role, perms := range presets {
		keys := make([]string, len(perms))
		for i, p := range perms {
			keys[i] = string(p)
		}
		normalized, err := Normalize(keys)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if len(normalized) != len(perms) {
			t.Fatalf("%s preset is missing dependencies: have %v, need %v", role, perms, normalized)
		}
	}
}

func TestStaffPreset_ExcludesAdminOnlyPermissions(t *testing.T) {
	staff := Preset(RoleStoreStaff)
	for _, p := range []Permission{TeamManage, StoreManage, CatalogManage, OrdersRefund} {
		if slices.Contains(staff, p) {
			t.Fatalf("store staff preset should not include %s", p)
		}
	}
	if !slices.Contains(Preset(RoleStoreAdmin), TeamManage) {
		t.Fatal("store admin preset should include team.manage")
	}
}

func TestAccess_Can(t *testing.T) {
	owner := accessRow{Role: string(RoleOwner)}.toAccess()
	if !owner.Can(TeamManage) {
		t.Fatal("owner should have every permission")
	}
	staff := accessRow{Role: string(RoleStoreStaff), Permissions: "inventory.view,unknown.key"}.toAccess()
	if !staff.Can(InventoryView) || staff.Can(InventoryUpdate) {
		t.Fatalf("staff permissions = %v", staff.Permissions)
	}
	if slices.Contains(staff.Permissions, Permission("unknown.key")) {
		t.Fatal("unknown stored permissions must be dropped")
	}
}
