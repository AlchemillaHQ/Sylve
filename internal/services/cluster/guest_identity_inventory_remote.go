// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

type guestIdentityInventoryVoter struct {
	nodeID  string
	address raft.ServerAddress
}

func (s *Service) guestIdentityInventoryLocalNodeID() string {
	if s == nil {
		return ""
	}
	if nodeID := strings.TrimSpace(s.NodeID); nodeID != "" {
		return nodeID
	}
	return strings.TrimSpace(s.LocalNodeID())
}

func strictGuestIdentityInventoryVoters(
	configuration raft.Configuration,
	localNodeID string,
) ([]guestIdentityInventoryVoter, error) {
	localNodeID = strings.TrimSpace(localNodeID)
	if localNodeID == "" {
		return nil, fmt.Errorf("guest_identity_inventory_local_node_id_unavailable")
	}

	voters := make([]guestIdentityInventoryVoter, 0, len(configuration.Servers))
	seenNodeIDs := make(map[string]struct{}, len(configuration.Servers))
	localMatches := 0

	for _, server := range configuration.Servers {
		if server.Suffrage != raft.Voter {
			return nil, fmt.Errorf(
				"guest_identity_inventory_non_voter_member_unsupported: node_id=%s suffrage=%d",
				strings.TrimSpace(string(server.ID)),
				server.Suffrage,
			)
		}

		nodeID := strings.TrimSpace(string(server.ID))
		if nodeID == "" {
			return nil, fmt.Errorf("guest_identity_inventory_node_id_ambiguous: empty_voter_id")
		}
		if _, exists := seenNodeIDs[nodeID]; exists {
			return nil, fmt.Errorf(
				"guest_identity_inventory_node_id_ambiguous: duplicate_voter_id=%s",
				nodeID,
			)
		}
		seenNodeIDs[nodeID] = struct{}{}

		if nodeID == localNodeID {
			localMatches++
		}
		voters = append(voters, guestIdentityInventoryVoter{
			nodeID:  nodeID,
			address: server.Address,
		})
	}

	if len(voters) == 0 {
		return nil, fmt.Errorf("guest_identity_inventory_raft_voters_unavailable")
	}
	if localMatches != 1 {
		return nil, fmt.Errorf(
			"guest_identity_inventory_node_id_ambiguous: local_node_id=%s matches=%d",
			localNodeID,
			localMatches,
		)
	}

	sort.Slice(voters, func(i, j int) bool {
		return voters[i].nodeID < voters[j].nodeID
	})
	return voters, nil
}

func normalizeGuestIdentityInventoryAPIEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(endpoint), "/"))
	if endpoint == "" || strings.Contains(endpoint, "://") {
		return "", fmt.Errorf("guest_identity_inventory_remote_api_invalid")
	}

	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return "", fmt.Errorf("guest_identity_inventory_remote_api_invalid")
	}
	return net.JoinHostPort(strings.TrimSpace(host), strings.TrimSpace(port)), nil
}

func (s *Service) guestIdentityInventoryRemoteAPI(
	nodeID string,
	address raft.ServerAddress,
) (string, error) {
	var endpoint string
	var err error
	if s.guestIdentityInventoryAPIForNode != nil {
		endpoint, err = s.guestIdentityInventoryAPIForNode(nodeID, address)
		if err != nil {
			return "", fmt.Errorf(
				"guest_identity_inventory_remote_api_resolve_failed: node_id=%s: %w",
				nodeID,
				err,
			)
		}
	} else {
		host := strings.TrimSpace(raftAddressHost(string(address)))
		if host == "" {
			return "", fmt.Errorf(
				"guest_identity_inventory_remote_api_resolve_failed: node_id=%s: empty_raft_address",
				nodeID,
			)
		}
		endpoint = net.JoinHostPort(host, fmt.Sprintf("%d", ClusterEmbeddedHTTPSPort))
	}

	endpoint, err = normalizeGuestIdentityInventoryAPIEndpoint(endpoint)
	if err != nil {
		return "", fmt.Errorf(
			"guest_identity_inventory_remote_api_resolve_failed: node_id=%s: %w",
			nodeID,
			err,
		)
	}
	return endpoint, nil
}

