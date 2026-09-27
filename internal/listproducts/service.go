package listproducts

import (
	"context"
	"errors"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
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
	repo   Repository
	access storeaccess.Resolver
}

func NewService(repo Repository, access storeaccess.Resolver) Service {
	return &service{repo: repo, access: access}
}

// authorize checks the caller's permission in the store and returns the
// retailer that owns it (products belong to the retailer, not the store).
func (s *service) authorize(
	ctx context.Context, userID, storeID uuid.UUID, permission storeaccess.Permission,
) (uuid.UUID, error) {
	access, err := s.access.Require(ctx, userID, storeID, permission)
	if err != nil {
		return uuid.Nil, err
	}
	return access.RetailerID, nil
}

func (s *service) List(ctx context.Context, userID, storeID uuid.UUID, query, status string) ([]Listing, *StoreSummary, error) {
	if _, err := s.authorize(ctx, userID, storeID, storeaccess.InventoryView); err != nil {
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
	retailerID, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogView)
	if err != nil {
		return nil, err
	}
	return s.repo.ListCatalog(ctx, retailerID, storeID, query)
}

func (s *service) Add(ctx context.Context, userID, storeID uuid.UUID, req *AddRequest) (*Listing, error) {
	retailerID, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogManage)
	if err != nil {
		return nil, err
	}
	available := true
	if req.IsAvailable != nil {
		available = *req.IsAvailable
	}
	return s.repo.AddListing(ctx, retailerID, storeID, req, available)
}

func (s *service) Create(ctx context.Context, userID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error) {
	retailerID, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogManage)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateAndList(ctx, retailerID, storeID, req)
}

func (s *service) Update(ctx context.Context, userID, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error) {
	if _, err := s.authorize(ctx, userID, storeID, storeaccess.InventoryUpdate); err != nil {
		return nil, err
	}
	return s.repo.UpdateListing(ctx, storeID, variantID, req)
}

func (s *service) BulkUpdate(ctx context.Context, userID, storeID uuid.UUID, req *BulkUpdateRequest) (int, error) {
	if _, err := s.authorize(ctx, userID, storeID, storeaccess.InventoryUpdate); err != nil {
		return 0, err
	}
	return s.repo.BulkUpdateListings(ctx, storeID, req.Items)
}

func (s *service) Remove(ctx context.Context, userID, storeID, variantID uuid.UUID) error {
	if _, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogManage); err != nil {
		return err
	}
	return s.repo.RemoveListing(ctx, storeID, variantID)
}

func MapError(err error) (int, string) {
	switch {
	case errors.Is(err, storeaccess.ErrNotFound):
		return 404, "store not found"
	case errors.Is(err, storeaccess.ErrForbidden):
		return 403, err.Error()
	case errors.Is(err, ErrNotFound):
		return 404, "product listing not found"
	case errors.Is(err, ErrVariantNotFound):
		return 404, "product not found"
	case errors.Is(err, ErrAlreadyListed):
		return 409, "product is already listed in this store"
	case errors.Is(err, ErrSKUTaken):
		return 409, "you already have a product with this SKU"
	default:
		return 500, "request failed"
	}
}
