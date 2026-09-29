package listproducts

import (
	"context"
	"slices"
	"testing"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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
			_, err := NewService(repo, tc.access).Update(context.Background(), uuid.New(), uuid.New(), uuid.New(), &tc.req)
			if tc.allowed {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, storeaccess.ErrForbidden)
			}
			assert.Equal(t, tc.allowed, repo.called)
		})
	}
}
