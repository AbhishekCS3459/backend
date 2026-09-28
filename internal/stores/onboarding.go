package stores

import (
	"encoding/json"
	"fmt"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/progress"
	"github.com/AbhishekCS3459/find-me-backend/internal/identity/retailer"
)

// IncompleteError rejects a submission while a required step is unfinished.
type IncompleteError struct {
	Step string
}

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("finish %s before submitting this store", progress.StepLabel(e.Step))
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

// firstIncomplete returns the first unfinished step before "verify & submit".
func firstIncomplete(draft progress.Draft) string {
	draft.Submitted = false
	steps, _, _ := progress.Evaluate(draft)
	for _, step := range steps[:len(steps)-1] {
		if step.Status != progress.StatusCompleted {
			return step.Key
		}
	}
	return ""
}

// onboardingView is the full setup state. A COMPLETED store is final even if
// its stored draft is sparse (stores that predate per-store onboarding).
func onboardingView(store Store, draft progress.Draft) *progress.View {
	steps, overall, current := progress.Evaluate(draft)
	if store.OnboardingStatus == OnboardingCompleted {
		for i := range steps {
			steps[i].Status = progress.StatusCompleted
		}
		overall, current = progress.StatusCompleted, steps[len(steps)-1].Key
	}
	updated := store.UpdatedAt
	return &progress.View{Status: overall, CurrentStep: current, Steps: steps, Data: &draft, UpdatedAt: &updated}
}

func onboardingSummary(store Store) OnboardingSummary {
	view := onboardingView(store, decodeDraft(store))
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
