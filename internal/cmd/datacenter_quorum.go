// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

type offlineQuorumRecoveryResult struct {
	NodeID  string   `json:"nodeId"`
	Voters  []string `json:"voters"`
	Removed []string `json:"removed"`
}

func newDatacenterClusterRecoverQuorumCommand() *cli.Command {
	return &cli.Command{
		Name:  "recover-quorum",
		Usage: "Rebuild Raft membership on this stopped node from the surviving voters",
		Description: "Use only when the other voters are gone for good and the cluster has no leader. " +
			"Run it on every surviving node with the same --voter list, then start Sylve on them. " +
			"Recovering both sides of a network split separately creates two clusters.",
		Flags: []cli.Flag{
			datacenterJSONFlag(),
			&cli.StringSliceFlag{Name: "voter", Usage: "Node ID of a surviving voter, this node included; repeat for each", Required: true},
			clusterDisruptionFlag(),
		},
		Action: func(_ context.Context, command *cli.Command) error {
			result, err := recoverOfflineQuorum(command.String("config"), command.StringSlice("voter"), command.Bool("allow-disruption"))
			if err != nil {
				printConsoleOperationError(command.Bool("json"), err)
				return err
			}
			if command.Bool("json") {
				encoded, err := json.Marshal(result)
				if err != nil {
					return err
				}
				fmt.Println(string(encoded))
				return nil
			}
			fmt.Printf(
				"Raft membership rebuilt with voters %s; removed %s.\n"+
					"Run the same command on every other surviving voter, then start Sylve on them.\n",
				strings.Join(result.Voters, ", "),
				strings.Join(result.Removed, ", "),
			)
			return nil
		},
	}
}

func recoverOfflineQuorum(configPath string, voters []string, allowDisruption bool) (offlineQuorumRecoveryResult, error) {
	if !allowDisruption {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("cluster_recover_quorum_disruption_acknowledgement_required")
	}
	resolvedConfigPath, err := ResolveConfigPath(configPath)
	if err != nil {
		return offlineQuorumRecoveryResult{}, err
	}
	dataPath, err := config.DataPathFromConfig(resolvedConfigPath)
	if err != nil {
		return offlineQuorumRecoveryResult{}, err
	}
	if err := requireOfflineConsole(consoleprotocol.SocketPath(dataPath)); err != nil {
		return offlineQuorumRecoveryResult{}, err
	}
	raftDir := filepath.Join(dataPath, "raft")
	if !offlineRaftStateExists(raftDir) {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("cluster_recover_quorum_raft_state_missing")
	}
	nodeID, err := utils.GetSystemUUID()
	if err != nil || strings.TrimSpace(nodeID) == "" {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("local_node_id_unavailable")
	}
	database, closeDatabase, err := openOfflineClusterDatabase(dataPath)
	if err != nil {
		return offlineQuorumRecoveryResult{}, err
	}
	result, recoveryErr := recoverRaftQuorum(database, raftDir, strings.TrimSpace(nodeID), voters)
	closeErr := closeDatabase()
	if recoveryErr != nil {
		return offlineQuorumRecoveryResult{}, recoveryErr
	}
	if closeErr != nil {
		return offlineQuorumRecoveryResult{}, closeErr
	}
	return result, nil
}

