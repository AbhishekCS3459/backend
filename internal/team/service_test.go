package team

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
)

type fakeResolver struct{ stores []storeaccess.Access }

func (f fakeResolver) Store(context.Context, uuid.UUID, uuid.UUID) (*storeaccess.Access, error) {
	return nil, storeaccess.ErrNotFound
}

func (f fakeResolver) Stores(context.Context, uuid.UUID) ([]storeaccess.Access, error) {
	return f.stores, nil
}

func (f fakeResolver) Require(
	context.Context, uuid.UUID, uuid.UUID, storeaccess.Permission,
) (*storeaccess.Access, error) {
	return nil, storeaccess.ErrNotFound
}

type fakeRepo struct {
	account     *account
	memberships []membership
	members     []memberRow
	created     *newAccount
	saved       []assignment
	passwordSet bool
}

func (f *fakeRepo) ListMembers(context.Context, []uuid.UUID) ([]memberRow, error) {
	return f.members, nil
}
func (f *fakeRepo) FindAccount(context.Context, []string, string) (*account, error) {
	return f.account, nil
}
func (f *fakeRepo) Memberships(context.Context, uuid.UUID) ([]membership, error) {
	return f.memberships, nil
}
func (f *fakeRepo) CreateStaff(_ context.Context, acct newAccount, rows []assignment, _ uuid.UUID) error {
	f.created, f.saved = &acct, rows
	for _, row := range rows {
		f.members = append(f.members, row.toMemberRow(acct.ID))
	}
	return nil
}
func (f *fakeRepo) SaveAssignments(
	_ context.Context, userID uuid.UUID, _ []uuid.UUID, rows []assignment, _ uuid.UUID,
) error {
	f.saved = rows
	for _, row := range rows {
		f.members = append(f.members, row.toMemberRow(userID))
	}
	return nil
}
func (a assignment) toMemberRow(userID uuid.UUID) memberRow {
	return memberRow{UserID: userID, Role: string(a.Role), StoreID: a.StoreID, IsActive: a.Active}
}

func (f *fakeRepo) RemoveAssignments(context.Context, uuid.UUID, []uuid.UUID) error { return nil }
func (f *fakeRepo) UpdateName(context.Context, uuid.UUID, string) error             { return nil }
func (f *fakeRepo) SetTemporaryPassword(context.Context, uuid.UUID, string) error {
	f.passwordSet = true
	return nil
}

var (
	callerID  = uuid.New()
	retailer  = uuid.New()
	storeA    = uuid.New()
	storeB    = uuid.New()
	ownerA    = storeaccess.Access{StoreID: storeA, RetailerID: retailer, Role: storeaccess.RoleOwner}
	adminOfA  = staffAccess(storeA, storeaccess.RoleStoreAdmin)
	staffOfB  = staffAccess(storeB, storeaccess.RoleStoreStaff)
	validBody = func(role storeaccess.Role, stores ...uuid.UUID) *CreateMemberRequest {
		return &CreateMemberRequest{
			FullName: "Priya", Phone: "98765 43210", Role: role, StoreIDs: stores, TemporaryPassword: "temp-pass-1",
		}
	}
)

func staffAccess(store uuid.UUID, role storeaccess.Role) storeaccess.Access {
	return storeaccess.Access{StoreID: store, RetailerID: retailer, Role: role, Permissions: storeaccess.Preset(role)}
}

func TestCreate_OwnerAddsStaffWithPresetAndPhoneLogin(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, fakeResolver{stores: []storeaccess.Access{ownerA}})

	res, err := svc.Create(context.Background(), callerID, validBody(storeaccess.RoleStoreStaff, storeA, storeA))
	if err != nil {
		t.Fatal(err)
	}
	if repo.created == nil || repo.created.Phone != "+919876543210" ||
		repo.created.Email != "919876543210"+syntheticEmailDomain {
		t.Fatalf("created account = %+v", repo.created)
	}
	if len(repo.saved) != 1 || !slices.Equal(repo.saved[0].Permissions, storeaccess.Preset(storeaccess.RoleStoreStaff)) {
		t.Fatalf("assignments = %+v", repo.saved)
	}
	if res.ExistingAccount || res.Member.UserID != repo.created.ID {
		t.Fatalf("response = %+v", res)
	}
}

