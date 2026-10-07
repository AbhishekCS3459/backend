package orders

import (
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCanMove(t *testing.T) {
	allowed := []struct{ from, to Status }{
		{StatusPendingPayment, StatusPlaced},
		{StatusPendingPayment, StatusCancelled},
		{StatusPendingPayment, StatusExpired},
		{StatusPlaced, StatusAccepted},
		{StatusPlaced, StatusRejected},
		{StatusPlaced, StatusCancelled},
		{StatusAccepted, StatusReady},
		{StatusAccepted, StatusRejected},
		{StatusAccepted, StatusCancelled},
		{StatusReady, StatusCompleted},
		{StatusReady, StatusNoShow},
		{StatusExpired, StatusPlaced},
	}
	for _, m := range allowed {
		assert.True(t, canMove(m.from, m.to), "%s -> %s should be allowed", m.from, m.to)
	}

	refused := []struct{ from, to Status }{
		{StatusReady, StatusCancelled},
		{StatusPendingPayment, StatusAccepted},
		{StatusPlaced, StatusReady},
		{StatusPlaced, StatusPlaced},
		{StatusCancelled, StatusCancelled},
		{StatusExpired, StatusCancelled},
	}
	for _, m := range refused {
		assert.False(t, canMove(m.from, m.to), "%s -> %s should be refused", m.from, m.to)
	}
	for _, closed := range []Status{StatusCompleted, StatusCancelled, StatusRejected, StatusNoShow} {
		assert.Empty(t, transitions[closed], "%s is final", closed)
		assert.False(t, closed.Open())
	}
}

func TestPickupCode(t *testing.T) {
	secret, orderID := []byte("secret"), uuid.New()
	code := pickupCode(secret, orderID)
	assert.Regexp(t, `^\d{6}$`, code)
	assert.Equal(t, code, pickupCode(secret, orderID), "the code must not change between calls")
	assert.True(t, pickupCodeMatches(secret, orderID, code))
	assert.False(t, pickupCodeMatches([]byte("other"), orderID, code))
	assert.False(t, pickupCodeMatches(secret, orderID, ""))
}

func TestOrderCode(t *testing.T) {
	pattern := regexp.MustCompile(`^TZ-[23456789ABCDEFGHJKMNPQRSTUVWXYZ]{6}$`)
	for range 100 {
		assert.Regexp(t, pattern, newOrderCode())
	}
}
