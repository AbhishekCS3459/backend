package stores

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/progress"
	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
)

var (
	ErrNoRetailer         = errors.New("complete the retailer profile before managing stores")
	ErrNotOwner           = errors.New("only the store owner can change store details and setup")
	ErrOnboardingComplete = errors.New("this store's setup is already complete")
	ErrNameTooLong        = errors.New("store name must be at most 255 characters")
)

type Service interface {
	// ListMine returns stores the user owns or is active staff in.
	ListMine(ctx context.Context, userID uuid.UUID) ([]Summary, error)
	// Create starts a store in DRAFT; it only becomes COMPLETED through SaveOnboarding.
	Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Summary, error)
	Get(ctx context.Context, userID, storeID uuid.UUID) (*Summary, error)
	Update(ctx context.Context, userID, storeID uuid.UUID, req *UpdateRequest) (*Summary, error)
	Onboarding(ctx context.Context, userID, storeID uuid.UUID) (*progress.View, error)
	// SaveOnboarding stores the draft. With Submitted set it completes the store,
	// or returns *IncompleteError naming the first unfinished step.
	SaveOnboarding(ctx context.Context, userID, storeID uuid.UUID, draft progress.Draft) (*progress.View, error)
}

type service struct {
	repo      Repository
	retailers retailer.Repository
	access    storeaccess.Resolver
}

func NewService(repo Repository, retailers retailer.Repository, access storeaccess.Resolver) Service {
	return &service{repo: repo, retailers: retailers, access: access}
}

func (s *service) ListMine(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	accesses, err := s.access.Stores(ctx, userID)
	if err != nil {
		return nil, err
	}
	byStore := make(map[uuid.UUID]*storeaccess.Access, len(accesses))
	ids := make([]uuid.UUID, 0, len(accesses))
	for i := range accesses {
		if _, seen := byStore[accesses[i].StoreID]; seen {
			continue
		}
		byStore[accesses[i].StoreID] = &accesses[i]
		ids = append(ids, accesses[i].StoreID)
	}
	rows, err := s.repo.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Access = byStore[rows[i].ID]
	}
	return rows, nil
}

func (s *service) Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Summary, error) {
	profile, err := s.retailers.FindByUserID(ctx, userID)
	if err == retailer.ErrNotFound {
		return nil, ErrNoRetailer
	}
	if err != nil {
		return nil, err
	}
	categoryID, err := s.repo.DefaultCategoryID(ctx)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	var draft progress.Draft
	if req.Onboarding != nil {
		draft = *req.Onboarding
	}
	draft.Name = name
	draft.Submitted = false
	applyRetailer(&draft, profile)
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}

	store := &Store{
		RetailerID:       profile.ID,
		CategoryID:       categoryID,
		Name:             name,
		Description:      strings.TrimSpace(req.Description),
		IsOpen:           true,
		OnboardingStatus: OnboardingDraft,
		OnboardingData:   payload,
	}
	summary, err := s.repo.Create(ctx, store, req.Images)
	if err != nil {
		return nil, err
	}
	summary.Access = &storeaccess.Access{
		StoreID:     summary.ID,
		RetailerID:  profile.ID,
		Role:        storeaccess.RoleOwner,
		Permissions: storeaccess.Preset(storeaccess.RoleStoreAdmin),
	}
	return summary, nil
}

func (s *service) Get(ctx context.Context, userID, storeID uuid.UUID) (*Summary, error) {
	access, err := s.access.Store(ctx, userID, storeID)
	if errors.Is(err, storeaccess.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListByIDs(ctx, []uuid.UUID{storeID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	rows[0].Access = access
	return &rows[0], nil
}

func (s *service) Update(ctx context.Context, userID, storeID uuid.UUID, req *UpdateRequest) (*Summary, error) {
	access, err := s.ownerAccess(ctx, userID, storeID)
	if err != nil {
		return nil, err
	}
	var name, description *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		name = &trimmed
	}
	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		description = &trimmed
	}
	summary, err := s.repo.Update(ctx, storeID, name, description, req.Images)
	if err != nil {
		return nil, err
	}
	summary.Access = access
	return summary, nil
}

func (s *service) Onboarding(ctx context.Context, userID, storeID uuid.UUID) (*progress.View, error) {
	if _, err := s.ownerAccess(ctx, userID, storeID); err != nil {
		return nil, err
	}
	store, err := s.repo.FindByID(ctx, storeID)
	if err != nil {
		return nil, err
	}
	draft := decodeDraft(*store)
	if store.OnboardingStatus != OnboardingCompleted {
		profile, err := s.retailerProfile(ctx, userID)
		if err != nil {
			return nil, err
		}
		applyRetailer(&draft, profile)
	}
	return onboardingView(*store, draft), nil
}

func (s *service) SaveOnboarding(
	ctx context.Context, userID, storeID uuid.UUID, draft progress.Draft,
) (*progress.View, error) {
	if _, err := s.ownerAccess(ctx, userID, storeID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(draft.Name)
	if utf8.RuneCountInString(name) > 255 {
		return nil, ErrNameTooLong
	}
	draft.Name = name

	profile, err := s.retailerProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	applyRetailer(&draft, profile)

	submit := draft.Submitted
	draft.Submitted = false
	if submit {
		if step := firstIncomplete(draft); step != "" {
			return nil, &IncompleteError{Step: step}
		}
		draft.Submitted = true
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}

	store, err := s.repo.UpdateLocked(ctx, storeID, func(store *Store) error {
		if store.OnboardingStatus == OnboardingCompleted {
			return ErrOnboardingComplete
		}
		// The store keeps its last usable name while the field is being edited.
		if utf8.RuneCountInString(name) >= 2 {
			store.Name = name
		}
		store.OnboardingData = payload
		if submit {
			now := time.Now().UTC()
			store.OnboardingStatus = OnboardingCompleted
			store.OnboardingCompletedAt = &now
		}
		return nil
	}, "name", "onboarding", "onboarding_status", "onboarding_completed_at")
	if err != nil {
		return nil, err
	}
	return onboardingView(*store, draft), nil
}

// ownerAccess allows only the retailer who owns the store; staff can't change setup.
func (s *service) ownerAccess(ctx context.Context, userID, storeID uuid.UUID) (*storeaccess.Access, error) {
	access, err := s.access.Store(ctx, userID, storeID)
	if errors.Is(err, storeaccess.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !access.IsOwner() {
		return nil, ErrNotOwner
	}
	return access, nil
}

func (s *service) retailerProfile(ctx context.Context, userID uuid.UUID) (*retailer.Profile, error) {
	profile, err := s.retailers.FindByUserID(ctx, userID)
	if errors.Is(err, retailer.ErrNotFound) {
		return nil, nil
	}
	return profile, err
}
