package clusterModels

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClusterOption struct {
	ID             uint      `gorm:"primaryKey;autoIncrement:false" json:"id"`
	KeyboardLayout string    `json:"keyboardLayout"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func upsertOption(db *gorm.DB, o *ClusterOption) error {
	if o.ID == 0 {
		o.ID = 1
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"keyboard_layout": o.KeyboardLayout,
			"updated_at":      time.Now(),
		}),
	}).Create(o).Error
}
