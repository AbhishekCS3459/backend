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

func TestEvaluateJudgesEachStepOnItsOwn(t *testing.T) {
	draft := completeDraft()
	draft.Spoc.Phone = ""
	steps, _, _ := Evaluate(draft)
	if steps[1].Status != StatusDraft || len(steps[1].Missing) != 1 || steps[1].Missing[0] != "Enter the contact phone." {
		t.Fatalf("business step %+v", steps[1])
	}
	for _, i := range []int{2, 3, 4, 5} {
		if steps[i].Status != StatusCompleted || steps[i].Missing != nil {
			t.Fatalf("step %s should stay completed, got %+v", steps[i].Key, steps[i])
		}
	}
	draft.Submitted = true
	steps, overall, current := Evaluate(draft)
	if steps[6].Status != StatusDraft || overall != StatusDraft || current != "business_details" {
		t.Fatalf("submit needs every step: %+v %s %s", steps[6], overall, current)
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

func TestShippingNeedsAFullAddressAndASavedPin(t *testing.T) {
	draft := completeDraft()
	draft.Shipping = Shipping{Address: "12 Market Road", City: "Bengaluru", Pin: "5600"}
	steps, _, _ := Evaluate(draft)
	want := []string{"Enter the 6-digit PIN code.", "Use your current location to pin the store."}
	if steps[5].Status != StatusDraft || len(steps[5].Missing) != 2 ||
		steps[5].Missing[0] != want[0] || steps[5].Missing[1] != want[1] {
		t.Fatalf("shipping step %+v", steps[5])
	}
}

var pinLat, pinLng = 12.9716, 77.5946

func completeDraft() Draft {
	return Draft{
		Name: "Airport kiosk", Categories: StringList{"Groceries"},
		Retail: []Channel{{Channel: "Own shop"}},
		Spoc:   Contact{Name: "Aman", Role: "Owner", Email: "aman@thegreenery.in", Phone: "6203390023"},
		GST:    "29ABCDE1234F1Z5", GSTVerified: true,
		Brand:    Brand{Name: "The Greenery", Manufacturer: "The Greenery", Logo: "logo.png"},
		Bank:     Bank{Holder: "Aman", Number: "123456789", IFSC: "HDFC0001234"},
		Shipping: Shipping{Address: "12 Market Road", City: "Bengaluru", Pin: "560001", Lat: &pinLat, Lng: &pinLng},
		Retailer: Retailer{
			LegalName: "The Greenery", OwnerName: "Aman", IDProofURL: "https://id", BusinessRegURL: "https://reg",
			Payout: "qr", QRURL: "https://qr",
		},
	}
}
