package retailer

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrInvalidPayout = errors.New("add bank details or upload a payment QR")

type Service interface {
	GetMine(ctx context.Context, userID uuid.UUID) (*Profile, error)
	SaveProfile(ctx context.Context, userID uuid.UUID, req *UpsertProfileRequest) (*Profile, error)
	SaveKYC(ctx context.Context, userID uuid.UUID, req *UpsertKYCRequest) (*Profile, error)
	SaveBank(ctx context.Context, userID uuid.UUID, req *UpsertBankRequest) (*Profile, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) GetMine(ctx context.Context, userID uuid.UUID) (*Profile, error) {
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) SaveProfile(ctx context.Context, userID uuid.UUID, req *UpsertProfileRequest) (*Profile, error) {
	if _, err := s.repo.UpsertProfile(ctx, userID, strings.TrimSpace(req.LegalName), strings.TrimSpace(req.OwnerName)); err != nil {
		return nil, err
	}
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) SaveKYC(ctx context.Context, userID uuid.UUID, req *UpsertKYCRequest) (*Profile, error) {
	profile, err := s.repo.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.UpsertKYC(ctx, profile.ID, strings.TrimSpace(req.IDProofURL), strings.TrimSpace(req.BusinessRegURL)); err != nil {
		return nil, err
	}
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) SaveBank(ctx context.Context, userID uuid.UUID, req *UpsertBankRequest) (*Profile, error) {
	profile, err := s.repo.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	accountNumber := strings.TrimSpace(req.AccountNumber)
	ifsc := strings.ToUpper(strings.TrimSpace(req.IFSC))
	holder := strings.TrimSpace(req.AccountHolderName)
	qrURL := strings.TrimSpace(req.QRURL)

	switch req.Method {
	case "bank":
		if len(accountNumber) < 6 || len(ifsc) < 4 || len(holder) < 2 {
			return nil, ErrInvalidPayout
		}
		qrURL = ""
	case "qr":
		if qrURL == "" {
			return nil, ErrInvalidPayout
		}
		accountNumber, ifsc, holder = "", "", ""
	default:
		return nil, ErrInvalidPayout
	}

	if _, err := s.repo.UpsertBank(ctx, profile.ID, accountNumber, ifsc, holder, qrURL); err != nil {
		return nil, err
	}
	return s.repo.FindByUserID(ctx, userID)
}
