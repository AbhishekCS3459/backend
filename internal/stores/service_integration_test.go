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

	var migrated bool
	require.NoError(t, db.Raw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'store' AND column_name = 'deleted_at')`).Scan(&migrated).Error)
	if !migrated {
		t.Skip("migrations 000038-000040 (store onboarding, bank, soft delete) are not applied")
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
	exec(`INSERT INTO bank_details (retailer_id, account_number, ifsc, account_holder_name, qr_url, updated_at)
		VALUES (?, '12345678', 'HDFC0001234', 'Test Owner', '', now())`, retailerID)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM store_media WHERE store_id IN
			(SELECT id FROM store WHERE retailer_id = ?)`, retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM bank_details WHERE retailer_id = ?`, retailerID).Error
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

func TestNewStoreOnboardingStartsFromRetailerProfile(t *testing.T) {
	svc, userID := newTestService(t)

	view, err := svc.NewOnboarding(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, progress.StatusDraft, view.Status)
	assert.Equal(t, "business_details", view.CurrentStep)
	assert.Equal(t, progress.StatusCompleted, view.Steps[0].Status, "a complete retailer profile finishes step one")
	assert.Equal(t, progress.StatusDraft, view.Steps[1].Status)
	assert.Equal(t, "Test Retail Pvt Ltd", view.Data.Retailer.LegalName)
	assert.Equal(t,
		progress.Bank{Method: "bank", Holder: "Test Owner", Number: "12345678", IFSC: "HDFC0001234"},
		view.Data.Bank, "payout details are shared by every store")
	assert.Nil(t, view.UpdatedAt, "nothing is saved until the store is created")

	unknown, err := svc.NewOnboarding(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "retailer_profile", unknown.CurrentStep, "without a profile, setup starts at step one")
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
	assert.Equal(t, "bank_details", incomplete.Step, "bank details sent with the draft don't count until saved")
	assert.Contains(t, err.Error(), "Enter the account holder.", "the error says what's missing")

	_, err = svc.SaveBank(ctx, userID, first.ID, &SaveBankRequest{
		Method: "bank", Holder: "Asha", Number: "12345678", IFSC: "hdfc0001234",
	})
	require.NoError(t, err)
	_, err = svc.SaveOnboarding(ctx, userID, first.ID, partial)
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

func TestEachStoreHasItsOwnBankAccount(t *testing.T) {
	svc, userID := newTestService(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, userID, &CreateRequest{Name: "First Store"})
	require.NoError(t, err)
	second, err := svc.Create(ctx, userID, &CreateRequest{Name: "Second Store"})
	require.NoError(t, err)

	view, err := svc.Onboarding(ctx, userID, second.ID)
	require.NoError(t, err)
	assert.Equal(t, progress.Bank{}, view.Data.Bank, "a created store has no account until one is saved for it")

	_, err = svc.SaveBank(ctx, userID, first.ID, &SaveBankRequest{Method: "bank", Holder: "Asha", Number: "123"})
	require.ErrorIs(t, err, ErrBankIncomplete)

	view, err = svc.SaveBank(ctx, userID, first.ID, &SaveBankRequest{
		Method: "bank", Holder: " Asha ", Number: "11112222", IFSC: "hdfc0001234", QRURL: "https://example.com/ignored.png",
	})
	require.NoError(t, err)
	assert.Equal(t,
		progress.Bank{Method: "bank", Holder: "Asha", Number: "11112222", IFSC: "HDFC0001234"},
		view.Data.Bank, "details are trimmed and only the chosen method is kept")

	_, err = svc.SaveBank(ctx, userID, second.ID, &SaveBankRequest{Method: "qr", QRURL: "https://example.com/qr.png"})
	require.NoError(t, err)

	firstView, err := svc.Onboarding(ctx, userID, first.ID)
	require.NoError(t, err)
	secondView, err := svc.Onboarding(ctx, userID, second.ID)
	require.NoError(t, err)
	assert.Equal(t, "11112222", firstView.Data.Bank.Number, "saving one store's account leaves the others alone")
	assert.Equal(t, progress.Bank{Method: "qr", QRURL: "https://example.com/qr.png"}, secondView.Data.Bank)

	fresh, err := svc.NewOnboarding(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "12345678", fresh.Data.Bank.Number, "the retailer's own account is untouched")

	// Stale bank details in a saved draft never override the store's account.
	stale := completeDraft("First Store")
	stale.Bank = progress.Bank{Method: "bank", Holder: "Old", Number: "99999999", IFSC: "OLD00001"}
	_, err = svc.SaveOnboarding(ctx, userID, first.ID, stale)
	require.NoError(t, err)
	firstView, err = svc.Onboarding(ctx, userID, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "11112222", firstView.Data.Bank.Number)

	stranger := uuid.New()
	_, err = svc.SaveBank(ctx, stranger, first.ID, &SaveBankRequest{Method: "qr", QRURL: "https://example.com/x.png"})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDraftStoreCanStartWithoutAName(t *testing.T) {
	svc, userID := newTestService(t)
	ctx := context.Background()

	store, err := svc.Create(ctx, userID, &CreateRequest{
		Onboarding: &progress.Draft{Categories: progress.StringList{"Grocery"}},
	})
	require.NoError(t, err)
	assert.Equal(t, untitledStore, store.Name)

	view, err := svc.Onboarding(ctx, userID, store.ID)
	require.NoError(t, err)
	assert.Empty(t, view.Data.Name, "the name field stays blank for the retailer to fill")
	assert.Equal(t, progress.StringList{"Grocery"}, view.Data.Categories,
		"details typed before the store existed are kept")
	assert.Contains(t, view.Steps[1].Missing, "Enter the store name.")

	draft := *view.Data
	draft.Name = "Named Later"
	_, err = svc.SaveOnboarding(ctx, userID, store.ID, draft)
	require.NoError(t, err)
	got, err := svc.Get(ctx, userID, store.ID)
	require.NoError(t, err)
	assert.Equal(t, "Named Later", got.Name)
}

func TestDeleteStoreHidesItButKeepsHistory(t *testing.T) {
	svc, userID := newTestService(t)
	ctx := context.Background()

	keep, err := svc.Create(ctx, userID, &CreateRequest{Name: "Keep Me"})
	require.NoError(t, err)
	gone, err := svc.Create(ctx, userID, &CreateRequest{Name: "Delete Me"})
	require.NoError(t, err)

	require.ErrorIs(t, svc.Delete(ctx, uuid.New(), gone.ID), ErrNotFound, "strangers can't delete a store")
	require.NoError(t, svc.Delete(ctx, userID, gone.ID))
	require.ErrorIs(t, svc.Delete(ctx, userID, gone.ID), ErrNotFound, "deleting twice reports not found")

	mine, err := svc.ListMine(ctx, userID)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, keep.ID, mine[0].ID)

	_, err = svc.Get(ctx, userID, gone.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.Onboarding(ctx, userID, gone.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.SaveBank(ctx, userID, gone.ID, &SaveBankRequest{Method: "qr", QRURL: "https://example.com/qr.png"})
	assert.ErrorIs(t, err, ErrNotFound, "a deleted store can't be changed")

	repo := svc.(*service).repo.(*repository)
	var row struct {
		Status    string
		IsOpen    bool
		DeletedAt *time.Time
	}
	require.NoError(t, repo.db.Raw(`SELECT status, is_open, deleted_at FROM store WHERE id = ?`, gone.ID).Scan(&row).Error)
	assert.Equal(t, "INACTIVE", row.Status)
	assert.False(t, row.IsOpen)
	assert.NotNil(t, row.DeletedAt, "the row is kept for order and stock history")
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
