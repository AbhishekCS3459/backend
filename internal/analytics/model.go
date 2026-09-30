// Package analytics reports how a store is doing, from its inventory ledger:
// counter sales, discarded stock, sell-through, and whether the stock record
// keeps up with what really leaves the shelf.
package analytics

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Period is a reporting window ending now, in the store's local time.
type Period string

const (
	PeriodToday   Period = "today"
	Period7Days   Period = "7d"
	Period14Days  Period = "14d"
	Period30Days  Period = "30d"
	PeriodQuarter Period = "90d"
)

// Bucket is the step of the sales chart.
type Bucket string

const (
	BucketHour Bucket = "hour"
	BucketDay  Bucket = "day"
	BucketWeek Bucket = "week"
)

var ErrInvalidPeriod = errors.New("period must be one of today, 7d, 14d, 30d, 90d")

// days is how many calendar days the period covers, today included.
func (p Period) days() (int, error) {
	switch p {
	case PeriodToday:
		return 1, nil
	case Period7Days:
		return 7, nil
	case Period14Days:
		return 14, nil
	case Period30Days:
		return 30, nil
	case PeriodQuarter:
		return 90, nil
	}
	return 0, ErrInvalidPeriod
}

func (p Period) bucket() Bucket {
	switch p {
	case PeriodToday:
		return BucketHour
	case PeriodQuarter:
		return BucketWeek
	}
	return BucketDay
}

// Window is the period being reported and the one it is compared with: the
// same length, directly before (for today, yesterday up to the same time).
type Window struct {
	Period        Period    `json:"period"`
	Timezone      string    `json:"timezone"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	PreviousStart time.Time `json:"previous_start"`
	PreviousEnd   time.Time `json:"previous_end"`
	Bucket        Bucket    `json:"bucket"`
}

func newWindow(period Period, loc *time.Location, now time.Time) (Window, error) {
	days, err := period.days()
	if err != nil {
		return Window{}, err
	}
	now = now.In(loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := midnight.AddDate(0, 0, -(days - 1))
	// Ending at the next whole minute includes the latest sales and keeps the
	// report, and its ETag, the same for the rest of the minute.
	end := now.Truncate(time.Minute).Add(time.Minute)
	return Window{
		Period:        period,
		Timezone:      loc.String(),
		Start:         start,
		End:           end,
		PreviousStart: start.AddDate(0, 0, -days),
		PreviousEnd:   end.AddDate(0, 0, -days),
		Bucket:        period.bucket(),
	}, nil
}

// Report is everything the analytics screen shows for one store and period.
type Report struct {
	Window      Window      `json:"window"`
	Sales       Sales       `json:"sales"`
	Discarded   Discarded   `json:"discarded"`
	SellThrough SellThrough `json:"sell_through"`
	Series      []Point     `json:"series"`
	TopSellers  []Seller    `json:"top_sellers"`
	SlowMovers  []SlowMover `json:"slow_movers"`
	StockSync   StockSync   `json:"stock_sync"`
}

// Sales are counter sales recorded in the store (online orders aren't taken yet).
type Sales struct {
	Revenue         float64 `json:"revenue"`
	PreviousRevenue float64 `json:"previous_revenue"`
	Units           int     `json:"units"`
	PreviousUnits   int     `json:"previous_units"`
	// Estimated is true when some sales predate recorded sale prices and are valued at today's price.
	Estimated bool `json:"estimated"`
}

// Discarded is stock thrown away as expired or damaged, at the store's selling price.
type Discarded struct {
	Value         float64 `json:"value"`
	PreviousValue float64 `json:"previous_value"`
	Units         int     `json:"units"`
	ExpiredUnits  int     `json:"expired_units"`
	DamagedUnits  int     `json:"damaged_units"`
}

// SellThrough is units sold over units there were to sell: stock at the start plus stock received.
// Rate is nil when there was nothing to sell.
type SellThrough struct {
	Rate      *float64 `json:"rate"`
	Sold      int      `json:"sold"`
	Available int      `json:"available"`
}

// Point is one step of the sales chart; Start is in the store's local time.
type Point struct {
	Start   time.Time `json:"start"`
	Revenue float64   `json:"revenue"`
	Units   int       `json:"units"`
}

type Seller struct {
	VariantID uuid.UUID `json:"variant_id"`
	Name      string    `json:"name"`
	Units     int       `json:"units"`
	Revenue   float64   `json:"revenue"`
}

// SlowMover is a listed product with stock that sold little or nothing in the period.
type SlowMover struct {
	VariantID  uuid.UUID  `json:"variant_id"`
	Name       string     `json:"name"`
	OnHand     int        `json:"on_hand"`
	UnitsSold  int        `json:"units_sold"`
	LastSaleAt *time.Time `json:"last_sale_at"`
}

// SyncStatus says how far the stock record can be trusted.
type SyncStatus string

const (
	SyncOK       SyncStatus = "ok"
	SyncWarning  SyncStatus = "warning"
	SyncCritical SyncStatus = "critical"
)

// StockSync detects counter sales that never reach the stock record. Unrecorded
// sales leave stock online that isn't on the shelf, so online shoppers order
// items the store can't hand over.
type StockSync struct {
	Status SyncStatus `json:"status"`

	// MissingUnits left the shop without a record in the period: stock counts
	// that found fewer units than recorded, plus stock marked lost.
	MissingUnits int     `json:"missing_units"`
	MissingValue float64 `json:"missing_value"`
	// UnrecordedRate is MissingUnits over everything that left (sold + missing); nil if nothing left.
	UnrecordedRate *float64    `json:"unrecorded_rate"`
	Shortfalls     []Shortfall `json:"shortfalls"`

	// StaleProducts have stock but no stock update for StaleAfterDays; online
	// shoppers see them as "confirm with store".
	StaleProducts  int            `json:"stale_products"`
	StaleAfterDays int            `json:"stale_after_days"`
	Stale          []StaleProduct `json:"stale"`

	// ListedWithStock is how many listed products have units on hand.
	ListedWithStock int `json:"listed_with_stock"`
	// LastSaleAt is the store's most recent recorded counter sale, ever.
	LastSaleAt *time.Time `json:"last_sale_at"`
	// DaysWithoutSales is the number of full days since LastSaleAt (or since the first product was listed).
	DaysWithoutSales *int `json:"days_without_sales"`
}

// Shortfall is a product whose stock counts found units missing in the period.
type Shortfall struct {
	VariantID    uuid.UUID `json:"variant_id"`
	Name         string    `json:"name"`
	MissingUnits int       `json:"missing_units"`
	LastFoundAt  time.Time `json:"last_found_at"`
}

// StaleProduct is a listed product with stock whose count hasn't changed or been confirmed recently.
type StaleProduct struct {
	VariantID    uuid.UUID `json:"variant_id"`
	Name         string    `json:"name"`
	Available    int       `json:"available"`
	LastUpdateAt time.Time `json:"last_update_at"`
}
