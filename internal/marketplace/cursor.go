package marketplace

import (
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"
)

// maxCursorLength bounds what a client can make the server decode.
const maxCursorLength = 512

// cursor is the last row of a store products page. Pages are ordered by
// (lower(name), inventory_id), neither of which changes with stock, so a
// product whose stock or bucket changes between pages is neither skipped nor
// repeated.
type cursor struct {
	Name        string    `json:"n"`
	InventoryID uuid.UUID `json:"i"`
}

func (c cursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (*cursor, error) {
	if s == "" {
		return nil, nil
	}
	if len(s) > maxCursorLength {
		return nil, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.InventoryID == uuid.Nil {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}

// categoryCursor is the last product of a nearby category page. Pages are
// ordered by (lower(name), catalog_key), which stock changes never move.
type categoryCursor struct {
	Name       string `json:"n"`
	CatalogKey string `json:"k"`
}

func (c categoryCursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCategoryCursor(s string) (*categoryCursor, error) {
	if s == "" {
		return nil, nil
	}
	if len(s) > maxCursorLength {
		return nil, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c categoryCursor
	if err := json.Unmarshal(raw, &c); err != nil || !catalogKeyPattern.MatchString(c.CatalogKey) {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}
