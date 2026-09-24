package catalog

import (
	"github.com/google/uuid"
)

type Category struct {
	ID         uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	Name       string    `json:"name"`
	L0         string    `json:"l0" gorm:"column:l0"`
	L1         string    `json:"l1" gorm:"column:l1"`
	L2         string    `json:"l2" gorm:"column:l2"`
	TreeString string    `json:"tree_string"`
	Locked     bool      `json:"locked"`
}

func (Category) TableName() string { return "catalog_category" }
