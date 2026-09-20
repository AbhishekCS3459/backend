package truecaller

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func allowedProfileEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("invalid profile endpoint")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "truecaller.com" && !strings.HasSuffix(host, ".truecaller.com") {
		return fmt.Errorf("untrusted profile endpoint")
	}
	return nil
}

func parseProfile(body []byte) (*Profile, error) {
	var v2 struct {
		PhoneNumbers []any `json:"phoneNumbers"`
		Name         struct {
			First string `json:"first"`
			Last  string `json:"last"`
		} `json:"name"`
		OnlineIdentities struct {
			Email string `json:"email"`
		} `json:"onlineIdentities"`
		Addresses []struct {
			CountryCode string `json:"countryCode"`
		} `json:"addresses"`
		AvatarURL string `json:"avatarUrl"`
	}
	if err := json.Unmarshal(body, &v2); err == nil && (len(v2.PhoneNumbers) > 0 || v2.Name.First != "") {
		profile := &Profile{
			FirstName: v2.Name.First,
			LastName:  v2.Name.Last,
			Email:     v2.OnlineIdentities.Email,
			AvatarURL: v2.AvatarURL,
		}
		if len(v2.Addresses) > 0 {
			profile.CountryCode = v2.Addresses[0].CountryCode
		}
		profile.Phone = firstPhone(v2.PhoneNumbers)
		if profile.Phone != "" {
			return profile, nil
		}
	}

	var v1 struct {
		FirstName   string `json:"firstName"`
		LastName    string `json:"lastName"`
		FirstName2  string `json:"first_name"`
		LastName2   string `json:"last_name"`
		PhoneNumber any    `json:"phoneNumber"`
		PhoneNumber2 any   `json:"phone_number"`
		Email       string `json:"email"`
		CountryCode string `json:"countryCode"`
		CountryCode2 string `json:"country_code"`
		AvatarURL   string `json:"avatarUrl"`
		AvatarURL2  string `json:"avatar_url"`
		Data        *struct {
			FirstName   string `json:"firstName"`
			LastName    string `json:"lastName"`
			PhoneNumber any    `json:"phoneNumber"`
			Email       string `json:"email"`
			CountryCode string `json:"countryCode"`
			AvatarURL   string `json:"avatarUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v1); err != nil {
		return nil, fmt.Errorf("failed to parse Truecaller profile")
	}

	profile := &Profile{
		FirstName:   firstNonEmpty(v1.FirstName, v1.FirstName2),
		LastName:    firstNonEmpty(v1.LastName, v1.LastName2),
		Email:       v1.Email,
		CountryCode: firstNonEmpty(v1.CountryCode, v1.CountryCode2),
		AvatarURL:   firstNonEmpty(v1.AvatarURL, v1.AvatarURL2),
		Phone:       stringifyPhone(firstNonNil(v1.PhoneNumber, v1.PhoneNumber2)),
	}
	if v1.Data != nil {
		if profile.FirstName == "" {
			profile.FirstName = v1.Data.FirstName
		}
		if profile.LastName == "" {
			profile.LastName = v1.Data.LastName
		}
		if profile.Email == "" {
			profile.Email = v1.Data.Email
		}
		if profile.CountryCode == "" {
			profile.CountryCode = v1.Data.CountryCode
		}
		if profile.AvatarURL == "" {
			profile.AvatarURL = v1.Data.AvatarURL
		}
		if profile.Phone == "" {
			profile.Phone = stringifyPhone(v1.Data.PhoneNumber)
		}
	}
	if profile.Phone == "" {
		return nil, fmt.Errorf("profile did not include a verified phone number")
	}
	return profile, nil
}

func firstPhone(values []any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return normalizePhone(typed)
			}
		case map[string]any:
			if number, ok := typed["number"].(string); ok && number != "" {
				return normalizePhone(number)
			}
			if number, ok := typed["phoneNumber"].(string); ok && number != "" {
				return normalizePhone(number)
			}
		default:
			if phone := stringifyPhone(typed); phone != "" {
				return phone
			}
		}
	}
	return ""
}

func stringifyPhone(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return normalizePhone(typed)
	case float64:
		return normalizePhone(fmt.Sprintf("%.0f", typed))
	case json.Number:
		return normalizePhone(typed.String())
	default:
		return normalizePhone(fmt.Sprintf("%v", typed))
	}
}

func normalizePhone(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}
	var b strings.Builder
	if strings.HasPrefix(phone, "+") {
		b.WriteByte('+')
	}
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || out == "+" {
		return ""
	}
	if !strings.HasPrefix(out, "+") {
		out = "+" + out
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
