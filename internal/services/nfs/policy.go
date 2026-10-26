// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

type savedPolicy struct {
	config  nfsModels.NFSConfig
	shares  []nfsModels.NFSShare
	pools   []string
	enabled bool
}

func (s *Service) readPolicy(ctx context.Context) (savedPolicy, error) {
	if err := ctx.Err(); err != nil {
		return savedPolicy{}, err
	}
	var basic models.BasicSettings
	if err := s.DB.WithContext(ctx).First(&basic).Error; err != nil {
		return savedPolicy{}, err
	}
	config, err := s.GetConfig(ctx)
	if err != nil {
		return savedPolicy{}, err
	}
	shares, err := s.GetShares(ctx)
	return savedPolicy{config: config, shares: shares, pools: basic.Pools, enabled: slices.Contains(basic.Services, models.NFS)}, err
}

func (p savedPolicy) hash() string {
	type rule struct {
		ID     uint
		Config nfsModels.NFSShareConfig
	}
	shares := make([]rule, 0, len(p.shares))
	for _, share := range p.shares {
		if share.Enabled {
			shares = append(shares, rule{share.ID, sharePolicy(share.NFSShareConfig)})
		}
	}
	data, _ := json.Marshal(struct {
		Config nfsModels.NFSConfig
		Shares []rule
	}{p.config, shares})
	return exportsHash(string(data))
}

func sharePolicy(share nfsModels.NFSShareConfig) nfsModels.NFSShareConfig {
	share.Description = ""
	return share
}
