package progress

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	StatusDraft     = "draft"
	StatusCompleted = "completed"
)

type Record struct {
	ID        uuid.UUID `json:"-" gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `json:"-" gorm:"type:uuid;uniqueIndex"`
	Status    string    `json:"-"`
	Payload   JSONB     `json:"-" gorm:"type:jsonb"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (Record) TableName() string { return "user_progress" }

type JSONB json.RawMessage

func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	return string(j), nil
}

func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = JSONB([]byte("{}"))
		return nil
	}
	switch typed := value.(type) {
	case []byte:
		copied := make([]byte, len(typed))
		copy(copied, typed)
		*j = JSONB(copied)
		return nil
	case string:
		*j = JSONB([]byte(typed))
		return nil
	default:
		return errors.New("user progress payload must be json")
	}
}

type StringList []string

func (s *StringList) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	if data[0] == '"' {
		var one string
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		if strings.TrimSpace(one) == "" {
			*s = nil
			return nil
		}
		*s = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

type Channel struct {
	Channel string `json:"channel"`
	Metric  string `json:"metric"`
	Link    string `json:"link"`
}

type Draft struct {
	Name        string     `json:"name"`
	Categories  StringList `json:"categories"`
	Retail      []Channel  `json:"retail"`
	Social      []Channel  `json:"social"`
	Spoc        Contact    `json:"spoc"`
	GST         string     `json:"gst"`
	GSTVerified bool       `json:"gst_verified"`
	Brand       Brand      `json:"brand"`
	Bank        Bank       `json:"bank"`
	Shipping    Shipping   `json:"shipping"`
	Retailer    Retailer   `json:"retailer"`
	Submitted   bool       `json:"submitted"`
}

type Contact struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

type Brand struct {
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Trademark    string `json:"trademark"`
	Logo         string `json:"logo"`
	Letter       string `json:"letter"`
}

type Bank struct {
	Method string `json:"method"`
	Holder string `json:"holder"`
	Number string `json:"number"`
	IFSC   string `json:"ifsc"`
	Bank   string `json:"bank"`
	QRURL  string `json:"qr_url"`
}

type Shipping struct {
	Label   string `json:"label"`
	Address string `json:"address"`
	City    string `json:"city"`
	Pin     string `json:"pin"`
}

type Retailer struct {
	LegalName      string `json:"legal_name"`
	OwnerName      string `json:"owner_name"`
	IDProofURL     string `json:"id_proof_url"`
	BusinessRegURL string `json:"business_reg_url"`
	Payout         string `json:"payout"`
	Holder         string `json:"holder"`
	Number         string `json:"number"`
	IFSC           string `json:"ifsc"`
	QRURL          string `json:"qr_url"`
}

type Step struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

type View struct {
	Status      string     `json:"status"`
	CurrentStep string     `json:"current_step"`
	Steps       []Step     `json:"steps"`
	Data        *Draft     `json:"data"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type SaveRequest struct {
	Data Draft `json:"data"`
}
