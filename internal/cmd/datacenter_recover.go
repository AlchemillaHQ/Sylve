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
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/urfave/cli/v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

const offlineReaddressPhaseLocalRebound = "local_rebound"

type offlineClusterIPRecoveryResult struct {
	NodeID        string `json:"nodeId"`
	OldIP         string `json:"oldIp"`
	NewIP         string `json:"newIp"`
	Phase         string `json:"phase"`
	AlreadySet    bool   `json:"alreadySet"`
	RepairCommand string `json:"repairCommand"`
}

func newDatacenterClusterRecoverIPCommand() *cli.Command {
	return &cli.Command{
		Name:  "recover-ip",
		Usage: "Rebind this stopped clustered node to a new local IP",
		Flags: []cli.Flag{
			datacenterJSONFlag(),
			&cli.StringFlag{Name: "new-ip", Usage: "new locally assigned cluster IP", Required: true},
			clusterDisruptionFlag(),
		},
		Action: func(_ context.Context, command *cli.Command) error {
			result, err := recoverOfflineClusterIP(command.String("config"), command.String("new-ip"), command.Bool("allow-disruption"))
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
			fmt.Println(formatOfflineClusterIPRecovery(result))
			return nil
		},
	}
}

func recoverOfflineClusterIP(configPath string, requestedIP string, allowDisruption bool) (offlineClusterIPRecoveryResult, error) {
	if !allowDisruption {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_readdress_disruption_acknowledgement_required")
	}
	newIP, err := normalizeOfflineClusterIP(requestedIP)
	if err != nil {
		return offlineClusterIPRecoveryResult{}, err
	}
	resolvedConfigPath, err := ResolveConfigPath(configPath)
	if err != nil {
		return offlineClusterIPRecoveryResult{}, err
	}
	dataPath, err := config.DataPathFromConfig(resolvedConfigPath)
	if err != nil {
		return offlineClusterIPRecoveryResult{}, err
	}
	if err := requireOfflineConsole(consoleprotocol.SocketPath(dataPath)); err != nil {
		return offlineClusterIPRecoveryResult{}, err
	}
	if !offlineRaftStateExists(filepath.Join(dataPath, "raft")) {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_readdress_raft_state_missing")
	}
	if !utils.IsLocalIP(newIP) {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_readdress_ip_not_local")
	}
	for _, port := range []int{8180, 8183, 8184} {
		if err := network.TryBindToPort(newIP, port, "tcp"); err != nil {
			return offlineClusterIPRecoveryResult{}, fmt.Errorf(
				"cluster_readdress_port_unavailable: address=%s: %w", net.JoinHostPort(newIP, fmt.Sprint(port)), err,
			)
		}
	}
	databasePath := filepath.Join(dataPath, "sylve.db")
	info, err := os.Stat(databasePath)
	if err != nil {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_database_unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_database_invalid")
	}
	nodeID, err := utils.GetSystemUUID()
	if err != nil || strings.TrimSpace(nodeID) == "" {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("local_node_id_unavailable")
	}
	database, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Silent), TranslateError: true,
	})
	if err != nil {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_database_open_failed: %w", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		return offlineClusterIPRecoveryResult{}, err
	}
	sqlDB.SetMaxOpenConns(1)
	result, recoveryErr := applyOfflineClusterIPRecovery(database, strings.TrimSpace(nodeID), newIP)
	closeErr := sqlDB.Close()
	hardenErr := hardenOfflineDatabaseFiles(databasePath)
	if recoveryErr != nil {
		return offlineClusterIPRecoveryResult{}, recoveryErr
	}
	if closeErr != nil {
		return offlineClusterIPRecoveryResult{}, fmt.Errorf("cluster_database_close_failed: %w", closeErr)
	}
	if hardenErr != nil {
		return offlineClusterIPRecoveryResult{}, hardenErr
	}
	return result, nil
}

