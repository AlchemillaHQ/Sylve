package systemServiceInterfaces

import "github.com/alchemillahq/sylve/internal/db/models"

type InitializeRequest struct {
	Pools    []string                  `json:"pools" binding:"required"`
	Services []models.AvailableService `json:"services" binding:"required"`
}
