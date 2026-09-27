package phone

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"98765 43210":       "+919876543210",
		"+91 98765-43210":   "+919876543210",
		"098765 43210":      "+919876543210",
		"919876543210":      "+919876543210",
		"+1 (415) 555-0100": "+14155550100",
	}
	for in, want := range cases {
		got, ok := Normalize(in)
		if !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "12345", "98765abc10", "9876543210+"} {
		if got, ok := Normalize(bad); ok {
			t.Errorf("Normalize(%q) = %q, want invalid", bad, got)
		}
	}
}
