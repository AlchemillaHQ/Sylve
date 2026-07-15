// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type migrationTargetAuthStub struct {
	serviceInterfaces.AuthServiceInterface
	username string
}

func (s *migrationTargetAuthStub) CreateInternalClusterJWT(username, _ string) (string, error) {
	s.username = username
	return "migration-target-test-token", nil
}

func newTargetInventoryTestServer(
	t *testing.T,
	nodeID string,
	entries []clusterService.GuestIdentityInventoryEntry,
) *httptest.Server {
	t.Helper()
	return newTargetInventoryReportTestServer(
		t,
		nodeID,
		clusterService.BuildGuestIdentityInventoryReport(entries),
	)
}

func newTargetInventoryReportTestServer(
	t *testing.T,
	nodeID string,
	report clusterService.GuestIdentityInventoryReport,
) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/intra-cluster/guest-identity-inventory" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Cluster-Token"); got != "Bearer migration-target-test-token" {
			t.Errorf("cluster token header = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(internal.APIResponse[clusterService.GuestIdentityInventorySnapshot]{
			Status: "success",
			Data: clusterService.GuestIdentityInventorySnapshot{
				NodeID: nodeID,
				Report: report,
			},
		})
	}))
}

func TestRequireTargetGuestRecordAbsentBlocksSharedNumericID(t *testing.T) {
	const targetNodeID = "target-node"
	server := newTargetInventoryTestServer(t, targetNodeID, []clusterService.GuestIdentityInventoryEntry{
		{
			NodeID:    targetNodeID,
			GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID:   107,
			RecordID:  9,
			Name:      "existing-jail",
		},
	})
	defer server.Close()

	auth := &migrationTargetAuthStub{}
	svc := &Service{Cluster: &clusterService.Service{AuthService: auth}}
	err := svc.requireTargetGuestRecordAbsent(context.Background(), clusterModels.ClusterNode{
		NodeUUID: targetNodeID,
		API:      strings.TrimPrefix(server.URL, "https://"),
	}, 107)

	if !errors.Is(err, ErrTargetAlreadyHasGuest) {
		t.Fatalf("error = %v, want target guest conflict", err)
	}
	if !strings.Contains(err.Error(), "guest_type=jail") {
		t.Fatalf("error does not identify conflicting guest type: %v", err)
	}
	if auth.username != "migration" {
		t.Fatalf("cluster token username = %q, want migration", auth.username)
	}
}

func TestRequireTargetGuestRecordAbsentAllowsUnclaimedID(t *testing.T) {
	const targetNodeID = "target-node"
	server := newTargetInventoryTestServer(t, targetNodeID, []clusterService.GuestIdentityInventoryEntry{
		{
			NodeID:    targetNodeID,
			GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID:   106,
			RecordID:  8,
			Name:      "other-vm",
		},
	})
	defer server.Close()

	svc := &Service{Cluster: &clusterService.Service{AuthService: &migrationTargetAuthStub{}}}
	err := svc.requireTargetGuestRecordAbsent(context.Background(), clusterModels.ClusterNode{
		NodeUUID: targetNodeID,
		API:      strings.TrimPrefix(server.URL, "https://"),
	}, 107)
	if err != nil {
		t.Fatalf("unclaimed target ID was rejected: %v", err)
	}
}

func TestRequireTargetGuestRecordAbsentFailsClosedOnWrongNodeInventory(t *testing.T) {
	server := newTargetInventoryTestServer(t, "different-node", nil)
	defer server.Close()

	svc := &Service{Cluster: &clusterService.Service{AuthService: &migrationTargetAuthStub{}}}
	err := svc.requireTargetGuestRecordAbsent(context.Background(), clusterModels.ClusterNode{
		NodeUUID: "target-node",
		API:      strings.TrimPrefix(server.URL, "https://"),
	}, 107)
	if err == nil || !strings.Contains(err.Error(), "target_identity_inventory_node_mismatch") {
		t.Fatalf("wrong-node inventory error = %v", err)
	}
}

func TestRequireTargetGuestRecordAbsentFailsClosedOnInventoryDigestMismatch(t *testing.T) {
	const targetNodeID = "target-node"
	report := clusterService.BuildGuestIdentityInventoryReport(nil)
	report.Digest = "not-the-empty-inventory-digest"
	server := newTargetInventoryReportTestServer(t, targetNodeID, report)
	defer server.Close()

	svc := &Service{Cluster: &clusterService.Service{AuthService: &migrationTargetAuthStub{}}}
	err := svc.requireTargetGuestRecordAbsent(context.Background(), clusterModels.ClusterNode{
		NodeUUID: targetNodeID,
		API:      strings.TrimPrefix(server.URL, "https://"),
	}, 107)
	if err == nil || !strings.Contains(err.Error(), "target_identity_inventory_digest_mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestValidateJailPreflightRejectsNonexistentCTIDBeforeRemoteChecks(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	svc := &Service{DB: db}

	reasons := svc.validateJailPreflight(context.Background(), 404, clusterModels.ClusterNode{})
	if len(reasons) != 1 || reasons[0] != "jail_not_found" {
		t.Fatalf("reasons = %v, want [jail_not_found]", reasons)
	}
}
