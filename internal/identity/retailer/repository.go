package retailer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrNotFound = errors.New("retailer profile not found")

type Repository interface {
	FindByUserID(ctx context.Context, userID uuid.UUID) (*Profile, error)
	UpsertProfile(ctx context.Context, userID uuid.UUID, legalName, ownerName string) (*Retailer, error)
	UpsertKYC(ctx context.Context, retailerID uuid.UUID, idProofURL, businessRegURL string) (*KYC, error)
	UpsertBank(ctx context.Context, retailerID uuid.UUID, accountNumber, ifsc, holder, qrURL string) (*BankDetails, error)
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindByUserID(ctx context.Context, userID uuid.UUID) (*Profile, error) {
	var row Retailer
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find retailer: %w", err)
	}

	profile := &Profile{Retailer: row}
	var kyc KYC
	if err := r.db.WithContext(ctx).Where("retailer_id = ?", row.ID).First(&kyc).Error; err == nil {
		profile.KYC = &kyc
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find retailer kyc: %w", err)
	}

	var bank BankDetails
	if err := r.db.WithContext(ctx).Where("retailer_id = ?", row.ID).First(&bank).Error; err == nil {
		profile.Bank = &bank
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find bank details: %w", err)
	}
	return profile, nil
}

func (r *repository) UpsertProfile(ctx context.Context, userID uuid.UUID, legalName, ownerName string) (*Retailer, error) {
	now := time.Now()
	var row Retailer
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = Retailer{
			ID:        uuid.New(),
			UserID:    userID,
			LegalName: legalName,
			OwnerName: ownerName,
			KYCStatus: KYCPending,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return nil, fmt.Errorf("create retailer: %w", err)
		}
		return &row, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find retailer: %w", err)
	}

	row.LegalName = legalName
	row.OwnerName = ownerName
	row.UpdatedAt = now
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return nil, fmt.Errorf("update retailer: %w", err)
	}
	return &row, nil
}

func (r *repository) UpsertKYC(ctx context.Context, retailerID uuid.UUID, idProofURL, businessRegURL string) (*KYC, error) {
	var row KYC
	err := r.db.WithContext(ctx).Where("retailer_id = ?", retailerID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = KYC{
			ID:             uuid.New(),
			RetailerID:     retailerID,
			IDProofURL:     idProofURL,
			BusinessRegURL: businessRegURL,
			Status:         KYCPending,
		}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return nil, fmt.Errorf("create retailer kyc: %w", err)
		}
		return &row, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find retailer kyc: %w", err)
	}

	row.IDProofURL = idProofURL
	row.BusinessRegURL = businessRegURL
	if row.Status == "" {
		row.Status = KYCPending
	}
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return nil, fmt.Errorf("update retailer kyc: %w", err)
	}
	return &row, nil
}

func (r *repository) UpsertBank(ctx context.Context, retailerID uuid.UUID, accountNumber, ifsc, holder, qrURL string) (*BankDetails, error) {
	now := time.Now()
	var row BankDetails
	err := r.db.WithContext(ctx).Where("retailer_id = ?", retailerID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = BankDetails{
			RetailerID:        retailerID,
			AccountNumber:     accountNumber,
			IFSC:              ifsc,
			AccountHolderName: holder,
			QRURL:             qrURL,
			UpdatedAt:         now,
		}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return nil, fmt.Errorf("create bank details: %w", err)
		}
		return &row, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find bank details: %w", err)
	}

	row.AccountNumber = accountNumber
	row.IFSC = ifsc
	row.AccountHolderName = holder
	row.QRURL = qrURL
	row.UpdatedAt = now
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return nil, fmt.Errorf("update bank details: %w", err)
	}
	return &row, nil
}