func (s *Service) fetchRemoteGuestIdentityInventory(
	ctx context.Context,
	nodeID, endpoint, clusterToken string,
) (GuestIdentityInventoryReport, error) {
	if err := ctx.Err(); err != nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_collection_canceled: %w", err)
	}

	body, statusCode, err := utils.HTTPGetJSONReadContext(
		ctx,
		fmt.Sprintf("https://%s/api/intra-cluster/guest-identity-inventory", endpoint),
		map[string]string{
			"Accept":          "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)
	if err != nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_request_failed: node_id=%s status=%d: %w",
			nodeID,
			statusCode,
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_collection_canceled: %w", err)
	}

	var response internal.APIResponse[GuestIdentityInventorySnapshot]
	if err := json.Unmarshal(body, &response); err != nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_decode_failed: node_id=%s: %w",
			nodeID,
			err,
		)
	}
	if strings.ToLower(strings.TrimSpace(response.Status)) != "success" {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_non_success: node_id=%s status=%s message=%s error=%s",
			nodeID,
			strings.TrimSpace(response.Status),
			strings.TrimSpace(response.Message),
			strings.TrimSpace(response.Error),
		)
	}

	nodeID = strings.TrimSpace(nodeID)
	if response.Data.NodeID != nodeID {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_node_id_mismatch: expected=%s actual=%s",
			nodeID,
			response.Data.NodeID,
		)
	}

	report := response.Data.Report
	for _, entry := range report.Entries {
		if entry.NodeID != nodeID {
			return GuestIdentityInventoryReport{}, fmt.Errorf(
				"guest_identity_inventory_remote_entry_node_id_mismatch: expected=%s actual=%s guest_id=%d",
				nodeID,
				entry.NodeID,
				entry.GuestID,
			)
		}
	}

	canonical := BuildGuestIdentityInventoryReport(report.Entries)
	if !reflect.DeepEqual(report.Entries, canonical.Entries) {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_entries_not_canonical: node_id=%s",
			nodeID,
		)
	}
	if report.Digest != canonical.Digest {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_digest_mismatch: node_id=%s",
			nodeID,
		)
	}
	if !reflect.DeepEqual(report.Conflicts, canonical.Conflicts) {
		return GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_remote_conflicts_not_canonical: node_id=%s",
			nodeID,
		)
	}

	return canonical, nil
}

// collectClusterGuestIdentityInventoriesStrict reads the current Raft voter
// set, scans this node's durable registrations directly, and obtains every
// remote voter's typed inventory through its authenticated API. Transport,
// decoding, response-status, and node-identity ambiguity errors abort the
// entire collection; callers never receive a partial inventory as success.
func (s *Service) collectClusterGuestIdentityInventoriesStrict(
	ctx context.Context,
) (map[string]GuestIdentityInventoryReport, GuestIdentityInventoryReport, error) {
	if s == nil || s.DB == nil {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_collection_canceled: %w",
			err,
		)
	}
	if s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_raft_unavailable")
	}

	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_raft_configuration_failed: %w",
			err,
		)
	}

	localNodeID := s.guestIdentityInventoryLocalNodeID()
	voters, err := strictGuestIdentityInventoryVoters(
		configurationFuture.Configuration(),
		localNodeID,
	)
	if err != nil {
		return nil, GuestIdentityInventoryReport{}, err
	}

	localReport, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), localNodeID)
	if err != nil {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_local_scan_failed: node_id=%s: %w",
			localNodeID,
			err,
		)
	}

	reports := make(map[string]GuestIdentityInventoryReport, len(voters))
	reports[localNodeID] = localReport
	combinedEntries := append([]GuestIdentityInventoryEntry(nil), localReport.Entries...)

	remoteVoters := make([]guestIdentityInventoryVoter, 0, len(voters)-1)
	remoteEndpoints := make(map[string]string, len(voters)-1)
	endpointOwners := make(map[string]string, len(voters)-1)
	for _, voter := range voters {
		if voter.nodeID == localNodeID {
			continue
		}
		endpoint, err := s.guestIdentityInventoryRemoteAPI(voter.nodeID, voter.address)
		if err != nil {
			return nil, GuestIdentityInventoryReport{}, err
		}
		if owner, exists := endpointOwners[endpoint]; exists {
			return nil, GuestIdentityInventoryReport{}, fmt.Errorf(
				"guest_identity_inventory_node_id_ambiguous: endpoint=%s node_ids=%s,%s",
				endpoint,
				owner,
				voter.nodeID,
			)
		}
		endpointOwners[endpoint] = voter.nodeID
		remoteEndpoints[voter.nodeID] = endpoint
		remoteVoters = append(remoteVoters, voter)
	}

	if len(remoteVoters) == 0 {
		return reports, BuildGuestIdentityInventoryReport(combinedEntries), nil
	}
	if s.AuthService == nil {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_auth_service_unavailable")
	}
	clusterToken, err := s.AuthService.CreateInternalClusterJWT(localNodeID, "")
	if err != nil {
		return nil, GuestIdentityInventoryReport{}, fmt.Errorf(
			"guest_identity_inventory_cluster_token_failed: %w",
			err,
		)
	}

	for _, voter := range remoteVoters {
		if err := ctx.Err(); err != nil {
			return nil, GuestIdentityInventoryReport{}, fmt.Errorf(
				"guest_identity_inventory_collection_canceled: %w",
				err,
			)
		}
		report, err := s.fetchRemoteGuestIdentityInventory(
			ctx,
			voter.nodeID,
			remoteEndpoints[voter.nodeID],
			clusterToken,
		)
		if err != nil {
			return nil, GuestIdentityInventoryReport{}, err
		}
		reports[voter.nodeID] = report
		combinedEntries = append(combinedEntries, report.Entries...)
	}

	return reports, BuildGuestIdentityInventoryReport(combinedEntries), nil
}
