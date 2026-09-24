package catalog

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

type Repository interface {
	List(ctx context.Context) ([]Category, error)
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) List(ctx context.Context) ([]Category, error) {
	var rows []Category
	err := r.db.WithContext(ctx).Where("locked = ?", false).Order("name").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	return rows, nil
}
