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

// untitledStore names a draft until the retailer types a name in setup.
const untitledStore = "Untitled store"

var (
	ErrNoRetailer         = errors.New("complete the retailer profile before managing stores")
	ErrNotOwner           = errors.New("only the store owner can change store details and setup")
	ErrOnboardingComplete = errors.New("this store's setup is already complete")
	ErrNameTooLong        = errors.New("store name must be at most 255 characters")
	ErrBankIncomplete     = errors.New("enter the account holder, account number and IFSC, or upload a payment QR")
	ErrLocationNotSet     = errors.New("this store's location isn't set yet")
	ErrLocationIncomplete = errors.New("enter the address and city, and pin the store's location")
)

// defaultServiceRadiusKm applies until the retailer chooses a service area.
const defaultServiceRadiusKm = 5

type Service interface {
	// ListMine returns stores the user owns or is active staff in.
	ListMine(ctx context.Context, userID uuid.UUID) ([]Summary, error)
	// Create starts a store in DRAFT; it only becomes COMPLETED through SaveOnboarding.
	Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Summary, error)
	Get(ctx context.Context, userID, storeID uuid.UUID) (*Summary, error)
	Update(ctx context.Context, userID, storeID uuid.UUID, req *UpdateRequest) (*Summary, error)
	// NewOnboarding is the setup a store starts with before it's created,
	// so steps already covered by the retailer profile show as completed.
	NewOnboarding(ctx context.Context, userID uuid.UUID) (*progress.View, error)
	Onboarding(ctx context.Context, userID, storeID uuid.UUID) (*progress.View, error)
	// SaveOnboarding stores the draft. With Submitted set it completes the store,
	// or returns *IncompleteError naming the first unfinished step.
	SaveOnboarding(ctx context.Context, userID, storeID uuid.UUID, draft progress.Draft) (*progress.View, error)
	// SaveBank sets where this store is paid; it can change after setup is complete.
	SaveBank(ctx context.Context, userID, storeID uuid.UUID, req *SaveBankRequest) (*progress.View, error)
	// Delete removes the store for its owner and staff. Orders, stock history
	// and payouts are kept for records.
	Delete(ctx context.Context, userID, storeID uuid.UUID) error
	// Location returns ErrLocationNotSet until the store saves one.
	Location(ctx context.Context, userID, storeID uuid.UUID) (*Location, error)
	// SaveLocation sets the store's address and map pin; search uses the pin.
	SaveLocation(ctx context.Context, userID, storeID uuid.UUID, req *SaveLocationRequest) (*Location, error)
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
	draft.Bank = progress.Bank{}
	applyLocation(&draft, nil)
	applyRetailer(&draft, profile)
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}

	storeName := name
	if utf8.RuneCountInString(storeName) < 2 {
		storeName = untitledStore
	}
	store := &Store{
		RetailerID:       profile.ID,
		CategoryID:       categoryID,
		Name:             storeName,
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

func (s *service) NewOnboarding(ctx context.Context, userID uuid.UUID) (*progress.View, error) {
	profile, err := s.retailerProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	var draft progress.Draft
	applyRetailer(&draft, profile)
	applyPayout(&draft, profile)
	steps, overall, current := progress.Evaluate(draft)
	return &progress.View{Status: overall, CurrentStep: current, Steps: steps, Data: &draft}, nil
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
	if err := s.applySaved(ctx, storeID, &draft); err != nil {
		return nil, err
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
	if err := s.applySaved(ctx, storeID, &draft); err != nil {
		return nil, err
	}

	submit := draft.Submitted
	draft.Submitted = false
	if submit {
		if incomplete := firstIncomplete(draft); incomplete != nil {
			return nil, incomplete
		}
		draft.Submitted = true
	}
	persisted := draft
	persisted.Bank = progress.Bank{}
	persisted.Shipping.Lat, persisted.Shipping.Lng = nil, nil
	payload, err := json.Marshal(persisted)
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

func (s *service) SaveBank(
	ctx context.Context, userID, storeID uuid.UUID, req *SaveBankRequest,
) (*progress.View, error) {
	if _, err := s.ownerAccess(ctx, userID, storeID); err != nil {
		return nil, err
	}
	bank := &Bank{StoreID: storeID, Method: req.Method}
	if req.Method == "qr" {
		bank.QRURL = strings.TrimSpace(req.QRURL)
	} else {
		bank.AccountHolderName = strings.TrimSpace(req.Holder)
		bank.AccountNumber = strings.TrimSpace(req.Number)
		bank.IFSC = strings.ToUpper(strings.TrimSpace(req.IFSC))
		bank.BankName = strings.TrimSpace(req.Bank)
	}
	if !progress.BankComplete(bank.draft()) {
		return nil, ErrBankIncomplete
	}
	if _, err := s.repo.SaveBank(ctx, bank); err != nil {
		return nil, err
	}
	return s.Onboarding(ctx, userID, storeID)
}

func (s *service) Delete(ctx context.Context, userID, storeID uuid.UUID) error {
	if _, err := s.ownerAccess(ctx, userID, storeID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, storeID)
}

func (s *service) Location(ctx context.Context, userID, storeID uuid.UUID) (*Location, error) {
	if err := s.require(ctx, userID, storeID, storeaccess.StoreView); err != nil {
		return nil, err
	}
	loc, err := s.repo.FindLocation(ctx, storeID)
	if err != nil {
		return nil, err
	}
	if loc == nil {
		return nil, ErrLocationNotSet
	}
	return loc, nil
}

func (s *service) SaveLocation(
	ctx context.Context, userID, storeID uuid.UUID, req *SaveLocationRequest,
) (*Location, error) {
	if err := s.require(ctx, userID, storeID, storeaccess.StoreManage); err != nil {
		return nil, err
	}
	loc := &Location{
		StoreID:             storeID,
		AddressLine:         strings.TrimSpace(req.AddressLine),
		City:                strings.TrimSpace(req.City),
		Pincode:             strings.TrimSpace(req.Pincode),
		Lat:                 *req.Lat,
		Lng:                 *req.Lng,
		ServiceAreaRadiusKm: req.ServiceAreaRadiusKm,
	}
	// 0,0 is what an untouched map picker sends; no store is there.
	if loc.AddressLine == "" || loc.City == "" || (loc.Lat == 0 && loc.Lng == 0) {
		return nil, ErrLocationIncomplete
	}
	if loc.ServiceAreaRadiusKm == 0 {
		loc.ServiceAreaRadiusKm = defaultServiceRadiusKm
		current, err := s.repo.FindLocation(ctx, storeID)
		if err != nil {
			return nil, err
		}
		if current != nil {
			loc.ServiceAreaRadiusKm = current.ServiceAreaRadiusKm
		}
	}
	return s.repo.SaveLocation(ctx, loc)
}

// applySaved shows what the store keeps outside its draft: the payout account
// and the location. Setup is judged on these, not on what the client sent.
func (s *service) applySaved(ctx context.Context, storeID uuid.UUID, draft *progress.Draft) error {
	bank, err := s.repo.FindBank(ctx, storeID)
	if err != nil {
		return err
	}
	loc, err := s.repo.FindLocation(ctx, storeID)
	if err != nil {
		return err
	}
	applyBank(draft, bank)
	applyLocation(draft, loc)
	return nil
}

// require checks a store permission, hiding stores the caller can't see.
func (s *service) require(ctx context.Context, userID, storeID uuid.UUID, permission storeaccess.Permission) error {
	_, err := s.access.Require(ctx, userID, storeID, permission)
	if errors.Is(err, storeaccess.ErrNotFound) {
		return ErrNotFound
	}
	return err
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
