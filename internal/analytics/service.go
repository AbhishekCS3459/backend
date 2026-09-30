package analytics

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
)

var ErrInvalidTimezone = errors.New("tz must be an IANA time zone such as Asia/Kolkata")

const (
	DefaultTimezone = "Asia/Kolkata"
	listLimit       = 5
	shownStale      = 5

	// A count gap this large, relative to everything that left the shop,
	// points to sales that weren't recorded rather than the odd miscount.
	criticalUnrecordedRate  = 0.2
	criticalMissingUnits    = 5
	warningDaysWithoutSales = 3
	// A shop with stock that records no sale for a week is almost certainly
	// selling without recording it.
	criticalDaysWithoutSales = 7
	criticalStaleShare       = 0.5
	criticalStaleMinimum     = 3
)

type Service interface {
	Report(ctx context.Context, userID, storeID uuid.UUID, period Period, tz string) (*Report, error)
}

type service struct {
	repo       Repository
	access     storeaccess.Resolver
	staleAfter time.Duration
	now        func() time.Time
}

// NewService reports from repo. staleAfter must match the customer search, so
// "not updated" here means "confirm with store" to online shoppers.
func NewService(repo Repository, access storeaccess.Resolver, staleAfter time.Duration) Service {
	return &service{repo: repo, access: access, staleAfter: staleAfter, now: time.Now}
}

