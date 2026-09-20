package truecaller

import "testing"

func TestParseProfileV2(t *testing.T) {
	body := []byte(`{
		"phoneNumbers": [{"number": "+91 98765 43210"}],
		"name": {"first": "Priya", "last": "Shah"},
		"onlineIdentities": {"email": "priya@example.com"},
		"addresses": [{"countryCode": "IN"}]
	}`)
	profile, err := parseProfile(body)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Phone != "+919876543210" {
		t.Fatalf("phone = %q", profile.Phone)
	}
	if profile.FirstName != "Priya" || profile.Email != "priya@example.com" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestAllowedProfileEndpoint(t *testing.T) {
	if err := allowedProfileEndpoint("https://profile4.truecaller.com/v1/default"); err != nil {
		t.Fatal(err)
	}
	if err := allowedProfileEndpoint("https://evil.example/steal"); err == nil {
		t.Fatal("expected untrusted host to fail")
	}
	if err := allowedProfileEndpoint("http://profile4.truecaller.com/v1/default"); err == nil {
		t.Fatal("expected http to fail")
	}
}
