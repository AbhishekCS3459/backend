package inventory

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intp(n int) *int { return &n }

func TestReceiveChange(t *testing.T) {
	c, err := receiveChange(20)
	require.NoError(t, err)
	assert.Equal(t, TypeStockReceived, c.Type)
	assert.Equal(t, 20, c.Delta)
	assert.Nil(t, c.Reason)

	for _, qty := range []int{0, -1, MaxChange + 1} {
		_, err := receiveChange(qty)
		var ve *ValidationError
		assert.True(t, errors.As(err, &ve), "quantity %d should be rejected", qty)
	}
}

func TestSaleChange(t *testing.T) {
	c, err := saleChange(3)
	require.NoError(t, err)
	assert.Equal(t, TypeOfflineSale, c.Type)
	assert.Equal(t, -3, c.Delta)
	assert.Nil(t, c.Reason)

	for _, qty := range []int{0, -1, MaxChange + 1} {
		_, err := saleChange(qty)
		var ve *ValidationError
		assert.True(t, errors.As(err, &ve), "quantity %d should be rejected", qty)
	}
}

func TestOrderChanges(t *testing.T) {
	c, err := reserveChange(4)
	require.NoError(t, err)
	assert.Equal(t, change{Type: TypeOrderReserved, ReservedDelta: 4}, c)

	c, err = releaseChange(4)
	require.NoError(t, err)
	assert.Equal(t, change{Type: TypeOrderReleased, ReservedDelta: -4}, c)

	c, err = pickupChange(4)
	require.NoError(t, err)
	assert.Equal(t, change{Type: TypeOrderPickup, Delta: -4, ReservedDelta: -4}, c)

	for _, build := range []func(int) (change, error){reserveChange, releaseChange, pickupChange} {
		for _, qty := range []int{0, -1, MaxChange + 1} {
			_, err := build(qty)
			var ve *ValidationError
			assert.True(t, errors.As(err, &ve), "quantity %d should be rejected", qty)
		}
	}
}

func TestAdjustChangeMode(t *testing.T) {
	current := Stock{OnHand: 20, Reserved: 2}

	c, err := adjustChange(current, AdjustRequest{Mode: ModeChange, Quantity: intp(-3), Reason: " damaged "})
	require.NoError(t, err)
	assert.Equal(t, TypeAdjustment, c.Type)
	assert.Equal(t, -3, c.Delta)
	require.NotNil(t, c.Reason)
	assert.Equal(t, ReasonDamaged, *c.Reason)
	assert.Nil(t, c.Counted)

	invalid := map[string]AdjustRequest{
		"missing quantity":    {Mode: ModeChange, Reason: ReasonLost},
		"zero quantity":       {Mode: ModeChange, Quantity: intp(0), Reason: ReasonLost},
		"too large":           {Mode: ModeChange, Quantity: intp(MaxChange + 1), Reason: ReasonLost},
		"too small":           {Mode: ModeChange, Quantity: intp(-MaxChange - 1), Reason: ReasonLost},
		"missing reason":      {Mode: ModeChange, Quantity: intp(-1)},
		"unknown reason":      {Mode: ModeChange, Quantity: intp(-1), Reason: "STOLEN_BY_ALIENS"},
		"stock count reason":  {Mode: ModeChange, Quantity: intp(-1), Reason: ReasonStockCount},
		"counted with change": {Mode: ModeChange, Quantity: intp(-1), Reason: ReasonLost, CountedQuantity: intp(5)},
		"unknown mode":        {Mode: "SET", Quantity: intp(5)},
	}
	for name, req := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := adjustChange(current, req)
			var ve *ValidationError
			assert.True(t, errors.As(err, &ve), "got %v", err)
		})
	}
}

func TestAdjustCountMode(t *testing.T) {
	current := Stock{OnHand: 20, Reserved: 2}

	c, err := adjustChange(current, AdjustRequest{Mode: ModeCount, CountedQuantity: intp(17)})
	require.NoError(t, err)
	assert.Equal(t, -3, c.Delta, "difference is computed from the locked stock")
	require.NotNil(t, c.Reason)
	assert.Equal(t, ReasonStockCount, *c.Reason)
	require.NotNil(t, c.Counted)
	assert.Equal(t, 17, *c.Counted)

	c, err = adjustChange(current, AdjustRequest{Mode: ModeCount, CountedQuantity: intp(20)})
	require.NoError(t, err)
	assert.Equal(t, 0, c.Delta, "a count that matches is still recorded")

	invalid := map[string]AdjustRequest{
		"missing counted":   {Mode: ModeCount},
		"negative counted":  {Mode: ModeCount, CountedQuantity: intp(-1)},
		"counted too large": {Mode: ModeCount, CountedQuantity: intp(maxOnHand + 1)},
		"quantity on count": {Mode: ModeCount, CountedQuantity: intp(5), Quantity: intp(1)},
		"reason on count":   {Mode: ModeCount, CountedQuantity: intp(5), Reason: ReasonLost},
	}
	for name, req := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := adjustChange(current, req)
			var ve *ValidationError
			assert.True(t, errors.As(err, &ve), "got %v", err)
		})
	}
}

