package progress

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrNotFound = errors.New("user progress not found")

type Repository interface {
	FindByUserID(ctx context.Context, userID uuid.UUID) (*Record, error)
	Save(ctx context.Context, userID uuid.UUID, status string, payload []byte) (*Record, error)
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindByUserID(ctx context.Context, userID uuid.UUID) (*Record, error) {
	var row Record
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user progress: %w", err)
	}
	return &row, nil
}

func (r *repository) Save(ctx context.Context, userID uuid.UUID, status string, payload []byte) (*Record, error) {
	now := time.Now().UTC()
	row := Record{
		ID:        uuid.New(),
		UserID:    userID,
		Status:    status,
		Payload:   JSONB(payload),
		CreatedAt: now,
		UpdatedAt: now,
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "payload", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return nil, fmt.Errorf("save user progress: %w", err)
	}
	return r.FindByUserID(ctx, userID)
}
