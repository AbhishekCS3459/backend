package stores

import (
	"context"
	"errors"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
)

var ErrNoRetailer = errors.New("complete the retailer profile before managing stores")

type Service interface {
	// ListMine returns stores the user owns or is active staff in.
	ListMine(ctx context.Context, userID uuid.UUID) ([]Summary, error)
	Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Summary, error)
	Get(ctx context.Context, userID, storeID uuid.UUID) (*Summary, error)
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
	store := &Store{
		RetailerID:  profile.ID,
		CategoryID:  categoryID,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		IsOpen:      true,
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
