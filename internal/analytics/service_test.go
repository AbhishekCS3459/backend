package analytics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func TestSyncStatus(t *testing.T) {
	cases := []struct {
		name string
		sync StockSync
		want SyncStatus
	}{
		{"all recorded", StockSync{ListedWithStock: 10, DaysWithoutSales: intPtr(0), UnrecordedRate: floatPtr(0)}, SyncOK},
		{"one miscount", StockSync{ListedWithStock: 10, MissingUnits: 1, UnrecordedRate: floatPtr(0.02), DaysWithoutSales: intPtr(0)}, SyncWarning},
		{"a few quiet days", StockSync{ListedWithStock: 10, DaysWithoutSales: intPtr(3)}, SyncWarning},
		{"one stale product", StockSync{ListedWithStock: 10, StaleProducts: 1, DaysWithoutSales: intPtr(0)}, SyncWarning},
		{"counts find a fifth missing", StockSync{ListedWithStock: 10, MissingUnits: 6, UnrecordedRate: floatPtr(0.25), DaysWithoutSales: intPtr(0)}, SyncCritical},
		{"many missing but mostly recorded", StockSync{ListedWithStock: 10, MissingUnits: 6, UnrecordedRate: floatPtr(0.05), DaysWithoutSales: intPtr(0)}, SyncWarning},
		{"a week without sales", StockSync{ListedWithStock: 10, DaysWithoutSales: intPtr(7)}, SyncCritical},
		{"most stock never updated", StockSync{ListedWithStock: 4, StaleProducts: 3, DaysWithoutSales: intPtr(0)}, SyncCritical},
		{"nothing in stock", StockSync{}, SyncOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, syncStatus(tc.sync))
		})
	}
}

func TestNewWindow(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)
	now := time.Date(2026, 10, 1, 14, 30, 0, 0, loc)

	today, err := newWindow(PeriodToday, loc, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), today.Start)
	assert.Equal(t, time.Date(2026, 9, 30, 0, 0, 0, 0, loc), today.PreviousStart)
	assert.Equal(t, time.Date(2026, 10, 1, 14, 31, 0, 0, loc), today.End, "sales made this minute are included")
	assert.Equal(t, time.Date(2026, 9, 30, 14, 31, 0, 0, loc), today.PreviousEnd, "today compares with yesterday up to the same time")

	lastMinute, err := newWindow(PeriodToday, loc, time.Date(2026, 10, 1, 23, 59, 30, 0, loc))
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), lastMinute.Start, "the last minute of the day is still today")
	assert.Equal(t, BucketHour, today.Bucket)

	week, err := newWindow(Period7Days, loc, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 25, 0, 0, 0, 0, loc), week.Start, "seven calendar days, today included")
	assert.Equal(t, time.Date(2026, 9, 18, 0, 0, 0, 0, loc), week.PreviousStart)

	_, err = newWindow("1y", loc, now)
	assert.ErrorIs(t, err, ErrInvalidPeriod)
}

func TestBuildSeries(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)
	now := time.Date(2026, 10, 1, 14, 30, 0, 0, loc)

	week, err := newWindow(Period7Days, loc, now)
	require.NoError(t, err)
	series := buildSeries(week, loc, []Point{
		{Start: time.Date(2026, 9, 25, 0, 0, 0, 0, loc), Units: 2, Revenue: 40},
		{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, loc), Units: 1, Revenue: 12.5},
	})
	require.Len(t, series, 7, "every day appears, with or without sales")
	assert.Equal(t, 40.0, series[0].Revenue)
	assert.Equal(t, 0.0, series[3].Revenue)
	assert.Equal(t, 12.5, series[6].Revenue)

	today, err := newWindow(PeriodToday, loc, now)
	require.NoError(t, err)
	hourly := buildSeries(today, loc, []Point{{Start: time.Date(2026, 10, 1, 9, 0, 0, 0, loc), Units: 3, Revenue: 90}})
	require.Len(t, hourly, 15, "midnight up to the current hour")
	assert.Equal(t, 90.0, hourly[9].Revenue)

	quarter, err := newWindow(PeriodQuarter, loc, now)
	require.NoError(t, err)
	weekly := buildSeries(quarter, loc, []Point{
		{Start: quarter.Start, Units: 1, Revenue: 10},
		{Start: quarter.Start.AddDate(0, 0, 6), Units: 1, Revenue: 10},
		{Start: quarter.Start.AddDate(0, 0, 7), Units: 1, Revenue: 5},
	})
	require.Len(t, weekly, 13)
	assert.Equal(t, 20.0, weekly[0].Revenue, "days 1-7 fall in the first week")
	assert.Equal(t, 5.0, weekly[1].Revenue)
}