func applyOfflineClusterIPRecovery(
	database *gorm.DB,
	nodeID string,
	newIP string,
) (offlineClusterIPRecoveryResult, error) {
	result := offlineClusterIPRecoveryResult{NodeID: strings.TrimSpace(nodeID), NewIP: newIP}
	err := database.Transaction(func(tx *gorm.DB) error {
		var record clusterModels.Cluster
		if err := tx.First(&record).Error; err != nil {
			return err
		}
		if !record.Enabled {
			return fmt.Errorf("cluster_not_enabled")
		}
		if strings.TrimSpace(record.JoinPhase) != "" || strings.TrimSpace(record.LeavePhase) != "" {
			return fmt.Errorf("cluster_readdress_lifecycle_conflict")
		}
		phase := strings.TrimSpace(record.ReaddressPhase)
		if phase != "" && phase != "prepared" && phase != "membership_committed" && phase != offlineReaddressPhaseLocalRebound {
			return fmt.Errorf("cluster_readdress_phase_invalid: %s", phase)
		}
		if phase != "" && !sameOfflineClusterIP(record.ReaddressNewIP, newIP) {
			return fmt.Errorf(
				"cluster_readdress_already_active: old_ip=%s new_ip=%s phase=%s",
				record.ReaddressOldIP,
				record.ReaddressNewIP,
				phase,
			)
		}
		if phase == "" && sameOfflineClusterIP(record.RaftIP, newIP) {
			return fmt.Errorf("cluster_readdress_ip_unchanged")
		}
		result.OldIP = strings.TrimSpace(record.ReaddressOldIP)
		if result.OldIP == "" {
			result.OldIP = strings.TrimSpace(record.RaftIP)
		}
		result.Phase = offlineReaddressPhaseLocalRebound
		result.AlreadySet = phase == offlineReaddressPhaseLocalRebound && sameOfflineClusterIP(record.RaftIP, newIP)
		if result.AlreadySet {
			return nil
		}
		return tx.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
			"raft_ip":              newIP,
			"readdress_old_ip":     result.OldIP,
			"readdress_new_ip":     newIP,
			"readdress_phase":      offlineReaddressPhaseLocalRebound,
			"readdress_last_error": "",
		}).Error
	})
	if err != nil {
		return offlineClusterIPRecoveryResult{}, err
	}
	result.RepairCommand = fmt.Sprintf(
		"sylve datacenter cluster repair-address --node-id %s --new-ip %s --allow-disruption",
		result.NodeID,
		result.NewIP,
	)
	return result, nil
}

func requireOfflineConsole(socketPath string) error {
	connection, err := net.DialTimeout("unix", socketPath, 300*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		return fmt.Errorf("cluster_readdress_daemon_must_be_stopped")
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	if _, statErr := os.Lstat(socketPath); errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("cluster_readdress_daemon_state_unavailable: %w", err)
}

func offlineRaftStateExists(path string) bool {
	for _, name := range []string{"raft-log.db", "raft-stable.db", "snapshots"} {
		if _, err := os.Stat(filepath.Join(path, name)); err == nil {
			return true
		}
	}
	return false
}

func normalizeOfflineClusterIP(value string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return "", fmt.Errorf("cluster_readdress_ip_invalid")
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return "", fmt.Errorf("cluster_readdress_ipv6_unsupported")
	}
	return ipv4.String(), nil
}

func sameOfflineClusterIP(left string, right string) bool {
	leftIP := net.ParseIP(strings.TrimSpace(left))
	rightIP := net.ParseIP(strings.TrimSpace(right))
	return leftIP != nil && rightIP != nil && leftIP.Equal(rightIP)
}

func formatOfflineClusterIPRecovery(result offlineClusterIPRecoveryResult) string {
	status := "Local cluster IP recovery prepared."
	if result.AlreadySet {
		status = "Local cluster IP recovery is already prepared."
	}
	return strings.Join([]string{
		status,
		"Node ID: " + result.NodeID,
		"Old IP: " + result.OldIP,
		"New IP: " + result.NewIP,
		"Raft state and cluster resources were retained.",
		"The node will remain mutation-fenced until membership is repaired.",
		"Run on a surviving member:",
		result.RepairCommand,
	}, "\n")
}

func hardenOfflineDatabaseFiles(databasePath string) error {
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm", databasePath + "-journal"} {
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0o600); err != nil {
				return fmt.Errorf("cluster_database_permissions_failed: %w", err)
			}
		}
	}
	return nil
}
