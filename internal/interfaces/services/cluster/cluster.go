// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterServiceInterfaces

import (
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/hashicorp/raft"
)

type Detail struct {
	NodeID   string `json:"nodeId"`
	Hostname string `json:"hostname"`
	APIPort  int    `json:"apiPort"`
}

type RaftNode struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Suffrage string `json:"suffrage"`
	IsLeader bool   `json:"isLeader"`
	GuestIDs []uint `json:"guestIds"`
}

type ClusterDetails struct {
	Cluster       *clusterModels.Cluster `json:"cluster"`
	NodeID        string                 `json:"nodeId"`
	Nodes         []RaftNode             `json:"nodes"`
	LeaderID      string                 `json:"leaderId"`
	LeaderAddress string                 `json:"leaderAddress"`
	Partial       bool                   `json:"partial"`
}

type NodeResources struct {
	NodeUUID string                                `json:"nodeUUID"`
	Hostname string                                `json:"hostname"`
	Jails    []jailServiceInterfaces.SimpleList    `json:"jails"`
	VMs      []libvirtServiceInterfaces.SimpleList `json:"vms"`
}

type ClusterServiceInterface interface {
	Detail() *Detail
	InitRaft(fsm raft.FSM) error
	CreateCluster(ip string, port int, fsm raft.FSM) error
	SetupRaft(bootstrap bool, fsm raft.FSM) (*raft.Raft, error)
	GetClusterDetails() (*ClusterDetails, error)
	PopulateClusterNodes() error
}