func (s *service) Report(ctx context.Context, userID, storeID uuid.UUID, period Period, tz string) (*Report, error) {
	if _, err := s.access.Require(ctx, userID, storeID, storeaccess.AnalyticsView); err != nil {
		return nil, err
	}
	loc, err := location(tz)
	if err != nil {
		return nil, err
	}
	now := s.now()
	w, err := newWindow(period, loc, now)
	if err != nil {
		return nil, err
	}

	report := &Report{Window: w}
	if report.Sales, err = s.repo.Sales(ctx, storeID, w.Start, w.End); err != nil {
		return nil, err
	}
	previous, err := s.repo.Sales(ctx, storeID, w.PreviousStart, w.PreviousEnd)
	if err != nil {
		return nil, err
	}
	report.Sales.PreviousRevenue, report.Sales.PreviousUnits = money(previous.Revenue), previous.Units
	report.Sales.Revenue = money(report.Sales.Revenue)

	if report.Discarded, err = s.repo.Discarded(ctx, storeID, w.Start, w.End); err != nil {
		return nil, err
	}
	previousDiscarded, err := s.repo.Discarded(ctx, storeID, w.PreviousStart, w.PreviousEnd)
	if err != nil {
		return nil, err
	}
	report.Discarded.Value, report.Discarded.PreviousValue = money(report.Discarded.Value), money(previousDiscarded.Value)

	opening, err := s.repo.StockAt(ctx, storeID, w.Start)
	if err != nil {
		return nil, err
	}
	received, err := s.repo.Received(ctx, storeID, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	report.SellThrough = sellThrough(report.Sales.Units, opening+received)

	unit := "day"
	if w.Bucket == BucketHour {
		unit = "hour"
	}
	points, err := s.repo.SalesBy(ctx, storeID, w.Start, w.End, unit, loc.String())
	if err != nil {
		return nil, err
	}
	report.Series = buildSeries(w, loc, points)

	if report.TopSellers, err = s.repo.TopSellers(ctx, storeID, w.Start, w.End, listLimit); err != nil {
		return nil, err
	}
	for i := range report.TopSellers {
		report.TopSellers[i].Revenue = money(report.TopSellers[i].Revenue)
	}
	if report.SlowMovers, err = s.repo.SlowMovers(ctx, storeID, w.Start, w.End, listLimit); err != nil {
		return nil, err
	}
	if report.StockSync, err = s.stockSync(ctx, storeID, w, report.Sales.Units); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *service) stockSync(ctx context.Context, storeID uuid.UUID, w Window, sold int) (StockSync, error) {
	sync := StockSync{StaleAfterDays: int(s.staleAfter.Hours() / 24)}
	var err error
	if sync.MissingUnits, sync.MissingValue, err = s.repo.Missing(ctx, storeID, w.Start, w.End); err != nil {
		return StockSync{}, err
	}
	sync.MissingValue = money(sync.MissingValue)
	if sync.MissingUnits+sold > 0 {
		rate := float64(sync.MissingUnits) / float64(sync.MissingUnits+sold)
		sync.UnrecordedRate = &rate
	}
	if sync.Shortfalls, err = s.repo.Shortfalls(ctx, storeID, w.Start, w.End, listLimit); err != nil {
		return StockSync{}, err
	}
	stale, err := s.repo.Stale(ctx, storeID, w.End.Add(-s.staleAfter))
	if err != nil {
		return StockSync{}, err
	}
	sync.StaleProducts = len(stale)
	sync.Stale = stale[:min(len(stale), shownStale)]

	activity, err := s.repo.Activity(ctx, storeID)
	if err != nil {
		return StockSync{}, err
	}
	sync.ListedWithStock = activity.ListedWithStock
	sync.LastSaleAt = activity.LastSaleAt
	if since := activity.LastSaleAt; activity.ListedWithStock > 0 && (since != nil || activity.FirstListedAt != nil) {
		if since == nil {
			since = activity.FirstListedAt
		}
		days := int(w.End.Sub(*since).Hours() / 24)
		sync.DaysWithoutSales = &days
	}
	sync.Status = syncStatus(sync)
	return sync, nil
}

// syncStatus rates how far the stock record can be trusted. Any sign of
// unrecorded sales is a warning; strong signs are critical.
func syncStatus(s StockSync) SyncStatus {
	days := -1
	if s.DaysWithoutSales != nil {
		days = *s.DaysWithoutSales
	}
	rate := 0.0
	if s.UnrecordedRate != nil {
		rate = *s.UnrecordedRate
	}
	staleShare := 0.0
	if s.ListedWithStock > 0 {
		staleShare = float64(s.StaleProducts) / float64(s.ListedWithStock)
	}
	switch {
	case s.MissingUnits >= criticalMissingUnits && rate >= criticalUnrecordedRate,
		days >= criticalDaysWithoutSales,
		s.StaleProducts >= criticalStaleMinimum && staleShare >= criticalStaleShare:
		return SyncCritical
	case s.MissingUnits > 0, s.StaleProducts > 0, days >= warningDaysWithoutSales:
		return SyncWarning
	}
	return SyncOK
}

func sellThrough(sold, available int) SellThrough {
	st := SellThrough{Sold: sold, Available: available}
	if available > 0 {
		rate := math.Min(1, float64(sold)/float64(available))
		st.Rate = &rate
	}
	return st
}

// buildSeries lays sales onto every step of the window, including steps with no sales.
func buildSeries(w Window, loc *time.Location, points []Point) []Point {
	var series []Point
	switch w.Bucket {
	case BucketHour:
		for t := w.Start; t.Before(w.End); t = t.Add(time.Hour) {
			series = append(series, Point{Start: t})
		}
	case BucketDay:
		for t := w.Start; t.Before(w.End); t = t.AddDate(0, 0, 1) {
			series = append(series, Point{Start: t})
		}
	case BucketWeek:
		for t := w.Start; t.Before(w.End); t = t.AddDate(0, 0, 7) {
			series = append(series, Point{Start: t})
		}
	}
	for _, p := range points {
		i := bucketIndex(w, loc, p.Start)
		if i < 0 || i >= len(series) {
			continue
		}
		series[i].Units += p.Units
		series[i].Revenue += p.Revenue
	}
	for i := range series {
		series[i].Revenue = money(series[i].Revenue)
	}
	return series
}

func bucketIndex(w Window, loc *time.Location, start time.Time) int {
	if w.Bucket == BucketHour {
		return int(start.Sub(w.Start) / time.Hour)
	}
	// Whole calendar days, so a daylight-saving change can't shift a day into the next bucket.
	day := func(t time.Time) time.Time {
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	days := int(day(start).Sub(day(w.Start)).Hours() / 24)
	if w.Bucket == BucketWeek {
		return days / 7
	}
	return days
}

func location(tz string) (*time.Location, error) {
	if tz == "" {
		tz = DefaultTimezone
	}
	// "Local" would mean the server's zone, not the store's.
	if tz == "Local" {
		return nil, ErrInvalidTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, ErrInvalidTimezone
	}
	return loc, nil
}

func money(v float64) float64 {
	return math.Round(v*100) / 100
}
