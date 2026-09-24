package progress

import "strings"

type stepRule struct {
	key  string
	done func(Draft) bool
}

var stepRules = []stepRule{
	{key: "retailer_profile", done: retailerDone},
	{key: "business_details", done: businessDone},
	{key: "seller_details", done: sellerDone},
	{key: "brand_details", done: brandDone},
	{key: "bank_details", done: bankDone},
	{key: "shipping_location", done: shippingDone},
	{key: "verify_submit", done: func(Draft) bool { return false }},
}

func Evaluate(draft Draft) (steps []Step, overall, current string) {
	steps = make([]Step, len(stepRules))
	previousDone := true
	for i, rule := range stepRules {
		done := previousDone && rule.done(draft)
		if rule.key == "verify_submit" {
			done = previousDone && draft.Submitted
		}
		status := StatusDraft
		if done {
			status = StatusCompleted
		} else {
			previousDone = false
		}
		steps[i] = Step{Key: rule.key, Status: status}
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

func retailerDone(draft Draft) bool {
	retailer := draft.Retailer
	return filled(retailer.LegalName) && filled(retailer.OwnerName) && filled(retailer.IDProofURL) && filled(retailer.BusinessRegURL)
}

func businessDone(draft Draft) bool {
	return filled(draft.Name) && len(draft.Categories) > 0 && hasChannel(draft.Retail) && filled(draft.Spoc.Name) && filled(draft.Spoc.Role) && filled(draft.Spoc.Email) && filled(draft.Spoc.Phone)
}

func sellerDone(draft Draft) bool {
	return draft.GSTVerified && len(strings.TrimSpace(draft.GST)) >= 15
}

func brandDone(draft Draft) bool {
	return filled(draft.Brand.Name) && filled(draft.Brand.Manufacturer) && filled(draft.Brand.Logo)
}

func bankDone(draft Draft) bool {
	if draft.Bank.Method == "qr" {
		return filled(draft.Bank.QRURL)
	}
	return filled(draft.Bank.Holder) && len(strings.TrimSpace(draft.Bank.Number)) >= 6 && len(strings.TrimSpace(draft.Bank.IFSC)) >= 4
}

func shippingDone(draft Draft) bool {
	return filled(draft.Shipping.Address)
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
