package network

import (
	networkServiceInterfaces "sylve/internal/interfaces/services/network"
	"sync"

	"gorm.io/gorm"
)

var _ networkServiceInterfaces.NetworkServiceInterface = (*Service)(nil)

type Service struct {
	DB        *gorm.DB
	syncMutex sync.Mutex
}

func NewNetworkService(db *gorm.DB) networkServiceInterfaces.NetworkServiceInterface {
	return &Service{
		DB: db,
	}
}
