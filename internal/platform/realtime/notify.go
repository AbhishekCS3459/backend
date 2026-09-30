package realtime

import (
	"fmt"

	"gorm.io/gorm"
)

// Notify announces a change to topic on channel from inside tx. PostgreSQL
// delivers it only if tx commits, and once however often tx repeats it.
func Notify(tx *gorm.DB, channel, topic string) error {
	if err := tx.Exec(`SELECT pg_notify(?, ?)`, channel, topic).Error; err != nil {
		return fmt.Errorf("notify %s: %w", channel, err)
	}
	return nil
}
