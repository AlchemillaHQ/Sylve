// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/json"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"gorm.io/gorm"
)

func newTestClusterService(db *gorm.DB, nodeID string) *cluster.Service {
	service := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	service.NodeID = nodeID
	return service
}

func seedReplicationPolicy(db *gorm.DB, policy *clusterModels.ReplicationPolicy, targets []clusterModels.ReplicationPolicyTarget) error {
	fsm := clusterModels.NewFSMDispatcher(db)
	clusterModels.RegisterDefaultHandlers(fsm)
	data, err := json.Marshal(clusterModels.ReplicationPolicyPayload{Policy: *policy, Targets: targets})
	if err != nil {
		return err
	}
	cmd := clusterModels.Command{Type: "replication_policy", Action: "create", Data: data}
	if err := clusterModels.PrepareCommand(&cmd, time.Now()); err != nil {
		return err
	}
	return fsm.ApplyLocal(cmd)
}
