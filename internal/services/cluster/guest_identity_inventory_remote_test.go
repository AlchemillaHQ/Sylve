// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

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

func (s *guestIdentityInventoryAuthStub) CreateInternalClusterJWT(username string) (string, error) {
	s.usernames = append(s.usernames, username)
	if s.err != nil {
		return "", s.err
	}
	return "inventory-test-token", nil
}

func (s *guestIdentityInventoryAuthStub) CreateUserProxyJWT(_ uint, _ string, _ string) (string, error) {
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

func guestIdentityInventoryTestConfiguration(nodeIDs ...string) raft.Configuration {
	servers := make([]raft.Server, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		servers = append(servers, raft.Server{
			ID:       raft.ServerID(nodeID),
			Address:  raft.ServerAddress(nodeID + ":7000"),
			Suffrage: raft.Voter,
		})
	}
	return raft.Configuration{Servers: servers}
}

func newGuestIdentityInventoryTestService(t *testing.T, nodeID string) *Service {
	t.Helper()
	return &Service{
		DB:     newClusterServiceTestDB(t, &vmModels.VM{}, &jailModels.Jail{}),
		NodeID: nodeID,
	}
}

func TestCollectClusterGuestIdentityInventoriesFromConfiguration(t *testing.T) {
	t.Run("combines voter inventories", func(t *testing.T) {
		service := newGuestIdentityInventoryTestService(t, "node-local")
		if err := service.DB.Create(&vmModels.VM{RID: 100, Name: "local-vm"}).Error; err != nil {
			t.Fatalf("seed local VM: %v", err)
		}

		sim := newClusterPeerSimulator()
		defer sim.Close()
		registerGuestIdentityInventoryPeer(t, sim, "node-remote", []GuestIdentityInventoryEntry{{
			NodeID: "node-remote", GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 100, RecordID: 1, Name: "remote-jail",
		}})
		auth := &guestIdentityInventoryAuthStub{}
		service.AuthService = auth
		service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
			return sim.Addr(), nil
		}

		reports, combined, err := service.collectClusterGuestIdentityInventoriesFromConfiguration(
			context.Background(),
			guestIdentityInventoryTestConfiguration("node-local", "node-remote"),
		)
		if err != nil {
			t.Fatalf("collect inventories: %v", err)
		}
		if len(reports) != 2 || len(combined.Entries) != 2 {
			t.Fatalf("unexpected inventories: reports=%+v combined=%+v", reports, combined)
		}
		if len(combined.Conflicts) != 1 || combined.Conflicts[0].Reason != GuestIdentityInventoryConflictSharedGuestID {
			t.Fatalf("cross-node conflict = %+v", combined.Conflicts)
		}
		if sim.NumRequests() != 1 || len(auth.usernames) != 1 || auth.usernames[0] != "node-local" {
			t.Fatalf("requests=%d token users=%v, want one authenticated request", sim.NumRequests(), auth.usernames)
		}
	})

	t.Run("single voter needs no auth", func(t *testing.T) {
		service := newGuestIdentityInventoryTestService(t, "node-local")
		if err := service.DB.Create(&vmModels.VM{RID: 55, Name: "single-node-vm"}).Error; err != nil {
			t.Fatalf("seed local VM: %v", err)
		}

		reports, combined, err := service.collectClusterGuestIdentityInventoriesFromConfiguration(
			context.Background(),
			guestIdentityInventoryTestConfiguration("node-local"),
		)
		if err != nil {
			t.Fatalf("collect single-voter inventory: %v", err)
		}
		if len(reports) != 1 || len(combined.Entries) != 1 || combined.Entries[0].GuestID != 55 {
			t.Fatalf("unexpected inventory: reports=%+v combined=%+v", reports, combined)
		}
	})
}

func TestCollectClusterGuestIdentityInventoriesFromConfigurationFailsClosed(t *testing.T) {
	responses := []struct {
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
				_, _ = w.Write([]byte(`{"status":`))
			},
			wantError: "guest_identity_inventory_remote_decode_failed",
		},
		{
			name: "non-success response",
			response: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":"error","message":"nope","data":{},"error":"failed"}`))
			},
			wantError: "guest_identity_inventory_remote_non_success",
		},
	}

	for _, test := range responses {
		t.Run(test.name, func(t *testing.T) {
			service := newGuestIdentityInventoryTestService(t, "node-local")
			service.AuthService = &guestIdentityInventoryAuthStub{}
			sim := newClusterPeerSimulator()
			defer sim.Close()
			sim.serveMux.HandleFunc("/api/intra-cluster/guest-identity-inventory", test.response)
			service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
				return sim.Addr(), nil
			}

			reports, combined, err := service.collectClusterGuestIdentityInventoriesFromConfiguration(
				context.Background(),
				guestIdentityInventoryTestConfiguration("node-local", "node-remote"),
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %s", err, test.wantError)
			}
			if reports != nil || combined.Digest != "" {
				t.Fatalf("partial inventory escaped: reports=%+v combined=%+v", reports, combined)
			}
		})
	}

	t.Run("token error", func(t *testing.T) {
		service := newGuestIdentityInventoryTestService(t, "node-local")
		service.AuthService = &guestIdentityInventoryAuthStub{err: errors.New("token unavailable")}
		service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
			return "127.0.0.1:65530", nil
		}
		_, _, err := service.collectClusterGuestIdentityInventoriesFromConfiguration(
			context.Background(),
			guestIdentityInventoryTestConfiguration("node-local", "node-remote"),
		)
		if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_cluster_token_failed") {
			t.Fatalf("error = %v, want token failure", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		service := newGuestIdentityInventoryTestService(t, "node-local")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := service.collectClusterGuestIdentityInventoriesFromConfiguration(
			ctx,
			guestIdentityInventoryTestConfiguration("node-local"),
		)
		if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_collection_canceled") {
			t.Fatalf("error = %v, want canceled collection", err)
		}
	})

	t.Run("ambiguous topology", func(t *testing.T) {
		service := newGuestIdentityInventoryTestService(t, "not-a-voter")
		_, _, err := service.collectClusterGuestIdentityInventoriesFromConfiguration(
			context.Background(),
			guestIdentityInventoryTestConfiguration("node-a", "node-b"),
		)
		if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_node_id_ambiguous") {
			t.Fatalf("error = %v, want node ambiguity", err)
		}

		service.NodeID = "node-a"
		service.guestIdentityInventoryAPIForNode = func(string, raft.ServerAddress) (string, error) {
			return "127.0.0.1:65530", nil
		}
		_, _, err = service.collectClusterGuestIdentityInventoriesFromConfiguration(
			context.Background(),
			guestIdentityInventoryTestConfiguration("node-a", "node-b", "node-c"),
		)
		if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_node_id_ambiguous") {
			t.Fatalf("error = %v, want shared endpoint ambiguity", err)
		}
	})
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

func TestStrictGuestIdentityInventoryVotersSkipsNonVotersAndRejectsDuplicateCanonicalIDs(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "node-a", Address: "node-a", Suffrage: raft.Voter},
		{ID: "non-voter", Address: "non-voter", Suffrage: raft.Nonvoter},
	}}
	voters, err := strictGuestIdentityInventoryVoters(configuration, "node-a")
	if err != nil || len(voters) != 1 || voters[0].nodeID != "node-a" {
		t.Fatalf("voters=%+v error=%v, want only node-a", voters, err)
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
