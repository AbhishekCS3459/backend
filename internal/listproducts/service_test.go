package listproducts

import (
	"context"
	"slices"
	"testing"

	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staff has exactly the given permissions in every store.
type staff []storeaccess.Permission

func (s staff) Store(_ context.Context, _, storeID uuid.UUID) (*storeaccess.Access, error) {
	return &storeaccess.Access{StoreID: storeID, Role: storeaccess.RoleStoreStaff, Permissions: s}, nil
}

func (s staff) Stores(context.Context, uuid.UUID) ([]storeaccess.Access, error) { return nil, nil }

func (s staff) Require(
	ctx context.Context, userID, storeID uuid.UUID, permission storeaccess.Permission,
) (*storeaccess.Access, error) {
	if !slices.Contains(s, permission) {
		return nil, storeaccess.ErrForbidden
	}
	return s.Store(ctx, userID, storeID)
}

// updateOnly records whether UpdateListing ran; no other method is called.
type updateOnly struct {
	Repository
	called bool
}

func (r *updateOnly) UpdateListing(context.Context, uuid.UUID, uuid.UUID, *UpdateRequest) (*Listing, error) {
	r.called = true
	return &Listing{}, nil
}

// createOnly records the catalogue key CreateAndList received.
type createOnly struct {
	Repository
	called     bool
	catalogKey *string
}

func (r *createOnly) CreateAndList(
	_ context.Context, _, _, _ uuid.UUID, _ *CreateProductRequest, catalogKey *string,
) (*Listing, error) {
	r.called, r.catalogKey = true, catalogKey
	return &Listing{}, nil
}

// catalogue holds the product ids that exist, or fails every lookup with err.
type catalogue struct {
	ids []string
	err error
}

func (c catalogue) ExistingProductIDs(_ context.Context, ids []string) (map[string]bool, error) {
	if c.err != nil {
		return nil, c.err
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[id] = slices.Contains(c.ids, id)
	}
	return found, nil
}

func TestCreatingFromTheCatalogueNeedsARealCatalogueProduct(t *testing.T) {
	known := catalogue{ids: []string{"776963"}}
	todayzKey := "todayz:776963"
	for _, tc := range []struct {
		name      string
		productID string
		catalogue Catalogue
		wantKey   *string
		wantErr   error
	}{
		{"a product made by hand has no key", "", known, nil, nil},
		{"a catalogue product gets its todayz key", " 776963 ", known, &todayzKey, nil},
		{"an id that isn't a catalogue id", "TDZ-776963", known, nil, ErrInvalidCatalogProduct},
		{"an id the catalogue doesn't have", "111", known, nil, ErrCatalogProductNotFound},
		{"the catalogue is down", "776963", catalogue{err: productcatalog.ErrUnavailable}, nil,
			productcatalog.ErrUnavailable},
		{"no catalogue configured", "776963", nil, nil, productcatalog.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &createOnly{}
			_, err := NewService(repo, staff{storeaccess.CatalogManage}, tc.catalogue).Create(
				context.Background(), uuid.New(), uuid.New(), &CreateProductRequest{CatalogProductID: tc.productID})
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.False(t, repo.called, "nothing is created")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantKey, repo.catalogKey)
		})
	}
}

func TestChangingAPriceNeedsPricingPermission(t *testing.T) {
	price, threshold := 30.0, 5
	stock := staff{storeaccess.InventoryUpdate}
	pricing := staff{storeaccess.CatalogManage}
	both := staff{storeaccess.InventoryUpdate, storeaccess.CatalogManage}

	for _, tc := range []struct {
		name    string
		access  staff
		req     UpdateRequest
		allowed bool
	}{
		{"stock staff change the threshold", stock, UpdateRequest{LowStockThreshold: &threshold}, true},
		{"stock staff can't change the price", stock, UpdateRequest{Price: &price}, false},
		{"pricing staff change the price", pricing, UpdateRequest{Price: &price}, true},
		{"pricing alone can't change the threshold", pricing,
			UpdateRequest{Price: &price, LowStockThreshold: &threshold}, false},
		{"both together", both, UpdateRequest{Price: &price, LowStockThreshold: &threshold}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &updateOnly{}
			_, err := NewService(repo, tc.access, nil).Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), &tc.req)
			if tc.allowed {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, storeaccess.ErrForbidden)
			}
			assert.Equal(t, tc.allowed, repo.called)
		})
	}
}