// recoverRaftQuorum rewrites the Raft membership in raftDir to the listed
// voters. raft.RecoverCluster replays the log into the FSM, so this node's
// database ends up current, then snapshots it with the new membership.
func recoverRaftQuorum(database *gorm.DB, raftDir, nodeID string, voters []string) (offlineQuorumRecoveryResult, error) {
	var record clusterModels.Cluster
	if err := database.First(&record).Error; err != nil {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("cluster_record_unavailable: %w", err)
	}
	if !record.Enabled {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("cluster_not_enabled")
	}
	if strings.TrimSpace(record.LeavePhase) != "" {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("cluster_recover_quorum_leave_in_progress")
	}

	logs, err := raftboltdb.NewBoltStore(filepath.Join(raftDir, "raft-log.db"))
	if err != nil {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("raft_log_store_open_failed: %w", err)
	}
	defer logs.Close()
	stable, err := raftboltdb.NewBoltStore(filepath.Join(raftDir, "raft-stable.db"))
	if err != nil {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("raft_stable_store_open_failed: %w", err)
	}
	defer stable.Close()
	snapshots, err := raft.NewFileSnapshotStore(raftDir, 2, io.Discard)
	if err != nil {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("raft_snapshot_store_open_failed: %w", err)
	}

	current, err := latestOfflineRaftConfiguration(logs, snapshots)
	if err != nil {
		return offlineQuorumRecoveryResult{}, err
	}
	recovered, result, err := survivingRaftConfiguration(current, nodeID, voters)
	if err != nil {
		return offlineQuorumRecoveryResult{}, err
	}

	conf := raft.DefaultConfig()
	conf.LocalID = raft.ServerID(nodeID)
	conf.Logger = hclog.NewNullLogger()
	fsm := clusterModels.NewFSMDispatcher(database)
	clusterModels.RegisterDefaultHandlers(fsm)
	_, transport := raft.NewInmemTransport("")
	if err := raft.RecoverCluster(conf, fsm, logs, stable, snapshots, transport, recovered); err != nil {
		return offlineQuorumRecoveryResult{}, fmt.Errorf("raft_recover_cluster_failed: %w", err)
	}
	return result, nil
}

// latestOfflineRaftConfiguration finds the membership Raft would load on
// start: the newest snapshot's, then any configuration entries after it.
func latestOfflineRaftConfiguration(logs raft.LogStore, snapshots raft.SnapshotStore) (raft.Configuration, error) {
	var configuration raft.Configuration
	after := uint64(0)
	metas, err := snapshots.List()
	if err != nil {
		return configuration, fmt.Errorf("raft_snapshot_list_failed: %w", err)
	}
	if len(metas) > 0 {
		configuration, after = metas[0].Configuration, metas[0].Index
	}
	last, err := logs.LastIndex()
	if err != nil {
		return configuration, fmt.Errorf("raft_log_index_failed: %w", err)
	}
	for index := after + 1; index <= last; index++ {
		var entry raft.Log
		if err := logs.GetLog(index, &entry); err != nil {
			if errors.Is(err, raft.ErrLogNotFound) {
				continue
			}
			return configuration, fmt.Errorf("raft_log_read_failed: %w", err)
		}
		if entry.Type == raft.LogConfiguration {
			configuration = raft.DecodeConfiguration(entry.Data)
		}
	}
	return configuration, nil
}

func survivingRaftConfiguration(
	current raft.Configuration,
	nodeID string,
	voters []string,
) (raft.Configuration, offlineQuorumRecoveryResult, error) {
	result := offlineQuorumRecoveryResult{NodeID: nodeID}
	wanted := map[string]bool{}
	for _, voter := range voters {
		if voter = strings.TrimSpace(voter); voter != "" {
			wanted[voter] = true
		}
	}
	if !wanted[nodeID] {
		return raft.Configuration{}, result, fmt.Errorf("cluster_recover_quorum_local_node_not_listed: %s", nodeID)
	}

	var recovered raft.Configuration
	for _, server := range current.Servers {
		id := string(server.ID)
		if !wanted[id] {
			result.Removed = append(result.Removed, id)
			continue
		}
		if server.Suffrage != raft.Voter {
			return raft.Configuration{}, result, fmt.Errorf("cluster_recover_quorum_not_a_voter: %s", id)
		}
		recovered.Servers = append(recovered.Servers, server)
		result.Voters = append(result.Voters, id)
		delete(wanted, id)
	}
	if len(wanted) > 0 {
		unknown := make([]string, 0, len(wanted))
		for id := range wanted {
			unknown = append(unknown, id)
		}
		slices.Sort(unknown)
		return raft.Configuration{}, result, fmt.Errorf("cluster_recover_quorum_unknown_voters: %s", strings.Join(unknown, ","))
	}
	if len(result.Removed) == 0 {
		return raft.Configuration{}, result, fmt.Errorf("cluster_recover_quorum_nothing_to_remove")
	}
	return recovered, result, nil
}
