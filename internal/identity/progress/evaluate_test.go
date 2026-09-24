package progress

import "testing"

func TestEvaluateStartsOnRetailerProfile(t *testing.T) {
	steps, overall, current := Evaluate(Draft{})
	if overall != StatusDraft || current != "retailer_profile" {
		t.Fatalf("overall %s current %s", overall, current)
	}
	if steps[0].Status != StatusDraft || steps[1].Status != StatusDraft {
		t.Fatalf("expected every step to be draft, got %+v", steps)
	}
}

func TestEvaluateCompletesEarlierStepsOnly(t *testing.T) {
	draft := Draft{
		Retailer: Retailer{
			LegalName: "Topaz", OwnerName: "Shiv", IDProofURL: "https://id", BusinessRegURL: "https://reg",
			Payout: "bank", Holder: "Shiv", Number: "55530200321489", IFSC: "BARB1",
		},
	}
	steps, overall, current := Evaluate(draft)
	if steps[0].Status != StatusCompleted || steps[1].Status != StatusDraft {
		t.Fatalf("steps %+v", steps)
	}
	if overall != StatusDraft || current != "business_details" {
		t.Fatalf("overall %s current %s", overall, current)
	}
}

func TestEvaluateOverallCompletedOnlyAfterSubmit(t *testing.T) {
	draft := completeDraft()
	steps, overall, current := Evaluate(draft)
	if steps[5].Status != StatusCompleted || steps[6].Status != StatusDraft || overall != StatusDraft || current != "verify_submit" {
		t.Fatalf("before submit %+v %s %s", steps, overall, current)
	}
	draft.Submitted = true
	steps, overall, current = Evaluate(draft)
	if overall != StatusCompleted || current != "verify_submit" || steps[6].Status != StatusCompleted {
		t.Fatalf("after submit %+v %s %s", steps, overall, current)
	}
}

func completeDraft() Draft {
	return Draft{
		Name: "Airport kiosk", Categories: StringList{"Groceries"},
		Retail: []Channel{{Channel: "Own shop"}},
		Spoc:   Contact{Name: "Aman", Role: "Owner", Email: "aman@thegreenery.in", Phone: "6203390023"},
		GST:    "29ABCDE1234F1Z5", GSTVerified: true,
		Brand:    Brand{Name: "The Greenery", Manufacturer: "The Greenery", Logo: "logo.png"},
		Bank:     Bank{Holder: "Aman", Number: "123456789", IFSC: "HDFC0001234"},
		Shipping: Shipping{Address: "12 Market Road"},
		Retailer: Retailer{
			LegalName: "The Greenery", OwnerName: "Aman", IDProofURL: "https://id", BusinessRegURL: "https://reg",
			Payout: "qr", QRURL: "https://qr",
		},
	}
}
