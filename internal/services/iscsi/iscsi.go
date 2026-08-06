package iscsi

import (
	"sync"

	iscsiServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/iscsi"
	"gorm.io/gorm"
)

var _ iscsiServiceInterfaces.ISCSIServiceInterface = (*Service)(nil)

type Service struct {
	DB *gorm.DB

	mutationMu sync.Mutex
}

func NewISCSIService(db *gorm.DB) iscsiServiceInterfaces.ISCSIServiceInterface {
	return &Service{DB: db}
}
