package stores

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/progress"
	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
)

// IncompleteError rejects a submission, or an edit to a live store, while a
// required step is unfinished.
type IncompleteError struct {
	Step    string
	Missing []string
	Live    bool
}

func (e *IncompleteError) Error() string {
	action := "submitting"
	if e.Live {
		action = "saving"
	}
	msg := fmt.Sprintf("finish %s before %s this store", progress.StepLabel(e.Step), action)
	if len(e.Missing) > 0 {
		msg += ": " + strings.Join(e.Missing, " ")
	}
	return msg
}

func decodeDraft(store Store) progress.Draft {
	var draft progress.Draft
	if len(store.OnboardingData) > 0 {
		// A malformed payload is treated as an empty draft rather than hiding the store.
		_ = json.Unmarshal(store.OnboardingData, &draft)
	}
	return draft
}

// applyRetailer overwrites the draft's retailer section with the saved profile:
// the retailer profile is shared by every store and is the source of truth.
func applyRetailer(draft *progress.Draft, profile *retailer.Profile) {
	draft.Retailer.LegalName, draft.Retailer.OwnerName = "", ""
	draft.Retailer.IDProofURL, draft.Retailer.BusinessRegURL = "", ""
	if profile == nil {
		return
	}
	draft.Retailer.LegalName = profile.LegalName
	draft.Retailer.OwnerName = profile.OwnerName
	if profile.KYC != nil {
		draft.Retailer.IDProofURL = profile.KYC.IDProofURL
		draft.Retailer.BusinessRegURL = profile.KYC.BusinessRegURL
	}
}

// applyBank shows the store's saved payout account. It lives in
// store_bank_details, never in the onboarding payload, so the two can't drift.
func applyBank(draft *progress.Draft, bank *Bank) {
	draft.Bank = bank.draft()
}

// applyLocation shows the store's saved address and pin. The pin lives only in
// store_location, so the shipping step completes once a location is saved; until
// then the address typed so far stays in the draft for the form to restore.
func applyLocation(draft *progress.Draft, loc *Location) {
	if loc == nil {
		draft.Shipping.Lat, draft.Shipping.Lng = nil, nil
		return
	}
	draft.Shipping = loc.draft(draft.Shipping.Label)
}

// applyPayout suggests the retailer's own account for a store that doesn't
// exist yet; it only becomes the store's account once saved through SaveBank.
func applyPayout(draft *progress.Draft, profile *retailer.Profile) {
	if profile == nil || profile.Bank == nil {
		return
	}
	bank := profile.Bank
	if bank.QRURL != "" {
		draft.Bank = progress.Bank{Method: "qr", QRURL: bank.QRURL}
		return
	}
	draft.Bank = progress.Bank{Method: "bank", Holder: bank.AccountHolderName, Number: bank.AccountNumber, IFSC: bank.IFSC}
}

// firstIncomplete returns the first unfinished step before "verify & submit", or nil.
func firstIncomplete(draft progress.Draft) *IncompleteError {
	draft.Submitted = false
	steps, _, _ := progress.Evaluate(draft)
	for _, step := range steps[:len(steps)-1] {
		if step.Status != progress.StatusCompleted {
			return &IncompleteError{Step: step.Key, Missing: step.Missing}
		}
	}
	return nil
}

// onboardingView is the full setup state. A COMPLETED store is final even if
// its stored draft is sparse (stores that predate per-store onboarding).
func onboardingView(store Store, draft progress.Draft) *progress.View {
	steps, overall, current := progress.Evaluate(draft)
	if store.OnboardingStatus == OnboardingCompleted {
		for i := range steps {
			steps[i].Status, steps[i].Missing = progress.StatusCompleted, nil
		}
		overall, current = progress.StatusCompleted, steps[len(steps)-1].Key
	}
	updated := store.UpdatedAt
	return &progress.View{Status: overall, CurrentStep: current, Steps: steps, Data: &draft, UpdatedAt: &updated}
}

func onboardingSummary(store Store, bank *Bank, loc *Location) OnboardingSummary {
	draft := decodeDraft(store)
	applyBank(&draft, bank)
	applyLocation(&draft, loc)
	view := onboardingView(store, draft)
	done := 0
	for _, step := range view.Steps {
		if step.Status == progress.StatusCompleted {
			done++
		}
	}
	return OnboardingSummary{
		Status:         view.Status,
		CurrentStep:    view.CurrentStep,
		CompletedSteps: done,
		TotalSteps:     len(view.Steps),
		CompletedAt:    store.OnboardingCompletedAt,
	}
}