func TestCreate_StoreAdminCannotAssignAdmins(t *testing.T) {
	svc := NewService(&fakeRepo{}, fakeResolver{stores: []storeaccess.Access{adminOfA}})
	_, err := svc.Create(context.Background(), callerID, validBody(storeaccess.RoleStoreAdmin, storeA))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestCreate_StoreAdminCannotDelegateTeamManage(t *testing.T) {
	svc := NewService(&fakeRepo{}, fakeResolver{stores: []storeaccess.Access{adminOfA}})
	body := validBody(storeaccess.RoleStoreStaff, storeA)
	body.Permissions = []string{"inventory.update", "team.manage"}
	var validation *ValidationError
	if _, err := svc.Create(context.Background(), callerID, body); !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestCreate_RejectsStoresOutsideCallerTeamScope(t *testing.T) {
	svc := NewService(&fakeRepo{}, fakeResolver{stores: []storeaccess.Access{ownerA, staffOfB}})
	_, err := svc.Create(context.Background(), callerID, validBody(storeaccess.RoleStoreStaff, storeB))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestCreate_StaffOnlyCallerHasNoTeamAccess(t *testing.T) {
	svc := NewService(&fakeRepo{}, fakeResolver{stores: []storeaccess.Access{staffOfB}})
	if _, err := svc.List(context.Background(), callerID, nil); !errors.Is(err, ErrNoTeamAccess) {
		t.Fatalf("err = %v, want ErrNoTeamAccess", err)
	}
}

func TestCreate_ExistingNonStaffAccountIsRejected(t *testing.T) {
	repo := &fakeRepo{account: &account{ID: uuid.New(), UserType: "RETAILER"}}
	svc := NewService(repo, fakeResolver{stores: []storeaccess.Access{ownerA}})
	_, err := svc.Create(context.Background(), callerID, validBody(storeaccess.RoleStoreStaff, storeA))
	if !errors.Is(err, ErrAccountInUse) {
		t.Fatalf("err = %v, want ErrAccountInUse", err)
	}
}

func TestCreate_ExistingStaffAccountIsLinked(t *testing.T) {
	existing := uuid.New()
	repo := &fakeRepo{account: &account{ID: existing, UserType: "STAFF"}}
	svc := NewService(repo, fakeResolver{stores: []storeaccess.Access{ownerA}})
	res, err := svc.Create(context.Background(), callerID, validBody(storeaccess.RoleStoreStaff, storeA))
	if err != nil {
		t.Fatal(err)
	}
	if !res.ExistingAccount || repo.created != nil || res.Member.UserID != existing {
		t.Fatalf("response = %+v, created = %+v", res, repo.created)
	}
}

func TestUpdate_StoreAdminCannotEditAnotherAdmin(t *testing.T) {
	admin := membership{StoreID: storeA, RetailerID: retailer, Role: string(storeaccess.RoleStoreAdmin)}
	repo := &fakeRepo{memberships: []membership{admin}}
	svc := NewService(repo, fakeResolver{stores: []storeaccess.Access{adminOfA}})
	req := &UpdateMemberRequest{
		FullName: "Ravi", Role: storeaccess.RoleStoreStaff, StoreIDs: []uuid.UUID{storeA}, Active: true,
	}
	if _, err := svc.Update(context.Background(), callerID, uuid.New(), req); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestUpdate_CannotEditSelf(t *testing.T) {
	svc := NewService(&fakeRepo{}, fakeResolver{stores: []storeaccess.Access{ownerA}})
	req := &UpdateMemberRequest{FullName: "Me", Role: storeaccess.RoleStoreStaff, StoreIDs: []uuid.UUID{storeA}}
	if _, err := svc.Update(context.Background(), callerID, callerID, req); !errors.Is(err, ErrSelf) {
		t.Fatalf("err = %v, want ErrSelf", err)
	}
}

func TestResetPassword_RefusesMembersOfOtherBusinesses(t *testing.T) {
	repo := &fakeRepo{memberships: []membership{
		{StoreID: storeA, RetailerID: retailer, Role: string(storeaccess.RoleStoreStaff)},
		{StoreID: uuid.New(), RetailerID: uuid.New(), Role: string(storeaccess.RoleStoreStaff)},
	}}
	svc := NewService(repo, fakeResolver{stores: []storeaccess.Access{ownerA}})
	var validation *ValidationError
	if err := svc.ResetPassword(context.Background(), callerID, uuid.New(), "temp-pass-2"); !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if repo.passwordSet {
		t.Fatal("password must not change")
	}
}

func TestGroupMembers(t *testing.T) {
	user := uuid.New()
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []memberRow{
		{UserID: user, Email: "9198" + syntheticEmailDomain, MustChangePassword: true, StoreID: storeA, StoreName: "A",
			Role: "STORE_STAFF", Permissions: "store.view,catalog.view", IsActive: false, CreatedAt: early.Add(time.Hour)},
		{UserID: user, StoreID: storeB, StoreName: "B", Role: "STORE_ADMIN", IsActive: false, CreatedAt: early},
	}
	got := groupMembers(rows)
	if len(got) != 1 {
		t.Fatalf("members = %d, want 1", len(got))
	}
	m := got[0]
	if m.Email != "" || m.Role != storeaccess.RoleStoreAdmin || m.Status != StatusPaused ||
		len(m.Stores) != 2 || !m.AddedAt.Equal(early) {
		t.Fatalf("member = %+v", m)
	}
}
