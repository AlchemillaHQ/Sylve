package info

import (
	"context"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
)

func (s *Service) GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error) {
	var basicSettings models.BasicSettings
	var pools []*gzfs.ZPool

	if err := s.DB.First(&basicSettings).Error; err != nil {
		return pools, err
	}

	for _, name := range basicSettings.Pools {
		pool, err := s.GZFS.Zpool.Get(ctx, name)
		if err != nil {
			return pools, err
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func (s *Service) GetDisksUsage() (zfsServiceInterfaces.SimpleZFSDiskUsage, error) {
	ctx := context.Background()

	pools, err := s.GetUsablePools(ctx)
	if err != nil {
		return zfsServiceInterfaces.SimpleZFSDiskUsage{}, err
	}

	var totalSize uint64
	var totalUsed uint64

	for _, pool := range pools {
		size := pool.Size
		used := pool.Alloc

		totalSize += size
		totalUsed += used
	}

	usage := float64(0)
	if totalSize > 0 {
		usage = (float64(totalUsed) / float64(totalSize)) * 100
	} else {
		usage = 0
	}

	return zfsServiceInterfaces.SimpleZFSDiskUsage{
		Total: float64(totalSize),
		Usage: usage,
	}, nil
}
