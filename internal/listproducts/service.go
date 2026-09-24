package listproducts

import (
	"context"
	"errors"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
	"github.com/google/uuid"
)

type Service interface {
	List(ctx context.Context, userID, storeID uuid.UUID, query, status string) ([]Listing, *StoreSummary, error)
	Catalog(ctx context.Context, userID, storeID uuid.UUID, query string) ([]CatalogItem, error)
	Add(ctx context.Context, userID, storeID uuid.UUID, req *AddRequest) (*Listing, error)
	Create(ctx context.Context, userID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error)
	Update(ctx context.Context, userID, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error)
	BulkUpdate(ctx context.Context, userID, storeID uuid.UUID, req *BulkUpdateRequest) (int, error)
	Remove(ctx context.Context, userID, storeID, variantID uuid.UUID) error
}

type service struct {
	repo      Repository
	retailers retailer.Repository
}

func NewService(repo Repository, retailers retailer.Repository) Service {
	return &service{repo: repo, retailers: retailers}
}

func (s *service) retailerID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	profile, err := s.retailers.FindByUserID(ctx, userID)
	if err != nil {
		return uuid.Nil, err
	}
	return profile.ID, nil
}

func (s *service) assertOwned(ctx context.Context, userID, storeID uuid.UUID) (uuid.UUID, error) {
	retailerID, err := s.retailerID(ctx, userID)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.repo.AssertStoreOwned(ctx, storeID, retailerID); err != nil {
		return uuid.Nil, err
	}
	return retailerID, nil
}

func (s *service) List(ctx context.Context, userID, storeID uuid.UUID, query, status string) ([]Listing, *StoreSummary, error) {
	if _, err := s.assertOwned(ctx, userID, storeID); err != nil {
		return nil, nil, err
	}
	rows, err := s.repo.ListStoreProducts(ctx, storeID, query, status)
	if err != nil {
		return nil, nil, err
	}
	stats, err := s.repo.StoreStats(ctx, storeID)
	if err != nil {
		return nil, nil, err
	}
	return rows, stats, nil
}

func (s *service) Catalog(ctx context.Context, userID, storeID uuid.UUID, query string) ([]CatalogItem, error) {
	retailerID, err := s.assertOwned(ctx, userID, storeID)
	if err != nil {
		return nil, err
	}
	return s.repo.ListCatalog(ctx, retailerID, storeID, query)
}

func (s *service) Add(ctx context.Context, userID, storeID uuid.UUID, req *AddRequest) (*Listing, error) {
	if _, err := s.assertOwned(ctx, userID, storeID); err != nil {
		return nil, err
	}
	available := true
	if req.IsAvailable != nil {
		available = *req.IsAvailable
	}
	return s.repo.AddListing(ctx, storeID, req.VariantID, req.QuantityAvailable, req.LowStockThreshold, available)
}

func (s *service) Create(ctx context.Context, userID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error) {
	retailerID, err := s.assertOwned(ctx, userID, storeID)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateAndList(ctx, retailerID, storeID, req)
}

func (s *service) Update(ctx context.Context, userID, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error) {
	if _, err := s.assertOwned(ctx, userID, storeID); err != nil {
		return nil, err
	}
	return s.repo.UpdateListing(ctx, storeID, variantID, req)
}

func (s *service) BulkUpdate(ctx context.Context, userID, storeID uuid.UUID, req *BulkUpdateRequest) (int, error) {
	if _, err := s.assertOwned(ctx, userID, storeID); err != nil {
		return 0, err
	}
	return s.repo.BulkUpdateListings(ctx, storeID, req.Items)
}

func (s *service) Remove(ctx context.Context, userID, storeID, variantID uuid.UUID) error {
	if _, err := s.assertOwned(ctx, userID, storeID); err != nil {
		return err
	}
	return s.repo.RemoveListing(ctx, storeID, variantID)
}

func MapError(err error) (int, string) {
	switch {
	case errors.Is(err, retailer.ErrNotFound), errors.Is(err, ErrForbidden):
		return 404, "store not found"
	case errors.Is(err, ErrNotFound):
		return 404, "product listing not found"
	case errors.Is(err, ErrAlreadyListed):
		return 409, "product is already listed in this store"
	default:
		return 500, "request failed"
	}
}
