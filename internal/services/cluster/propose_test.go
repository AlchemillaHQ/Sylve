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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func TestProposalErrorsKeepTheirIdentity(t *testing.T) {
	for _, sentinel := range []error{
		gorm.ErrRecordNotFound, raft.ErrNotLeader, raft.ErrLeadershipLost, raft.ErrRaftShutdown,
		ErrNodeLeaveFenced, ErrNodeReaddressFenced, ErrClusterProtocolOutdated,
		clusterModels.ErrGuestIdentityAlreadyInUse, clusterModels.ErrGuestIdentityClaimConflict,
	} {
		wrapped := fmt.Errorf("fsm_apply_failed: handler: %w", sentinel)
		if got := proposalError(ProposalErrorCode(wrapped), wrapped.Error()); !errors.Is(got, sentinel) {
			t.Fatalf("%v came back as %v", sentinel, got)
		}
	}
	if got := proposalError(ProposalErrorCode(errors.New("note_title_invalid")), "note_title_invalid"); got.Error() != "note_title_invalid" {
		t.Fatalf("unknown error came back as %q", got)
	}
}

type testPeerNetwork struct {
	mu  sync.Mutex
	cut map[string]bool
}

func (n *testPeerNetwork) setCut(nodeID string, cut bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut[nodeID] = cut
}

func (n *testPeerNetwork) isCut(nodeIDs ...string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, nodeID := range nodeIDs {
		if n.cut[nodeID] {
			return true
		}
	}
	return false
}

func routeTestPeerRequests(nodes []*clusterRaftTestNode) *testPeerNetwork {
	network := &testPeerNetwork{cut: make(map[string]bool)}
	byHost := make(map[string]*clusterRaftTestNode, len(nodes))
	for _, node := range nodes {
		byHost[raftAddressHost(node.id)] = node
	}
	respond := func(status int, response any) (int, []byte, error) {
		body, err := json.Marshal(response)
		return status, body, err
	}
	for _, node := range nodes {
		sender := node.id
		node.service.AuthService = &guestIdentityInventoryAuthStub{}
		node.service.peerRequest = func(ctx context.Context, _, rawURL string, payload []byte, headers map[string]string) (int, []byte, error) {
			parsed, err := url.Parse(rawURL)
			if err != nil {
				return 0, nil, err
			}
			targetNode := byHost[parsed.Hostname()]
			if targetNode == nil || network.isCut(sender, targetNode.id) {
				return 0, nil, fmt.Errorf("dial %s: connection refused", parsed.Host)
			}
			target := targetNode.service
			leaderCall, isLeaderCall := strings.CutPrefix(parsed.Path, "/api/intra-cluster/leader/")
			nodeCall, isNodeCall := strings.CutPrefix(parsed.Path, "/api/intra-cluster/node/")
			if isLeaderCall || isNodeCall {
				var result CallResult
				if isLeaderCall {
					result, err = target.ServeLeaderCall(ctx, sender, leaderCall, payload)
				} else {
					index, _ := strconv.ParseUint(headers[CallIndexHeader], 10, 64)
					result, err = target.ServeNodeCall(ctx, sender, headers[CallNodeHeader], index, nodeCall, payload)
				}
				if err != nil {
					return respond(http.StatusConflict, internal.APIResponse[CallResult]{
						Status: "error", Message: ProposalErrorCode(err), Error: err.Error(), Data: result,
					})
				}
				return respond(http.StatusOK, internal.APIResponse[CallResult]{Status: "success", Data: result})
			}
			switch parsed.Path {
			case "/api/intra-cluster/raft/apply":
				var cmd clusterModels.Command
				if err := json.Unmarshal(payload, &cmd); err != nil {
					return 0, nil, err
				}
				index, err := target.ApplyPeerProposal(ctx, sender, cmd)
				if err != nil {
					return respond(http.StatusConflict, internal.APIResponse[any]{Status: "error", Message: ProposalErrorCode(err), Error: err.Error()})
				}
				return respond(http.StatusOK, internal.APIResponse[ProposalResult]{Status: "success", Data: ProposalResult{Index: index}})
			case "/api/cluster/membership-status":
				var request membershipStatusRequest
				if err := json.Unmarshal(payload, &request); err != nil {
					return 0, nil, err
				}
				status, err := target.AuthoritativeMembershipStatus(request.NodeID, testNodePin(sender))
				if err != nil {
					return respond(http.StatusConflict, internal.APIResponse[MembershipStatus]{Status: "error", Message: "not_leader", Error: err.Error(), Data: status})
				}
				return respond(http.StatusOK, internal.APIResponse[MembershipStatus]{Status: "success", Data: status})
			case "/api/cluster/accept-join":
				var request JoinAdmissionRequest
				if err := json.Unmarshal(payload, &request); err != nil {
					return 0, nil, err
				}
				// The member asked forwards the admission to the leader.
				admitting := target
				for _, node := range nodes {
					if node.raft.State() == raft.Leader {
						admitting = node.service
					}
				}
				status, err := admitting.AdmitJoiner(ctx, request, headers[auth.ClusterKeyHeader], testNodePin(request.NodeID))
				var conflict *GuestIdentityInventoryConflictError
				switch {
				case err == nil:
					return respond(http.StatusAccepted, internal.APIResponse[ClusterJoinStatus]{Status: "success", Data: status})
				case errors.As(err, &conflict):
					return respond(http.StatusConflict, internal.APIResponse[GuestIdentityInventoryReport]{
						Status: "error", Message: "guest_identity_inventory_conflict", Error: err.Error(), Data: conflict.Report,
					})
				case strings.Contains(err.Error(), "invalid_cluster_key"):
					return respond(http.StatusForbidden, internal.APIResponse[any]{Status: "error", Message: "invalid_cluster_key", Error: err.Error()})
				case strings.HasPrefix(err.Error(), "joining_node_users_taken"):
					return respond(http.StatusConflict, internal.APIResponse[any]{Status: "error", Message: "joining_node_users_taken", Error: err.Error()})
				default:
					return respond(http.StatusServiceUnavailable, internal.APIResponse[any]{Status: "error", Message: "cluster_join_admission_unavailable", Error: err.Error()})
				}
			}
			return 0, nil, fmt.Errorf("unexpected request %s", rawURL)
		}
	}
	return network
}

