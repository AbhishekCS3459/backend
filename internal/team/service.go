package team

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/phone"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Accounts created without an email get "<digits>@staff.todayz.in" so the
// users.email NOT NULL/UNIQUE constraint holds; they sign in with their phone.
const syntheticEmailDomain = "@staff.todayz.in"

type Service interface {
	List(ctx context.Context, callerID uuid.UUID, storeID *uuid.UUID) ([]Member, error)
	Create(ctx context.Context, callerID uuid.UUID, req *CreateMemberRequest) (*CreateMemberResponse, error)
	Update(ctx context.Context, callerID, memberID uuid.UUID, req *UpdateMemberRequest) (*Member, error)
	ResetPassword(ctx context.Context, callerID, memberID uuid.UUID, temporaryPassword string) error
	Remove(ctx context.Context, callerID, memberID uuid.UUID) error
}

type service struct {
	repo   Repository
	access storeaccess.Resolver
}

func NewService(repo Repository, access storeaccess.Resolver) Service {
	return &service{repo: repo, access: access}
}

// scope is the set of stores whose team the caller may manage.
type scope map[uuid.UUID]storeaccess.Access

func (sc scope) ids() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(sc))
	for id := range sc {
		out = append(out, id)
	}
	return out
}

func (sc scope) retailers() map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, access := range sc {
		out[access.RetailerID] = true
	}
	return out
}

func (s *service) scope(ctx context.Context, callerID uuid.UUID) (scope, error) {
	stores, err := s.access.Stores(ctx, callerID)
	if err != nil {
		return nil, err
	}
	sc := scope{}
	for _, access := range stores {
		if access.Can(storeaccess.TeamManage) {
			sc[access.StoreID] = access
		}
	}
	if len(sc) == 0 {
		return nil, ErrNoTeamAccess
	}
	return sc, nil
}

func (s *service) List(ctx context.Context, callerID uuid.UUID, storeID *uuid.UUID) ([]Member, error) {
	sc, err := s.scope(ctx, callerID)
	if err != nil {
		return nil, err
	}
	ids := sc.ids()
	if storeID != nil {
		if _, ok := sc[*storeID]; !ok {
			return nil, ErrForbidden
		}
		ids = []uuid.UUID{*storeID}
	}
	rows, err := s.repo.ListMembers(ctx, ids)
	if err != nil {
		return nil, err
	}
	return groupMembers(rows), nil
}

func (s *service) Create(
	ctx context.Context, callerID uuid.UUID, req *CreateMemberRequest,
) (*CreateMemberResponse, error) {
	sc, err := s.scope(ctx, callerID)
	if err != nil {
		return nil, err
	}
	number, ok := phone.Normalize(req.Phone)
	if !ok {
		return nil, &ValidationError{Message: "enter a valid phone number"}
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	storeIDs := uniqueIDs(req.StoreIDs)
	perms, err := resolvePermissions(sc, req.Role, req.Permissions, storeIDs)
	if err != nil {
		return nil, err
	}
	rows := assignments(storeIDs, req.Role, perms, true)

	existing, err := s.repo.FindAccount(ctx, uniqueStrings(number, strings.TrimSpace(req.Phone)), email)
	if err != nil {
		return nil, err
	}
	var memberID uuid.UUID
	if existing != nil {
		if err := s.linkExisting(ctx, callerID, existing, storeIDs, rows); err != nil {
			return nil, err
		}
		memberID = existing.ID
	} else {
		memberID, err = s.createAccount(ctx, callerID, req, number, email, rows)
		if err != nil {
			return nil, err
		}
	}

	member, err := s.findMember(ctx, sc, memberID)
	if err != nil {
		return nil, err
	}
	return &CreateMemberResponse{Member: *member, ExistingAccount: existing != nil}, nil
}

func (s *service) linkExisting(
	ctx context.Context, callerID uuid.UUID, existing *account, storeIDs []uuid.UUID, rows []assignment,
) error {
	if existing.ID == callerID {
		return ErrSelf
	}
	if existing.UserType != "STAFF" {
		return ErrAccountInUse
	}
	current, err := s.repo.Memberships(ctx, existing.ID)
	if err != nil {
		return err
	}
	for _, m := range current {
		if slices.Contains(storeIDs, m.StoreID) {
			return ErrAlreadyMember
		}
	}
	return s.repo.SaveAssignments(ctx, existing.ID, storeIDs, rows, callerID)
}

func (s *service) createAccount(
	ctx context.Context, callerID uuid.UUID, req *CreateMemberRequest, number, email string, rows []assignment,
) (uuid.UUID, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.TemporaryPassword), bcrypt.DefaultCost)
	if err != nil {
		return uuid.Nil, fmt.Errorf("hash temporary password: %w", err)
	}
	if email == "" {
		email = strings.TrimPrefix(number, "+") + syntheticEmailDomain
	}
	acct := newAccount{
		ID:           uuid.New(),
		FullName:     strings.TrimSpace(req.FullName),
		Phone:        number,
		Email:        email,
		PasswordHash: string(hash),
	}
	if err := s.repo.CreateStaff(ctx, acct, rows, callerID); err != nil {
		return uuid.Nil, err
	}
	return acct.ID, nil
}

