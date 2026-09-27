// Package phone normalises user-entered phone numbers.
package phone

import "strings"

// Normalize returns the number as "+<country code><number>". Ten-digit and
// 0-prefixed eleven-digit numbers are treated as Indian numbers.
func Normalize(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	var digits strings.Builder
	for i, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+' && i == 0, r == ' ', r == '-', r == '(', r == ')', r == '.':
		default:
			return "", false
		}
	}
	d := digits.String()
	switch {
	case len(d) == 10:
		d = "91" + d
	case len(d) == 11 && d[0] == '0':
		d = "91" + d[1:]
	}
	if len(d) < 11 || len(d) > 15 {
		return "", false
	}
	return "+" + d, true
}