type membershipStatusRequest struct {
	NodeID string `json:"nodeId"`
}

func TestIntegrationProposeFromFollower(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var follower *clusterRaftTestNode
	for _, node := range nodes {
		if node != leader && follower == nil {
			follower = node
		}
	}
	routeTestPeerRequests(nodes)

	if err := follower.service.ProposeNoteCreate(t.Context(), "from-follower", "content"); err != nil {
		t.Fatalf("propose from follower: %v", err)
	}
	var notes []clusterModels.ClusterNote
	if err := follower.service.DB.Find(&notes).Error; err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Title != "from-follower" {
		t.Fatalf("follower notes right after Propose = %+v", notes)
	}
	if err := follower.service.ProposeNoteDelete(t.Context(), 999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("delete of a missing note from a follower = %v, want gorm.ErrRecordNotFound", err)
	}

	// An old leader rejects new commands before it can check their protocol.
	forward := follower.service.peerRequest
	follower.service.peerRequest = func(context.Context, string, string, []byte, map[string]string) (int, []byte, error) {
		t.Error("forwarded a new command to a protocol-1 leader")
		return 0, nil, errors.New("raft_command_not_proposable: acl_role/set")
	}
	role := clusterModels.ACLRole{Name: "Operator", Privileges: []string{auth.SysAudit}}
	if err := follower.service.Propose(t.Context(), "acl_role", "set", role); !errors.Is(err, ErrClusterProtocolOutdated) {
		t.Fatalf("follower proposal with a protocol-1 leader = %v, want ErrClusterProtocolOutdated", err)
	}
	follower.service.peerRequest = forward
	for _, observer := range nodes {
		for _, member := range nodes {
			recordCurrentProtocol(t, observer, member)
		}
	}
	if err := follower.service.Propose(t.Context(), "acl_role", "set", role); err != nil {
		t.Fatalf("follower proposal after all members upgraded: %v", err)
	}
	var stored clusterModels.ACLRole
	if err := follower.service.DB.Where("name = ?", role.Name).First(&stored).Error; err != nil {
		t.Fatalf("role on follower right after Propose: %v", err)
	}
}

func TestApplyProposalWithoutRaftOrCaller(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t, &clusterModels.Cluster{})})
	cmd := clusterModels.Command{Type: "note", Action: "create", Data: json.RawMessage(`{"title":"x"}`)}
	if _, err := service.applyProposal(t.Context(), cmd); !errors.Is(err, raft.ErrRaftShutdown) {
		t.Fatalf("without Raft = %v, want raft.ErrRaftShutdown", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.applyProposal(ctx, cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller = %v, want context.Canceled", err)
	}
}

func TestApplyPeerProposalTakesOnlyWhatProposeSends(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t, &clusterModels.Cluster{})})
	identity := func(nodeID string) clusterModels.Command {
		cmd, err := newCommand("cluster_ssh_identity", "upsert", clusterModels.ClusterSSHIdentity{NodeUUID: nodeID})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	backupJobDelete, err := newCommand("backup_job", "delete", map[string]uint{"id": 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		cmd  clusterModels.Command
		want string
	}{
		{"leader-only command", backupJobDelete, "raft_command_not_proposable"},
		{"another node's identity", identity("node-b"), "raft_command_issuer_mismatch"},
		// Passes both checks, then finds no Raft.
		{"own identity", identity("node-a"), "cluster_consensus_unavailable"},
	} {
		if _, err := service.ApplyPeerProposal(t.Context(), "node-a", test.cmd); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: %v, want %s", test.name, err, test.want)
		}
	}
	if err := service.Propose(t.Context(), "backup_job", "delete", map[string]uint{"id": 1}); err == nil ||
		!strings.Contains(err.Error(), "raft_command_not_proposable") {
		t.Fatalf("Propose of a command peers may not send = %v", err)
	}
}