func (s *service) Update(ctx context.Context, callerID, memberID uuid.UUID, req *UpdateMemberRequest) (*Member, error) {
	sc, err := s.editableMember(ctx, callerID, memberID)
	if err != nil {
		return nil, err
	}
	storeIDs := uniqueIDs(req.StoreIDs)
	perms, err := resolvePermissions(sc, req.Role, req.Permissions, storeIDs)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpdateName(ctx, memberID, strings.TrimSpace(req.FullName)); err != nil {
		return nil, err
	}
	rows := assignments(storeIDs, req.Role, perms, req.Active)
	if err := s.repo.SaveAssignments(ctx, memberID, sc.ids(), rows, callerID); err != nil {
		return nil, err
	}
	return s.findMember(ctx, sc, memberID)
}

func (s *service) ResetPassword(ctx context.Context, callerID, memberID uuid.UUID, temporaryPassword string) error {
	sc, err := s.editableMember(ctx, callerID, memberID)
	if err != nil {
		return err
	}
	// A password is shared by every business the person works for, so only
	// reset it when all of their memberships belong to the caller's business.
	all, err := s.repo.Memberships(ctx, memberID)
	if err != nil {
		return err
	}
	retailers := sc.retailers()
	for _, m := range all {
		if !retailers[m.RetailerID] {
			return &ValidationError{
				Message: "this person also works with another business, so they need to reset their own password",
			}
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(temporaryPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash temporary password: %w", err)
	}
	return s.repo.SetTemporaryPassword(ctx, memberID, string(hash))
}

func (s *service) Remove(ctx context.Context, callerID, memberID uuid.UUID) error {
	sc, err := s.editableMember(ctx, callerID, memberID)
	if err != nil {
		return err
	}
	return s.repo.RemoveAssignments(ctx, memberID, sc.ids())
}

// editableMember returns the caller's scope after checking they may change
// memberID: the member must be in one of their stores, and only owners can
// change store admins.
func (s *service) editableMember(ctx context.Context, callerID, memberID uuid.UUID) (scope, error) {
	if memberID == callerID {
		return nil, ErrSelf
	}
	sc, err := s.scope(ctx, callerID)
	if err != nil {
		return nil, err
	}
	current, err := s.repo.Memberships(ctx, memberID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, m := range current {
		access, ok := sc[m.StoreID]
		if !ok {
			continue
		}
		found = true
		if storeaccess.Role(m.Role) == storeaccess.RoleStoreAdmin && !access.IsOwner() {
			return nil, ErrForbidden
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	return sc, nil
}

func (s *service) findMember(ctx context.Context, sc scope, memberID uuid.UUID) (*Member, error) {
	rows, err := s.repo.ListMembers(ctx, sc.ids())
	if err != nil {
		return nil, err
	}
	for _, member := range groupMembers(rows) {
		if member.UserID == memberID {
			return &member, nil
		}
	}
	return nil, ErrNotFound
}

// resolvePermissions validates the requested role/permissions against the
// stores being assigned and what the caller is allowed to delegate there.
func resolvePermissions(
	sc scope, role storeaccess.Role, keys []string, storeIDs []uuid.UUID,
) ([]storeaccess.Permission, error) {
	if !storeaccess.IsStaffRole(role) {
		return nil, &ValidationError{Message: "role must be STORE_ADMIN or STORE_STAFF"}
	}
	perms := storeaccess.Preset(role)
	if len(keys) > 0 {
		normalized, err := storeaccess.Normalize(keys)
		if err != nil {
			return nil, &ValidationError{Message: err.Error()}
		}
		perms = normalized
	}
	if len(perms) == 0 {
		return nil, &ValidationError{Message: "choose at least one permission"}
	}
	if role == storeaccess.RoleStoreStaff && slices.Contains(perms, storeaccess.TeamManage) {
		return nil, &ValidationError{Message: "only store admins can manage staff"}
	}

	var retailerID uuid.UUID
	for i, id := range storeIDs {
		access, ok := sc[id]
		if !ok {
			return nil, ErrForbidden
		}
		if i == 0 {
			retailerID = access.RetailerID
		} else if access.RetailerID != retailerID {
			return nil, &ValidationError{Message: "choose stores from a single business"}
		}
		if access.IsOwner() {
			continue
		}
		if role == storeaccess.RoleStoreAdmin {
			return nil, ErrForbidden
		}
		for _, p := range perms {
			if p == storeaccess.TeamManage || !access.Can(p) {
				return nil, ErrForbidden
			}
		}
	}
	return perms, nil
}

func assignments(
	storeIDs []uuid.UUID, role storeaccess.Role, perms []storeaccess.Permission, active bool,
) []assignment {
	out := make([]assignment, 0, len(storeIDs))
	for _, id := range storeIDs {
		out = append(out, assignment{StoreID: id, Role: role, Permissions: perms, Active: active})
	}
	return out
}

// groupMembers folds per-store rows into one Member per person.
func groupMembers(rows []memberRow) []Member {
	var order []uuid.UUID
	byUser := map[uuid.UUID]*Member{}
	anyActive := map[uuid.UUID]bool{}
	for _, row := range rows {
		member, ok := byUser[row.UserID]
		if !ok {
			email := row.Email
			if strings.HasSuffix(email, syntheticEmailDomain) {
				email = ""
			}
			perms := []storeaccess.Permission{}
			for _, key := range strings.Split(row.Permissions, ",") {
				if key != "" {
					perms = append(perms, storeaccess.Permission(key))
				}
			}
			member = &Member{
				UserID:      row.UserID,
				FullName:    row.FullName,
				Phone:       row.Phone,
				Email:       email,
				Role:        storeaccess.Role(row.Role),
				Permissions: perms,
				Stores:      []StoreRef{},
				Status:      StatusActive,
				AddedAt:     row.CreatedAt,
			}
			if row.MustChangePassword {
				member.Status = StatusPending
			}
			byUser[row.UserID] = member
			order = append(order, row.UserID)
		}
		if storeaccess.Role(row.Role) == storeaccess.RoleStoreAdmin {
			member.Role = storeaccess.RoleStoreAdmin
		}
		if row.CreatedAt.Before(member.AddedAt) {
			member.AddedAt = row.CreatedAt
		}
		anyActive[row.UserID] = anyActive[row.UserID] || row.IsActive
		member.Stores = append(member.Stores, StoreRef{ID: row.StoreID, Name: row.StoreName})
	}
	out := make([]Member, 0, len(order))
	for _, id := range order {
		member := byUser[id]
		if !anyActive[id] {
			member.Status = StatusPaused
		}
		out = append(out, *member)
	}
	return out
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func uniqueStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
