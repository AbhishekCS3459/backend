// Package availability maintains store_product_availability, the read model
// customer search uses. Every change to stock, listing, price, catalogue key or
// a store's visibility or location refreshes the affected rows in the same transaction,
// so search never lags behind the retailer. Customers read the table through
// the store_product_search view, which hides the exact count.
package availability

// Bucket is what customers see instead of an exact count.
type Bucket string

const (
	InStock Bucket = "IN_STOCK"
	Low     Bucket = "LOW"
	Out     Bucket = "OUT"
	// ConfirmWithStore is for stock nobody has confirmed recently (14 days by
	// default). It is never stored: the marketplace (MARKETPLACE_STALE_AFTER)
	// and the store_product_search view assign it at read time.
	ConfirmWithStore Bucket = "CONFIRM_WITH_STORE"
)

// BucketFor classifies sellable stock. The retailer's stock status uses it
// too, so the retailer and customers always see the same level.
func BucketFor(available, lowStockThreshold int) Bucket {
	switch {
	case available <= 0:
		return Out
	case available <= lowStockThreshold:
		return Low
	default:
		return InStock
	}
}

// StoreSearchable reports whether customers can find a store's products.
// KYB approval is not required yet.
func StoreSearchable(status string, isOpen bool, onboardingStatus string, deleted bool) bool {
	return !deleted && onboardingStatus == "COMPLETED" && status == "ACTIVE" && isOpen
}
