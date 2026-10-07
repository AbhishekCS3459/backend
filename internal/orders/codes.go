package orders

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
)

// codeAlphabet leaves out 0/O and 1/I/L so codes read out over the counter aren't misheard.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// newOrderCode is a short code people can read out, such as "TZ-4K7Q9M".
func newOrderCode() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return "TZ-" + string(b)
}

// pickupCode derives an order's six-digit pickup code from a server secret,
// so it never needs to be stored: a leaked database reveals no codes.
func pickupCode(secret []byte, orderID uuid.UUID) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(orderID[:])
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(mac.Sum(nil)[:4])%1_000_000)
}

func pickupCodeMatches(secret []byte, orderID uuid.UUID, code string) bool {
	return subtle.ConstantTimeCompare([]byte(pickupCode(secret, orderID)), []byte(code)) == 1
}
