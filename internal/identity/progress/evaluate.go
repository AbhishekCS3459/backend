package progress

import "strings"

type stepRule struct {
	key string
	// missing lists what the retailer still has to fill in; empty means the step is done.
	missing func(Draft) []string
}

var stepRules = []stepRule{
	{key: "retailer_profile", missing: retailerMissing},
	{key: "business_details", missing: businessMissing},
	{key: "seller_details", missing: sellerMissing},
	{key: "brand_details", missing: brandMissing},
	{key: "bank_details", missing: func(draft Draft) []string { return BankMissing(draft.Bank) }},
	{key: "shipping_location", missing: shippingMissing},
	{key: "verify_submit", missing: func(Draft) []string { return nil }},
}

var stepLabels = map[string]string{
	"retailer_profile":  "Retailer profile",
	"business_details":  "Business details",
	"seller_details":    "Seller details",
	"brand_details":     "Brand details",
	"bank_details":      "Bank details",
	"shipping_location": "Shipping location",
	"verify_submit":     "Verify & submit",
}

// StepLabel is the name the retailer sees for a step key.
func StepLabel(key string) string {
	if label, ok := stepLabels[key]; ok {
		return label
	}
	return key
}

// Evaluate judges each step on its own details, so a finished step shows as
// completed even while an earlier one is still open. "Verify & submit" is
// completed only once every other step is done and the draft was submitted.
func Evaluate(draft Draft) (steps []Step, overall, current string) {
	steps = make([]Step, len(stepRules))
	allDone := true
	for i, rule := range stepRules {
		step := Step{Key: rule.key, Status: StatusDraft}
		if rule.key == "verify_submit" {
			if allDone && draft.Submitted {
				step.Status = StatusCompleted
			} else if !allDone {
				step.Missing = []string{"Finish every step above."}
			}
		} else if step.Missing = rule.missing(draft); len(step.Missing) == 0 {
			step.Status = StatusCompleted
		} else {
			allDone = false
		}
		steps[i] = step
	}

	overall = StatusCompleted
	current = steps[len(steps)-1].Key
	for _, step := range steps {
		if step.Status != StatusCompleted {
			overall = StatusDraft
			current = step.Key
			break
		}
	}
	return steps, overall, current
}

// BankComplete reports whether the payout details are enough to pay out to.
func BankComplete(bank Bank) bool {
	return len(BankMissing(bank)) == 0
}

func BankMissing(bank Bank) []string {
	if bank.Method == "qr" {
		return need(nil, filled(bank.QRURL), "Upload a payment QR.")
	}
	var missing []string
	missing = need(missing, filled(bank.Holder), "Enter the account holder.")
	missing = need(missing, len(strings.TrimSpace(bank.Number)) >= 6, "Enter the account number.")
	return need(missing, len(strings.TrimSpace(bank.IFSC)) >= 4, "Enter the IFSC.")
}

func retailerMissing(draft Draft) []string {
	retailer := draft.Retailer
	var missing []string
	missing = need(missing, filled(retailer.LegalName), "Enter the legal name.")
	missing = need(missing, filled(retailer.OwnerName), "Enter the owner name.")
	missing = need(missing, filled(retailer.IDProofURL), "Upload an ID proof.")
	return need(missing, filled(retailer.BusinessRegURL), "Upload the business registration.")
}

func businessMissing(draft Draft) []string {
	var missing []string
	missing = need(missing, len([]rune(strings.TrimSpace(draft.Name))) >= 2, "Enter the store name.")
	missing = need(missing, len(draft.Categories) > 0, "Pick at least one category.")
	missing = need(missing, hasChannel(draft.Retail), "Choose a retail channel.")
	missing = need(missing, filled(draft.Spoc.Name), "Enter the contact name.")
	missing = need(missing, filled(draft.Spoc.Role), "Enter the contact role.")
	missing = need(missing, filled(draft.Spoc.Email), "Enter the contact email.")
	return need(missing, filled(draft.Spoc.Phone), "Enter the contact phone.")
}

func sellerMissing(draft Draft) []string {
	if len(strings.TrimSpace(draft.GST)) < 15 {
		return []string{"Enter the 15-character GSTIN."}
	}
	return need(nil, draft.GSTVerified, "Verify the GSTIN.")
}

func brandMissing(draft Draft) []string {
	var missing []string
	missing = need(missing, filled(draft.Brand.Name), "Enter the brand name.")
	missing = need(missing, filled(draft.Brand.Manufacturer), "Enter the manufacturer.")
	return need(missing, filled(draft.Brand.Logo), "Upload the brand logo.")
}

func shippingMissing(draft Draft) []string {
	shipping := draft.Shipping
	var missing []string
	missing = need(missing, filled(shipping.Address), "Enter the shipping address.")
	missing = need(missing, filled(shipping.City), "Enter the city.")
	missing = need(missing, validPin(shipping.Pin), "Enter the 6-digit PIN code.")
	return need(missing, shipping.Lat != nil && shipping.Lng != nil, "Use your current location to pin the store.")
}

func validPin(pin string) bool {
	pin = strings.TrimSpace(pin)
	if len(pin) != 6 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func need(missing []string, ok bool, reason string) []string {
	if ok {
		return missing
	}
	return append(missing, reason)
}

func hasChannel(rows []Channel) bool {
	for _, row := range rows {
		if filled(row.Channel) {
			return true
		}
	}
	return false
}

func filled(value string) bool {
	return strings.TrimSpace(value) != ""
}
