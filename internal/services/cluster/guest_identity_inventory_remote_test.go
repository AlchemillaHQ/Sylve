// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/hashicorp/raft"
)

type guestIdentityInventoryAuthStub struct {
	serviceInterfaces.AuthServiceInterface
	err       error
	usernames []string
}

func (s *guestIdentityInventoryAuthStub) CreateInternalClusterJWT(username, _ string) (string, error) {
	s.usernames = append(s.usernames, username)
	if s.err != nil {
		return "", s.err
	}
	return "inventory-test-token", nil
}

func registerGuestIdentityInventoryPeer(
	t *testing.T,
	sim *clusterPeerSimulator,
	nodeID string,
	entries []GuestIdentityInventoryEntry,
) {
	t.Helper()
	report := BuildGuestIdentityInventoryReport(entries)
	sim.serveMux.HandleFunc("/api/intra-cluster/guest-identity-inventory", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(internal.APIResponse[GuestIdentityInventorySnapshot]{
			Status: "success",
			Data: GuestIdentityInventorySnapshot{
				NodeID: nodeID,
				Report: report,
			},
		})
	})
}

func remoteClusterRaftTestNode(
	t *testing.T,
	nodes []*clusterRaftTestNode,
	leader *clusterRaftTestNode,
) *clusterRaftTestNode {
	t.Helper()
	for _, node := range nodes {
		if node.id != leader.id {
			return node
		}
	}
	t.Fatal("remote raft test node not found")
	return nil
}

