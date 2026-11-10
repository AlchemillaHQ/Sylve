// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"errors"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

func (s *Service) ensureClusterIdentity(ctx context.Context) (string, error) {
	id, err := clusterModels.ReadClusterID(s.DB.WithContext(ctx))
	if err != nil || id != "" {
		return id, err
	}
	if err := s.Propose(ctx, "cluster_identity", "initialize", clusterModels.ClusterIdentity{ID: uuid.NewString()}); err != nil {
		return "", err
	}
	id, err = clusterModels.ReadClusterID(s.DB.WithContext(ctx))
	if err == nil && id == "" {
		err = fmt.Errorf("cluster_identity_not_applied")
	}
	return id, err
}

// Same-format upgrades resume Raft without the founding snapshot that creates the ID.
func (s *Service) reconcileClusterIdentity(ctx context.Context) {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return
	}
	defer release()
	if r := s.Raft(); r == nil || r.State() != raft.Leader {
		return
	}
	if _, err := s.ensureClusterIdentity(ctx); err != nil && !errors.Is(err, ErrClusterProtocolOutdated) {
		logger.L.Warn().Err(err).Msg("cluster_identity_initialization_deferred")
	}
}
