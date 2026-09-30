package listproducts

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
)

var (
	ErrInvalidCatalogProduct  = errors.New("catalog_product_id must be a catalogue product id")
	ErrCatalogProductNotFound = errors.New("this product is not in the catalogue")
)

// Catalogue looks up catalogue products (productcatalog.Service).
type Catalogue interface {
	CanonicalProducts(ctx context.Context, ids []string) (map[string]productcatalog.CanonicalProduct, error)
}

// Fallbacks for a catalogue product missing a brand or category; the product
// tables require both.
const (
	unbranded     = "Unbranded"
	uncategorised = "Uncategorised"
)

type Service interface {
	List(ctx context.Context, userID, storeID uuid.UUID, filter ListFilter) ([]Listing, *StoreSummary, error)
	Catalog(ctx context.Context, userID, storeID uuid.UUID, query string) ([]CatalogItem, error)
	Add(ctx context.Context, userID, storeID uuid.UUID, req *AddRequest) (*Listing, error)
	Create(ctx context.Context, userID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error)
	Update(ctx context.Context, userID, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error)
	Remove(ctx context.Context, userID, storeID, variantID uuid.UUID) error
}

type service struct {
	repo      Repository
	access    storeaccess.Resolver
	catalogue Catalogue
}

func NewService(repo Repository, access storeaccess.Resolver, catalogue Catalogue) Service {
	return &service{repo: repo, access: access, catalogue: catalogue}
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

func (s *service) List(ctx context.Context, userID, storeID uuid.UUID, filter ListFilter) ([]Listing, *StoreSummary, error) {
	if _, err := s.authorize(ctx, userID, storeID, storeaccess.InventoryView); err != nil {
		return nil, nil, err
	}
	rows, err := s.repo.ListStoreProducts(ctx, storeID, filter)
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
	return s.repo.AddListing(ctx, userID, retailerID, storeID, req, available)
}

func (s *service) Create(ctx context.Context, userID, storeID uuid.UUID, req *CreateProductRequest) (*Listing, error) {
	retailerID, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogManage)
	if err != nil {
		return nil, err
	}
	source, err := s.catalogueProduct(ctx, req.CatalogProductID)
	if err != nil {
		return nil, err
	}
	if source == nil {
		if err := validateManual(req); err != nil {
			return nil, err
		}
	} else {
		fromCatalogue(req, source)
	}
	return s.repo.CreateAndList(ctx, userID, retailerID, storeID, req, source)
}

// catalogueProduct returns the catalogue product a create names, or nil for a
// product made by hand. The id must exist in the catalogue, so nobody can file
// their own product under another product's key and appear in its search results.
func (s *service) catalogueProduct(ctx context.Context, productID string) (*productcatalog.CanonicalProduct, error) {
	productID = strings.TrimSpace(productID)
	if productID == "" {
		return nil, nil
	}
	if !productcatalog.ValidProductID(productID) {
		return nil, ErrInvalidCatalogProduct
	}
	if s.catalogue == nil {
		return nil, productcatalog.ErrUnavailable
	}
	found, err := s.catalogue.CanonicalProducts(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	product, ok := found[productID]
	if !ok || product.Name == "" {
		return nil, ErrCatalogProductNotFound
	}
	return &product, nil
}

// fromCatalogue replaces the client's description of a catalogue product with
// the catalogue's, so the product reads the same at every retailer.
func fromCatalogue(req *CreateProductRequest, p *productcatalog.CanonicalProduct) {
	req.Name = p.Name
	req.Brand = cmp.Or(p.Brand, unbranded)
	req.Category = cmp.Or(p.Category, uncategorised)
	req.ImageURL = p.ImageURL
	req.SKU = p.SKU()
}

// Update needs pricing permission to change the price and stock permission
// for everything else, so a request changing both needs both.
func (s *service) Update(ctx context.Context, userID, storeID, variantID uuid.UUID, req *UpdateRequest) (*Listing, error) {
	if req.Price != nil {
		if _, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogManage); err != nil {
			return nil, err
		}
	}
	if req.Price == nil || req.LowStockThreshold != nil || req.IsAvailable != nil {
		if _, err := s.authorize(ctx, userID, storeID, storeaccess.InventoryUpdate); err != nil {
			return nil, err
		}
	}
	return s.repo.UpdateListing(ctx, storeID, variantID, req)
}

func (s *service) Remove(ctx context.Context, userID, storeID, variantID uuid.UUID) error {
	if _, err := s.authorize(ctx, userID, storeID, storeaccess.CatalogManage); err != nil {
		return err
	}
	return s.repo.RemoveListing(ctx, storeID, variantID)
}

// MapError handles listing errors and defers to the inventory module for
// store access and stock errors raised by the ledger.
func MapError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, inventory.ErrNotFound):
		return 404, "product listing not found"
	case errors.Is(err, ErrVariantNotFound):
		return 404, "product not found"
	case errors.Is(err, ErrSKUTaken):
		return 409, "you already have a product with this SKU"
	case errors.Is(err, ErrCatalogProductTaken):
		return 409, "you already have this catalogue product; add it from your products instead"
	case errors.Is(err, ErrInvalidCatalogProduct):
		return 400, err.Error()
	case errors.Is(err, ErrCatalogProductNotFound), errors.Is(err, ErrReservedSKU):
		return 422, err.Error()
	case errors.Is(err, ErrInvalidProduct):
		return 400, err.Error()
	case errors.Is(err, productcatalog.ErrUnavailable), errors.Is(err, productcatalog.ErrTimeout):
		return 503, "the product catalogue is unavailable right now; try again shortly"
	default:
		return inventory.MapError(err)
	}
}
