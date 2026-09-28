package stores

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/progress"
	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestService creates a retailer with a complete profile and KYC against
// TEST_DATABASE_URL or DATABASE_URL, and removes everything it made afterwards.
func newTestService(t *testing.T) (svc Service, userID uuid.UUID) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL / TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := database.NewDB(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	db := conn.Gorm

	var hasColumn bool
	require.NoError(t, db.Raw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'store' AND column_name = 'onboarding_status')`).Scan(&hasColumn).Error)
	if !hasColumn {
		t.Skip("migration 000038 (store onboarding) is not applied")
	}

	userID, retailerID := uuid.New(), uuid.New()
	suffix := userID.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, db.Exec(sql, args...).Error)
	}
	exec(`INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Owner', 'RETAILER')`, userID, "+91997"+suffix, suffix+"@stores.test")
	exec(`INSERT INTO retailers (id, user_id, legal_name, owner_name) VALUES (?, ?, 'Test Retail Pvt Ltd', 'Test Owner')`,
		retailerID, userID)
	exec(`INSERT INTO retailer_kyc (retailer_id, id_proof_url, business_reg_url)
		VALUES (?, 'https://example.com/id.pdf', 'https://example.com/reg.pdf')`, retailerID)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM store_media WHERE store_id IN
			(SELECT id FROM store WHERE retailer_id = ?)`, retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM retailer_kyc WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM retailers WHERE id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})
	return NewService(NewRepository(db), retailer.NewRepository(db), storeaccess.NewResolver(db)), userID
}

func completeDraft(name string) progress.Draft {
	return progress.Draft{
		Name:        name,
		Categories:  progress.StringList{"Grocery"},
		Retail:      []progress.Channel{{Channel: "Own shop"}},
		Spoc:        progress.Contact{Name: "Asha", Role: "Manager", Email: "asha@example.com", Phone: "9876543210"},
		GST:         "29ABCDE1234F1Z5",
		GSTVerified: true,
		Brand:       progress.Brand{Name: "Fresh Co", Manufacturer: "Fresh Co", Logo: "https://example.com/logo.png"},
		Bank:        progress.Bank{Method: "bank", Holder: "Asha", Number: "12345678", IFSC: "HDFC0001234"},
		Shipping:    progress.Shipping{Address: "12 Market Road"},
	}
}

func TestEachStoreHasItsOwnOnboarding(t *testing.T) {
	svc, userID := newTestService(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, userID, &CreateRequest{
		Name:       "First Store",
		Onboarding: &progress.Draft{Retailer: progress.Retailer{LegalName: "Spoofed Name"}, Submitted: true},
	})
	require.NoError(t, err)
	assert.Equal(t, progress.StatusDraft, first.Onboarding.Status, "a new store always starts in draft")
	assert.Equal(t, "business_details", first.Onboarding.CurrentStep, "retailer step comes from the saved profile")
	assert.Equal(t, 1, first.Onboarding.CompletedSteps)
	assert.Equal(t, 7, first.Onboarding.TotalSteps)

	view, err := svc.Onboarding(ctx, userID, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "Test Retail Pvt Ltd", view.Data.Retailer.LegalName, "client-sent retailer data is ignored")
	assert.False(t, view.Data.Submitted)

	partial := completeDraft("First Store")
	partial.Shipping.Address = ""
	partial.Submitted = true
	_, err = svc.SaveOnboarding(ctx, userID, first.ID, partial)
	var incomplete *IncompleteError
	require.ErrorAs(t, err, &incomplete)
	assert.Equal(t, "shipping_location", incomplete.Step)

	ready := completeDraft("First Store Renamed")
	view, err = svc.SaveOnboarding(ctx, userID, first.ID, ready)
	require.NoError(t, err)
	assert.Equal(t, progress.StatusDraft, view.Status, "saving without submitting keeps the store in draft")
	assert.Equal(t, "verify_submit", view.CurrentStep)

	ready.Submitted = true
	view, err = svc.SaveOnboarding(ctx, userID, first.ID, ready)
	require.NoError(t, err)
	assert.Equal(t, progress.StatusCompleted, view.Status)

	_, err = svc.SaveOnboarding(ctx, userID, first.ID, ready)
	assert.ErrorIs(t, err, ErrOnboardingComplete, "a completed setup can't be reopened")

	second, err := svc.Create(ctx, userID, &CreateRequest{Name: "Second Store"})
	require.NoError(t, err, "finishing one store never blocks the next")
	assert.Equal(t, progress.StatusDraft, second.Onboarding.Status)

	mine, err := svc.ListMine(ctx, userID)
	require.NoError(t, err)
	byID := map[uuid.UUID]Summary{}
	for _, row := range mine {
		byID[row.ID] = row
	}
	require.Len(t, byID, 2)
	assert.Equal(t, "First Store Renamed", byID[first.ID].Name, "the store takes the name from its setup")
	assert.Equal(t, progress.StatusCompleted, byID[first.ID].Onboarding.Status)
	assert.Equal(t, 7, byID[first.ID].Onboarding.CompletedSteps)
	assert.NotNil(t, byID[first.ID].Onboarding.CompletedAt)
	assert.Equal(t, progress.StatusDraft, byID[second.ID].Onboarding.Status)
	assert.Nil(t, byID[second.ID].Onboarding.CompletedAt)
}

func TestUpdateStoreDetailsAndPhotos(t *testing.T) {
	svc, userID := newTestService(t)
	ctx := context.Background()

	store, err := svc.Create(ctx, userID, &CreateRequest{
		Name: "Corner Shop", Images: []string{"https://example.com/a.jpg"},
	})
	require.NoError(t, err)

	name, description := "  Corner Shop Two ", "Open late"
	images := []string{"https://example.com/b.jpg", "https://example.com/a.jpg"}
	updated, err := svc.Update(ctx, userID, store.ID, &UpdateRequest{
		Name: &name, Description: &description, Images: &images,
	})
	require.NoError(t, err)
	assert.Equal(t, "Corner Shop Two", updated.Name)
	assert.Equal(t, "Open late", updated.Description)
	assert.Equal(t, images, updated.Images, "photos are replaced in the given order")

	stranger := uuid.New()
	_, err = svc.Update(ctx, stranger, store.ID, &UpdateRequest{Name: &name})
	assert.ErrorIs(t, err, ErrNotFound, "other users can't see the store")
	_, err = svc.SaveOnboarding(ctx, stranger, store.ID, completeDraft("x"))
	assert.ErrorIs(t, err, ErrNotFound)
}
