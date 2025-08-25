package datacenterModels

import "time"

type DataCenterOptions struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	KeyboardLayout string    `json:"keyboardLayout"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}