func TestProposeAppliesRealmCommandsStandalone(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t)})
	realm := clusterModels.AuthRealm{Name: "company", Issuer: "https://idp.example", ClientID: "sylve", AutoCreate: true}
	alice := clusterModels.OIDCUser{Realm: "company", Username: "alice", Subject: "sub-alice"}
	for _, step := range []struct {
		commandType, action string
		payload             any
	}{
		{"auth_realm", "set", clusterModels.AuthRealmSet{AuthRealm: realm}},
		{"oidc_user", "create", clusterModels.OIDCUser{Realm: "company", Username: "bob"}},
		{"oidc_user", "login", alice},
		{"oidc_user", "update", clusterModels.OIDCUser{Realm: "company", Username: "alice", Locked: true}},
		{"oidc_user", "delete", alice},
		{"auth_realm", "delete", realm},
	} {
		if err := service.Propose(t.Context(), step.commandType, step.action, step.payload); err != nil {
			t.Fatalf("%s/%s: %v", step.commandType, step.action, err)
		}
	}
}

func TestWriteReportsAnUnreadableClusterRow(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t)})
	if err := service.DB.Exec("INSERT INTO clusters (id, enabled) VALUES (1, 'not-a-bool')").Error; err != nil {
		t.Fatal(err)
	}
	err := service.ProposeNoteCreate(t.Context(), "title", "content")
	if err == nil || !strings.Contains(err.Error(), "cluster_state_unavailable") {
		t.Fatalf("write = %v, want cluster_state_unavailable", err)
	}
}

func TestIntegrationWriteDuringLeadershipTransfer(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var target *clusterRaftTestNode
	for _, node := range nodes {
		if node != leader {
			target = node
			break
		}
	}
	// Cut the target off and leave it a write behind so the transfer waits on it until it times out.
	leader.transport.Disconnect(target.addr)
	target.transport.Disconnect(leader.addr)
	t.Cleanup(func() { connectClusterRaftTestNodes(nodes) })
	if err := leader.service.ProposeNoteCreate(t.Context(), "before", "content"); err != nil {
		t.Fatal(err)
	}

	transfer := leader.raft.LeadershipTransferToServer(raft.ServerID(target.id), target.addr)
	var err error
	for deadline := time.Now().Add(time.Second); err == nil && time.Now().Before(deadline); {
		err = leader.service.ProposeNoteCreate(t.Context(), "during", "content")
	}
	_ = transfer.Error()
	if !errors.Is(err, raft.ErrLeadershipLost) || !errors.Is(err, raft.ErrLeadershipTransferInProgress) {
		t.Fatalf("write during the transfer = %v, want raft.ErrLeadershipLost wrapping the transfer", err)
	}
}

func TestIntegrationLeaderWaitsForEveryMemberToKnowACommand(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ClusterNode{})
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var follower *clusterRaftTestNode
	for _, node := range nodes {
		clusterModels.OnSince(node.service.fsm, 2, "test", "since_2", func(*gorm.DB, *struct{}) error { return nil })
		if node != leader {
			follower = node
		}
	}
	if err := leader.service.DB.Create(&clusterModels.ClusterNode{NodeUUID: follower.id, Protocol: 1}).Error; err != nil {
		t.Fatal(err)
	}

	if err := leader.service.apply("test", "since_2", struct{}{}); !errors.Is(err, ErrClusterProtocolOutdated) {
		t.Fatalf("with a protocol-1 member = %v, want ErrClusterProtocolOutdated", err)
	}
	if err := leader.service.ProposeNoteCreate(t.Context(), "baseline", "content"); err != nil {
		t.Fatalf("baseline command held back: %v", err)
	}
	leader.service.reconcileClusterIdentity(t.Context())
	if id, err := clusterModels.ReadClusterID(leader.service.DB); err != nil || id != "" {
		t.Fatalf("identity with a protocol-1 member = %q, %v", id, err)
	}
	if err := leader.service.DB.Model(&clusterModels.ClusterNode{}).Where("node_uuid = ?", follower.id).
		Update("protocol", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := leader.service.apply("test", "since_2", struct{}{}); err != nil {
		t.Fatalf("after the member upgraded = %v", err)
	}
	leader.service.reconcileClusterIdentity(t.Context())
	id, err := clusterModels.ReadClusterID(leader.service.DB)
	if err != nil || id == "" {
		t.Fatalf("identity after the member upgraded = %q, %v", id, err)
	}
	index := leader.service.AppliedIndex()
	leader.service.reconcileClusterIdentity(t.Context())
	if leader.service.AppliedIndex() != index {
		t.Fatal("repeated identity sync proposed another command")
	}
	waitForClusterCondition(t, time.Second, "cluster identity replicated", func() bool {
		other, err := clusterModels.ReadClusterID(follower.service.DB)
		return err == nil && other == id
	})
}

func testNodePin(nodeID string) [32]byte {
	return sha256.Sum256([]byte(nodeID))
}
