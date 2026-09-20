package truecaller

import "time"

const (
	StatusPending   = "PENDING"
	StatusCompleted = "COMPLETED"
	StatusExpired   = "EXPIRED"
)

const SessionTTL = 5 * time.Minute

type CallbackRequest struct {
	RequestID   string `json:"requestId"`
	AccessToken string `json:"accessToken"`
	Endpoint    string `json:"endpoint"`
}

type StartRequest struct {
	RequestID string `json:"requestId" validate:"required"`
}

type Profile struct {
	FirstName   string `json:"firstName,omitempty"`
	LastName    string `json:"lastName,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Email       string `json:"email,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
}

type Session struct {
	RequestID string    `json:"requestId"`
	Status    string    `json:"status"`
	Profile   *Profile  `json:"profile,omitempty"`
	Token     string    `json:"token,omitempty"`
	User      any       `json:"user,omitempty"`
	Error     string    `json:"error,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type StatusResponse struct {
	Status  string   `json:"status"`
	Token   string   `json:"token,omitempty"`
	User    any      `json:"user,omitempty"`
	Profile *Profile `json:"profile,omitempty"`
	Error   string   `json:"error,omitempty"`
}
