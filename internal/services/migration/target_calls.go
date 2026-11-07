// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
	"strconv"
	"strings"
	"time"

	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
)

// TargetDatasetRequest names a dataset a migration receives into on the target
// node. Writes carry the migration's token, and the target accepts them only
// while that migration targets it.
type TargetDatasetRequest struct {
	GuestType      string `json:"guestType"`
	GuestID        uint   `json:"guestId"`
	OperationToken string `json:"operationToken,omitempty"`
	Dataset        string `json:"dataset"`
}

type TargetDatasetInfo struct {
	Exists             bool     `json:"exists"`
	MigrationSnapshots []string `json:"migrationSnapshots"`
}

// Valid reports whether Dataset is a guest root the guest's migration receives
// into: pool/sylve/<jails|virtual-machines>/<id>, or a .merge-N staging
// sibling of a VM root.
func (req TargetDatasetRequest) Valid() bool {
	parsed, err := remoteexec.ParseZFSDataset(req.Dataset)
	parts := strings.Split(req.Dataset, "/")
	if err != nil || parsed.String() != req.Dataset || len(parts) != 4 || parts[1] != "sylve" || req.GuestID == 0 {
		return false
	}
	id := strconv.FormatUint(uint64(req.GuestID), 10)
	switch req.GuestType {
	case taskModels.GuestTypeVM:
		root, merge, merged := strings.Cut(parts[3], ".merge-")
		n, err := strconv.Atoi(merge)
		return parts[2] == "virtual-machines" && root == id && (!merged || err == nil && n > 0 && strconv.Itoa(n) == merge)
	case taskModels.GuestTypeJail:
		return parts[2] == "jails" && parts[3] == id
	}
	return false
}

func (s *Service) targetDataset(ctx context.Context, nodeID string, req TargetDatasetRequest) (TargetDatasetInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return cluster.CallNode[TargetDatasetRequest, TargetDatasetInfo](ctx, s.Cluster, nodeID, "migration-target-dataset", req)
}

// writeTargetDataset runs a write call on the target, such as destroying what
// an earlier attempt left. Destroying a large dataset can take minutes.
func (s *Service) writeTargetDataset(ctx context.Context, nodeID, name string, req TargetDatasetRequest) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, err := cluster.CallNode[TargetDatasetRequest, struct{}](ctx, s.Cluster, nodeID, name, req)
	return err
}
