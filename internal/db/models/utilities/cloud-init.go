package utilitiesModels

import "time"

type CloudInitTemplate struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `json:"name" gorm:"uniqueIndex;"`
	User string `json:"user"`
	Meta string `json:"meta"`
	NetworkConfig string `json:"networkConfig"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}
