package stores

import (
	"context"
	"errors"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
	"github.com/google/uuid"
)

var ErrNoRetailer = errors.New("complete the retailer profile before managing stores")

type Service interface {
	ListMine(ctx context.Context, userID uuid.UUID) ([]Summary, error)
	Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Summary, error)
	GetOwned(ctx context.Context, userID, storeID uuid.UUID) (*Summary, error)
}

type service struct {
	repo      Repository
	retailers retailer.Repository
}

func NewService(repo Repository, retailers retailer.Repository) Service {
	return &service{repo: repo, retailers: retailers}
}

func (s *service) ListMine(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	profile, err := s.retailers.FindByUserID(ctx, userID)
	if err == retailer.ErrNotFound {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.repo.ListByRetailer(ctx, profile.ID)
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
	return s.repo.Create(ctx, store, req.Images)
}

func (s *service) GetOwned(ctx context.Context, userID, storeID uuid.UUID) (*Summary, error) {
	profile, err := s.retailers.FindByUserID(ctx, userID)
	if err == retailer.ErrNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	store, err := s.repo.FindOwned(ctx, storeID, profile.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListByRetailer(ctx, profile.ID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID == store.ID {
			return &rows[i], nil
		}
	}
	return nil, ErrNotFound
}
