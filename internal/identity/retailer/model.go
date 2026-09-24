package retailer

import (
	"time"

	"github.com/google/uuid"
)

const (
	KYCPending  = "PENDING"
	KYCApproved = "APPROVED"
	KYCRejected = "REJECTED"
)

type Retailer struct {
	ID        uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `json:"user_id" gorm:"type:uuid;uniqueIndex"`
	LegalName string    `json:"legal_name"`
	OwnerName string    `json:"owner_name"`
	KYCStatus string    `json:"kyc_status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Retailer) TableName() string { return "retailers" }

type KYC struct {
	ID             uuid.UUID  `json:"id" gorm:"type:uuid;primaryKey"`
	RetailerID     uuid.UUID  `json:"retailer_id" gorm:"type:uuid;uniqueIndex"`
	IDProofURL     string     `json:"id_proof_url" gorm:"column:id_proof_url"`
	BusinessRegURL string     `json:"business_reg_url" gorm:"column:business_reg_url"`
	Status         string     `json:"status"`
	ReviewedBy     *uuid.UUID `json:"reviewed_by,omitempty" gorm:"type:uuid"`
	ReviewedAt     *time.Time `json:"reviewed_at,omitempty"`
}

func (KYC) TableName() string { return "retailer_kyc" }

type BankDetails struct {
	RetailerID        uuid.UUID `json:"retailer_id" gorm:"type:uuid;primaryKey"`
	AccountNumber     string    `json:"account_number"`
	IFSC              string    `json:"ifsc"`
	AccountHolderName string    `json:"account_holder_name"`
	QRURL             string    `json:"qr_url" gorm:"column:qr_url"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (BankDetails) TableName() string { return "bank_details" }

type Profile struct {
	Retailer
	KYC  *KYC         `json:"kyc,omitempty"`
	Bank *BankDetails `json:"bank,omitempty"`
}

type UpsertProfileRequest struct {
	LegalName string `json:"legal_name" validate:"required,min=2,max=255"`
	OwnerName string `json:"owner_name" validate:"required,min=2,max=255"`
}

type UpsertKYCRequest struct {
	IDProofURL     string `json:"id_proof_url" validate:"required,url,max=255"`
	BusinessRegURL string `json:"business_reg_url" validate:"required,url,max=255"`
}

type UpsertBankRequest struct {
	Method            string `json:"method" validate:"required,oneof=bank qr"`
	AccountNumber     string `json:"account_number"`
	IFSC              string `json:"ifsc"`
	AccountHolderName string `json:"account_holder_name"`
	QRURL             string `json:"qr_url"`
}