func TestApply(t *testing.T) {
	tests := []struct {
		name    string
		current Stock
		change  change
		want    Stock
		wantErr string
	}{
		{
			name:    "receive",
			current: Stock{OnHand: 5, Reserved: 2},
			change:  change{Delta: 20},
			want:    Stock{OnHand: 25, Reserved: 2},
		},
		{
			name:    "reduce down to reserved",
			current: Stock{OnHand: 10, Reserved: 7},
			change:  change{Delta: -3},
			want:    Stock{OnHand: 7, Reserved: 7},
		},
		{
			name:    "reduce to zero",
			current: Stock{OnHand: 3},
			change:  change{Delta: -3},
			want:    Stock{OnHand: 0},
		},
		{
			name:    "below reserved",
			current: Stock{OnHand: 10, Reserved: 7},
			change:  change{Delta: -5},
			wantErr: "Cannot reduce stock by 5 because 7 units are currently reserved.",
		},
		{
			name:    "below reserved singular",
			current: Stock{OnHand: 2, Reserved: 1},
			change:  change{Delta: -2},
			wantErr: "Cannot reduce stock by 2 because 1 unit is currently reserved.",
		},
		{
			name:    "below zero",
			current: Stock{OnHand: 3},
			change:  change{Delta: -5},
			wantErr: "Cannot reduce stock by 5 because only 3 units are on hand.",
		},
		{
			name:    "count below reserved",
			current: Stock{OnHand: 10, Reserved: 7},
			change:  change{Delta: -6, Counted: intp(4)},
			wantErr: "Cannot set stock to 4 because 7 units are currently reserved.",
		},
		{
			name:    "sale within free stock",
			current: Stock{OnHand: 10, Reserved: 3},
			change:  change{Type: TypeOfflineSale, Delta: -7},
			want:    Stock{OnHand: 3, Reserved: 3},
		},
		{
			name:    "sale of reserved units",
			current: Stock{OnHand: 10, Reserved: 3},
			change:  change{Type: TypeOfflineSale, Delta: -8},
			wantErr: "Cannot sell 8: only 7 units are free to sell (3 held for online orders).",
		},
		{
			name:    "sale beyond stock",
			current: Stock{OnHand: 1},
			change:  change{Type: TypeOfflineSale, Delta: -2},
			wantErr: "Cannot sell 2: only 1 unit is free to sell.",
		},
		{
			name:    "reserve free units",
			current: Stock{OnHand: 5, Reserved: 2},
			change:  change{Type: TypeOrderReserved, ReservedDelta: 3},
			want:    Stock{OnHand: 5, Reserved: 5},
		},
		{
			name:    "reserve more than free",
			current: Stock{OnHand: 5, Reserved: 4},
			change:  change{Type: TypeOrderReserved, ReservedDelta: 2},
			wantErr: "Cannot reserve 2: only 1 unit is free.",
		},
		{
			name:    "release",
			current: Stock{OnHand: 5, Reserved: 3},
			change:  change{Type: TypeOrderReleased, ReservedDelta: -3},
			want:    Stock{OnHand: 5, Reserved: 0},
		},
		{
			name:    "release more than reserved",
			current: Stock{OnHand: 5, Reserved: 1},
			change:  change{Type: TypeOrderReleased, ReservedDelta: -2},
			wantErr: "Cannot free 2: only 1 unit is reserved.",
		},
		{
			name:    "pickup takes reserved units off the shelf",
			current: Stock{OnHand: 5, Reserved: 3},
			change:  change{Type: TypeOrderPickup, Delta: -3, ReservedDelta: -3},
			want:    Stock{OnHand: 2, Reserved: 0},
		},
		{
			name:    "above maximum",
			current: Stock{OnHand: maxOnHand},
			change:  change{Delta: 1},
			wantErr: "stock would exceed the maximum allowed quantity",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := apply(tt.current, tt.change)
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
				return
			}
			var se *StockError
			require.True(t, errors.As(err, &se), "got %v", err)
			assert.Equal(t, tt.wantErr, se.Message)
		})
	}
}

func TestStockStatus(t *testing.T) {
	tests := []struct {
		name      string
		stock     Stock
		available bool
		listed    bool
		want      string
	}{
		{"unlisted wins", Stock{OnHand: 50}, true, false, "unlisted"},
		{"switched off", Stock{OnHand: 50}, false, true, "unavailable"},
		{"nothing on hand", Stock{}, true, true, "out"},
		{"all reserved", Stock{OnHand: 4, Reserved: 4}, true, true, "out"},
		{"at threshold", Stock{OnHand: 7, Reserved: 2}, true, true, "low"},
		{"above threshold", Stock{OnHand: 8, Reserved: 2}, true, true, "in_stock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StockStatus(tt.stock, 5, tt.available, tt.listed))
		})
	}
}
