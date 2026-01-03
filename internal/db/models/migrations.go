package models

import "time"

type Migrations struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"unique;not null"`
	AppliedAt time.Time `json:"appliedAt" gorm:"autoCreateTime"`
}