func TestCollectClusterGuestIdentityInventoriesStrict(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	remote := remoteClusterRaftTestNode(t, nodes, leader)
	leader.service.NodeID = leader.id
	auth := &guestIdentityInventoryAuthStub{}
	leader.service.AuthService = auth

	if err := leader.service.DB.Create(&vmModels.VM{RID: 10, Name: "local-vm"}).Error; err != nil {
		t.Fatalf("seed local VM: %v", err)
	}
	if err := leader.service.DB.Create(&jailModels.Jail{CTID: 20, Name: "local-jail"}).Error; err != nil {
		t.Fatalf("seed local jail: %v", err)
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(
		t,
		sim,
		remote.id,
		[]GuestIdentityInventoryEntry{
			{NodeID: remote.id, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 30, RecordID: 31, Name: "remote-vm"},
			{NodeID: remote.id, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 40, RecordID: 41, Name: "remote-jail"},
		},
	)
	leader.service.guestIdentityInventoryAPIForNode = func(
		nodeID string,
		_ raft.ServerAddress,
	) (string, error) {
		if nodeID != remote.id {
			return "", errors.New("unexpected remote node")
		}
		return sim.Addr(), nil
	}

	reports, combined, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
	if err != nil {
		t.Fatalf("collect strict inventories: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("per-node report count = %d, want 2", len(reports))
	}
	if got := reports[leader.id].Entries; len(got) != 2 || got[0].GuestID != 10 || got[1].GuestID != 20 {
		t.Fatalf("unexpected local report: %+v", reports[leader.id])
	}
	if got := reports[remote.id].Entries; len(got) != 2 || got[0].GuestID != 30 || got[1].GuestID != 40 {
		t.Fatalf("unexpected remote report: %+v", reports[remote.id])
	}
	if len(combined.Entries) != 4 || len(combined.Conflicts) != 0 {
		t.Fatalf("unexpected combined report: %+v", combined)
	}
	for index, guestID := range []uint{10, 20, 30, 40} {
		if combined.Entries[index].GuestID != guestID {
			t.Fatalf("combined entries are not canonical: %+v", combined.Entries)
		}
	}
	if len(auth.usernames) != 1 || auth.usernames[0] != leader.id {
		t.Fatalf("internal token usernames = %v, want [%s]", auth.usernames, leader.id)
	}
	if sim.NumRequests() != 1 {
		t.Fatalf("remote request count = %d, want 1", sim.NumRequests())
	}
	path := "/api/intra-cluster/guest-identity-inventory"
	request := sim.FindRequest(path)
	if request == nil {
		t.Fatalf("request %s not observed", path)
	}
	if got := request.Header.Get("X-Cluster-Token"); got != "Bearer inventory-test-token" {
		t.Fatalf("%s cluster token = %q", path, got)
	}
}

func TestRequireGuestIDsAvailableChecksRemoteVoterOncePerBatch(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		2,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	remote := remoteClusterRaftTestNode(t, nodes, leader)
	leader.service.NodeID = leader.id
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	if err := leader.service.DB.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatalf("seed clustered state: %v", err)
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(
		t,
		sim,
		remote.id,
		[]GuestIdentityInventoryEntry{{
			NodeID: remote.id, GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 703, RecordID: 1, Name: "remote-vm-703",
		}},
	)
	leader.service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
		return sim.Addr(), nil
	}

	if err := leader.service.RequireGuestIDsAvailable(t.Context(), []uint{704, 705}); err != nil {
		t.Fatalf("free clustered guest ID batch rejected: %v", err)
	}
	if sim.NumRequests() != 1 {
		t.Fatalf("remote inventory requests after first batch = %d, want 1", sim.NumRequests())
	}
	err := leader.service.RequireGuestIDsAvailable(t.Context(), []uint{704, 703, 705})
	if err == nil || !strings.Contains(err.Error(), "guest_id_already_in_use") {
		t.Fatalf("remote occupied guest ID error = %v", err)
	}
	if sim.NumRequests() != 2 {
		t.Fatalf("remote inventory requests = %d, want 2", sim.NumRequests())
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictBuildsCrossNodeConflict(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	remote := remoteClusterRaftTestNode(t, nodes, leader)
	leader.service.NodeID = leader.id
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	if err := leader.service.DB.Create(&vmModels.VM{RID: 100, Name: "local-vm-100"}).Error; err != nil {
		t.Fatalf("seed local VM: %v", err)
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(
		t,
		sim,
		remote.id,
		[]GuestIdentityInventoryEntry{{
			NodeID: remote.id, GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 100, RecordID: 1, Name: "remote-jail-100",
		}},
	)
	leader.service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
		return sim.Addr(), nil
	}

	_, combined, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
	if err != nil {
		t.Fatalf("collect strict inventories: %v", err)
	}
	if len(combined.Conflicts) != 1 ||
		combined.Conflicts[0].Reason != GuestIdentityInventoryConflictSharedGuestID ||
		len(combined.Conflicts[0].Entries) != 2 {
		t.Fatalf("unexpected cross-node conflicts: %+v", combined.Conflicts)
	}
	if combined.Conflicts[0].Entries[0].NodeID == combined.Conflicts[0].Entries[1].NodeID {
		t.Fatalf("conflict did not retain distinct node owners: %+v", combined.Conflicts[0])
	}
	if reports := combined.Conflicts[0].Entries; reports[0].GuestID != 100 || reports[1].GuestID != 100 {
		t.Fatalf("unexpected conflict entries: %+v", reports)
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictSingleVoterNeedsNoAuth(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	leader.service.NodeID = leader.id
	if err := leader.service.DB.Create(&vmModels.VM{RID: 55, Name: "single-node-vm"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	reports, combined, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
	if err != nil {
		t.Fatalf("single-voter collection: %v", err)
	}
	if len(reports) != 1 || len(combined.Entries) != 1 || combined.Entries[0].GuestID != 55 {
		t.Fatalf("unexpected single-voter reports: per-node=%+v combined=%+v", reports, combined)
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictFailsClosedOnRemoteResponses(t *testing.T) {
	tests := []struct {
		name      string
		response  func(http.ResponseWriter, *http.Request)
		wantError string
	}{
		{
			name: "http error",
			response: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			},
			wantError: "guest_identity_inventory_remote_request_failed",
		},
		{
			name: "decode error",
			response: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":`))
			},
			wantError: "guest_identity_inventory_remote_decode_failed",
		},
		{
			name: "non-success response",
			response: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"error","message":"nope","data":{},"error":"failed"}`))
			},
			wantError: "guest_identity_inventory_remote_non_success",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodes := setupClusterRaftTestNodes(t, 2, &vmModels.VM{}, &jailModels.Jail{})
			defer cleanupClusterRaftTestNodes(t, nodes)

			leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
			leader.service.NodeID = leader.id
			leader.service.AuthService = &guestIdentityInventoryAuthStub{}
			sim := newClusterPeerSimulator()
			defer sim.Close()
			sim.serveMux.HandleFunc("/api/intra-cluster/guest-identity-inventory", test.response)
			leader.service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
				return sim.Addr(), nil
			}

			reports, combined, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %s", err, test.wantError)
			}
			if reports != nil || len(combined.Entries) != 0 || combined.Digest != "" {
				t.Fatalf("partial result escaped on failure: reports=%+v combined=%+v", reports, combined)
			}
		})
	}
}

func TestFetchRemoteGuestIdentityInventoryRejectsUntrustedReport(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*GuestIdentityInventorySnapshot)
		wantError string
	}{
		{
			name: "response node mismatch",
			mutate: func(snapshot *GuestIdentityInventorySnapshot) {
				snapshot.NodeID = "different-node"
			},
			wantError: "guest_identity_inventory_remote_node_id_mismatch",
		},
		{
			name: "entry node mismatch",
			mutate: func(snapshot *GuestIdentityInventorySnapshot) {
				snapshot.Report.Entries[0].NodeID = "different-node"
			},
			wantError: "guest_identity_inventory_remote_entry_node_id_mismatch",
		},
		{
			name: "non-canonical entry order",
			mutate: func(snapshot *GuestIdentityInventorySnapshot) {
				snapshot.Report.Entries[0], snapshot.Report.Entries[1] =
					snapshot.Report.Entries[1], snapshot.Report.Entries[0]
			},
			wantError: "guest_identity_inventory_remote_entries_not_canonical",
		},
		{
			name: "digest mismatch",
			mutate: func(snapshot *GuestIdentityInventorySnapshot) {
				snapshot.Report.Digest = "wrong-digest"
			},
			wantError: "guest_identity_inventory_remote_digest_mismatch",
		},
		{
			name: "conflicts mismatch",
			mutate: func(snapshot *GuestIdentityInventorySnapshot) {
				snapshot.Report.Conflicts = append(snapshot.Report.Conflicts, GuestIdentityInventoryConflict{
					GuestID: 10,
					Reason:  GuestIdentityInventoryConflictSharedGuestID,
				})
			},
			wantError: "guest_identity_inventory_remote_conflicts_not_canonical",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
				{NodeID: "node-remote", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 10, RecordID: 1, Name: "vm-10"},
				{NodeID: "node-remote", GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 20, RecordID: 2, Name: "jail-20"},
			})
			snapshot := GuestIdentityInventorySnapshot{NodeID: "node-remote", Report: report}
			test.mutate(&snapshot)

			sim := newClusterPeerSimulator()
			defer sim.Close()
			sim.serveMux.HandleFunc("/api/intra-cluster/guest-identity-inventory", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(internal.APIResponse[GuestIdentityInventorySnapshot]{
					Status: "success",
					Data:   snapshot,
				})
			})

			service := &Service{}
			got, err := service.fetchRemoteGuestIdentityInventory(
				context.Background(), "node-remote", sim.Addr(), "token",
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %s", err, test.wantError)
			}
			if len(got.Entries) != 0 || got.Digest != "" {
				t.Fatalf("untrusted report escaped: %+v", got)
			}
			if sim.NumRequests() != 1 {
				t.Fatalf("request count = %d, want 1", sim.NumRequests())
			}
		})
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictRejectsNodeIDAmbiguity(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	leader.service.NodeID = "not-a-configured-voter"

	reports, combined, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
	if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_node_id_ambiguous") {
		t.Fatalf("error = %v, want local node ambiguity", err)
	}
	if reports != nil || combined.Digest != "" {
		t.Fatalf("unexpected result on ambiguity: reports=%+v combined=%+v", reports, combined)
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictRejectsSharedRemoteEndpoint(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	leader.service.NodeID = leader.id
	leader.service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
		return "127.0.0.1:65530", nil
	}

	_, _, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
	if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_node_id_ambiguous") {
		t.Fatalf("error = %v, want shared-endpoint ambiguity", err)
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictFailsClosedOnTokenError(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	leader.service.NodeID = leader.id
	leader.service.AuthService = &guestIdentityInventoryAuthStub{err: errors.New("token unavailable")}
	leader.service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
		return "127.0.0.1:65530", nil
	}

	_, _, err := leader.service.collectClusterGuestIdentityInventoriesStrict(context.Background())
	if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_cluster_token_failed") {
		t.Fatalf("error = %v, want token failure", err)
	}
}

func TestCollectClusterGuestIdentityInventoriesStrictHonorsCanceledContext(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, &vmModels.VM{}, &jailModels.Jail{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	leader.service.NodeID = leader.id
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := leader.service.collectClusterGuestIdentityInventoriesStrict(ctx)
	if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_collection_canceled") {
		t.Fatalf("error = %v, want canceled collection", err)
	}
}

func TestStrictGuestIdentityInventoryVotersRejectsNonVotersAndDuplicateCanonicalIDs(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "node-a", Address: "node-a", Suffrage: raft.Voter},
		{ID: "non-voter", Address: "non-voter", Suffrage: raft.Nonvoter},
	}}
	_, err := strictGuestIdentityInventoryVoters(configuration, "node-a")
	if err == nil || !strings.Contains(err.Error(), "non_voter_member_unsupported") {
		t.Fatalf("error = %v, want fail-closed non-voter rejection", err)
	}

	configuration = raft.Configuration{Servers: []raft.Server{
		{ID: "node-a", Address: "node-a", Suffrage: raft.Voter},
		{ID: " node-a ", Address: "duplicate", Suffrage: raft.Voter},
	}}
	_, err = strictGuestIdentityInventoryVoters(configuration, "node-a")
	if err == nil || !strings.Contains(err.Error(), "duplicate_voter_id") {
		t.Fatalf("error = %v, want duplicate canonical voter ID", err)
	}
}

func TestFetchRemoteGuestIdentityInventoryAcceptsEmptyTypedLists(t *testing.T) {
	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(t, sim, "node-empty", nil)

	service := &Service{}
	report, err := service.fetchRemoteGuestIdentityInventory(
		context.Background(),
		"node-empty",
		sim.Addr(),
		"token",
	)
	if err != nil {
		t.Fatalf("fetch empty inventory: %v", err)
	}
	if len(report.Entries) != 0 || len(report.Conflicts) != 0 || report.Digest == "" {
		t.Fatalf("unexpected empty inventory report: %+v", report)
	}
}
