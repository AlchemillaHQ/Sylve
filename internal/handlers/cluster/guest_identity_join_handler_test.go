// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func TestAcceptJoinRejectsMalformedInventoryPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/accept-join", AcceptJoin(nil))

	body := []byte(fmt.Sprintf(
		`{"nodeId":"node-1","nodeIp":"192.0.2.10","nodeVersion":%q,"inventory":{"entries":"not-an-array"}}`,
		cmd.Version,
	))
	response := performJSONRequest(t, router, http.MethodPost, "/cluster/accept-join", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}

	var decoded handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "invalid_request_payload" {
		t.Fatalf("message = %q, want invalid_request_payload", decoded.Message)
	}
}

func TestWriteJoinAdmissionErrorReturnsTypedInventoryConflict(t *testing.T) {
	report := cluster.BuildGuestIdentityInventoryReport([]cluster.GuestIdentityInventoryEntry{
		{NodeID: "node-a", GuestType: "vm", GuestID: 110, RecordID: 1, Name: "vm-110"},
		{NodeID: "node-b", GuestType: "jail", GuestID: 110, RecordID: 2, Name: "jail-110"},
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writeJoinAdmissionError(context, &cluster.GuestIdentityInventoryConflictError{Report: report})

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	var decoded handlerAPIResponse[cluster.GuestIdentityInventoryReport]
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "guest_identity_inventory_conflict" {
		t.Fatalf("message = %q, want guest_identity_inventory_conflict", decoded.Message)
	}
	if decoded.Data.Digest != report.Digest || len(decoded.Data.Conflicts) != 1 {
		t.Fatalf("typed conflict report = %+v, want %+v", decoded.Data, report)
	}
	if !strings.Contains(decoded.Error, "guest_identity_inventory_conflict") {
		t.Fatalf("error = %q, want inventory conflict", decoded.Error)
	}
}

func TestWriteJoinAdmissionErrorTellsRefusalsFromRetries(t *testing.T) {
	tests := []struct {
		err     error
		status  int
		message string
	}{
		{fmt.Errorf("not_leader"), http.StatusServiceUnavailable, "cluster_join_admission_unavailable"},
		{&cluster.ClusterVersionError{Code: "cluster_version_mismatch"}, http.StatusConflict, "cluster_version_mismatch"},
		{errors.New("invalid_cluster_key"), http.StatusForbidden, "invalid_cluster_key"},
		{errors.New("joining_node_address_already_in_use"), http.StatusConflict, "cluster_join_refused"},
		{errors.New("invalid_joining_node_ip"), http.StatusConflict, "cluster_join_refused"},
		{errors.New("joining_node_hostname_taken: node-1"), http.StatusConflict, "joining_node_hostname_taken"},
		{fmt.Errorf("add_nonvoter_failed: %w", raft.ErrLeadershipLost), http.StatusServiceUnavailable, "cluster_join_outcome_uncertain"},
		{fmt.Errorf("joining_guest_identity_claim_failed: %w", raft.ErrNotLeader), http.StatusServiceUnavailable, "cluster_join_outcome_uncertain"},
		{cluster.ErrNodeLeaveFenced, http.StatusServiceUnavailable, "cluster_join_admission_unavailable"},
		{errors.New("raft_not_initialized"), http.StatusServiceUnavailable, "cluster_join_admission_unavailable"},
	}
	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		writeJoinAdmissionError(context, test.err)
		var decoded handlerAPIResponse[any]
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != test.status || decoded.Message != test.message {
			t.Errorf("%v: %d %q, want %d %q", test.err, recorder.Code, decoded.Message, test.status, test.message)
		}
	}
}

func TestCreateClusterReturnsTypedInventoryConflict(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := db.Create(&clusterModels.Cluster{Enabled: false, RaftPort: cluster.ClusterRaftPort}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if err := db.Create(&vmModels.VM{RID: 120, Name: "vm-120"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	if err := db.Create(&jailModels.Jail{CTID: 120, Name: "jail-120"}).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster", CreateCluster(newTestClusterService(db, "node-create")))
	response := performJSONRequest(t, router, http.MethodPost, "/cluster", []byte(`{"ip":"127.0.0.1"}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", response.Code, response.Body.String())
	}

	var decoded handlerAPIResponse[cluster.GuestIdentityInventoryReport]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "guest_identity_inventory_conflict" ||
		len(decoded.Data.Conflicts) != 1 ||
		decoded.Data.Conflicts[0].GuestID != 120 {
		t.Fatalf("unexpected create conflict: %+v", decoded)
	}
}

type joinLeaderStubState struct {
	mu         sync.Mutex
	paths      []string
	admissions []AcceptJoinRequest
	errors     []string
}

func (s *joinLeaderStubState) recordPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
}

func (s *joinLeaderStubState) recordAdmission(admission AcceptJoinRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admissions = append(s.admissions, admission)
}

func (s *joinLeaderStubState) recordError(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}

func (s *joinLeaderStubState) checkTransport(r *http.Request) {
	if r.Header.Get(auth.ClusterKeyHeader) != "cluster-secret" ||
		r.Header.Get("Accept") != "application/json" {
		s.recordError("missing minimal join headers on %s: %v", r.URL.Path, r.Header)
	}
	wantContentType := ""
	if r.URL.Path == "/api/cluster/accept-join" {
		wantContentType = "application/json"
	}
	if r.Header.Get("Content-Type") != wantContentType {
		s.recordError("content type on %s = %q, want %q", r.URL.Path, r.Header.Get("Content-Type"), wantContentType)
	}
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "Cookie", auth.ProxyUserHeader, "X-Current-Hostname",
	} {
		if value := r.Header.Get(name); value != "" {
			s.recordError("browser header %s leaked to %s", name, r.URL.Path)
		}
	}
}

func (s *joinLeaderStubState) snapshot() ([]string, []AcceptJoinRequest, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...),
		append([]AcceptJoinRequest(nil), s.admissions...),
		append([]string(nil), s.errors...)
}

func writeJoinLeaderStubJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func startJoinLeaderTLSStub(t *testing.T, handler http.Handler, serverTLS *tls.Config) (string, string) {
	t.Helper()

	type listenCandidate struct {
		network string
		host    string
	}
	candidates := []listenCandidate{
		{network: "tcp4", host: "127.0.0.1"},
		{network: "tcp6", host: "::1"},
		{network: "tcp4", host: "127.0.0.2"},
	}
	var listenErrors []string
	for _, candidate := range candidates {
		address := net.JoinHostPort(candidate.host, strconv.Itoa(cluster.ClusterEmbeddedHTTPSPort))
		listener, err := net.Listen(candidate.network, address)
		if err != nil {
			listenErrors = append(listenErrors, fmt.Sprintf("%s: %v", address, err))
			continue
		}

		server := httptest.NewUnstartedServer(handler)
		_ = server.Listener.Close()
		server.Listener = listener
		server.EnableHTTP2 = false
		server.Config.SetKeepAlivesEnabled(false)
		server.TLS = serverTLS
		server.StartTLS()
		t.Cleanup(server.Close)
		pin := cluster.CertPin(server.Certificate())
		return candidate.host, "cluster-secret." + hex.EncodeToString(pin[:])
	}

	t.Skipf("cluster HTTPS test port unavailable: %s", strings.Join(listenErrors, "; "))
	return "", ""
}

func TestJoinClusterRelaysARefusalAndLeavesTheNodeUntouched(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.Cluster{},
		&clusterModels.ClusterNote{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	clusterRow := clusterModels.Cluster{RaftPort: cluster.ClusterRaftPort}
	if err := db.Create(&clusterRow).Error; err != nil {
		t.Fatalf("create cluster row: %v", err)
	}
	note := clusterModels.ClusterNote{Title: "sentinel", Content: "must survive rejected join"}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create clustered-data sentinel: %v", err)
	}
	vm := vmModels.VM{RID: 407, Name: "joiner-vm-407"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create joiner VM: %v", err)
	}

	clusterService := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	if err := clusterService.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	localNodeID := strings.TrimSpace(clusterService.LocalNodeID())
	if localNodeID == "" {
		t.Skip("system UUID is unavailable; cannot satisfy JoinCluster node identity check")
	}

	conflictReport := cluster.BuildGuestIdentityInventoryReport([]cluster.GuestIdentityInventoryEntry{
		{NodeID: "leader-node", GuestType: "jail", GuestID: vm.RID, RecordID: 1, Name: "leader-jail-407"},
		{NodeID: localNodeID, GuestType: "vm", GuestID: vm.RID, RecordID: vm.ID, Name: vm.Name},
	})
	stubState := &joinLeaderStubState{}
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stubState.recordPath(r.URL.Path)
		stubState.checkTransport(r)
		switch r.URL.Path {
		case "/api/health/basic":
			writeJoinLeaderStubJSON(w, http.StatusOK, internal.APIResponse[map[string]string]{
				Status: "success",
				Data:   map[string]string{"sylveVersion": cmd.Version},
			})
		case "/api/cluster/accept-join":
			var admission AcceptJoinRequest
			if err := json.NewDecoder(r.Body).Decode(&admission); err != nil {
				stubState.recordError("decode admission: %v", err)
				writeJoinLeaderStubJSON(w, http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "invalid_request_payload", Error: err.Error(),
				})
				return
			}
			stubState.recordAdmission(admission)
			if admission.NodeID != localNodeID || admission.NodeIP != "203.0.113.250" || admission.NodeVersion != cmd.Version {
				stubState.recordError("admission = %+v", admission)
			}
			if len(admission.Inventory.Entries) != 1 || admission.Inventory.Entries[0].GuestID != vm.RID {
				stubState.recordError("admission inventory = %+v", admission.Inventory)
			}
			writeJoinLeaderStubJSON(w, http.StatusConflict, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
				Status:  "error",
				Message: "guest_identity_inventory_conflict",
				Error:   "guest_identity_inventory_conflict",
				Data:    conflictReport,
			})
		default:
			stubState.recordError("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	leaderIP, joinKey := startJoinLeaderTLSStub(t, stub, nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(clusterService))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID:     localNodeID,
		NodeIP:     "203.0.113.250",
		LeaderIP:   leaderIP,
		ClusterKey: joinKey,
	})
	if err != nil {
		t.Fatalf("marshal join request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/cluster/join", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer browser-token")
	request.Header.Set("Proxy-Authorization", "Basic browser")
	request.Header.Set("Cookie", "session=browser")
	request.Header.Set(auth.ProxyUserHeader, `{"id":1,"username":"admin","authType":"sylve"}`)
	request.Header.Set("X-Current-Hostname", "browser-selected-node")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want the leader's conflict: %s", response.Code, response.Body.String())
	}
	var decoded handlerAPIResponse[cluster.GuestIdentityInventoryReport]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "guest_identity_inventory_conflict" || decoded.Data.Digest != conflictReport.Digest {
		t.Fatalf("unexpected relayed leader conflict: %+v", decoded)
	}

	paths, admissions, stubErrors := stubState.snapshot()
	if len(stubErrors) != 0 {
		t.Fatalf("leader stub errors: %v", stubErrors)
	}
	if got, want := strings.Join(paths, ","), "/api/health/basic,/api/cluster/accept-join"; got != want {
		t.Fatalf("leader request sequence = %q, want %q", got, want)
	}
	if len(admissions) != 1 {
		t.Fatalf("leader admissions = %+v, want one", admissions)
	}

	var persisted clusterModels.Cluster
	if err := db.First(&persisted, clusterRow.ID).Error; err != nil {
		t.Fatalf("reload cluster row: %v", err)
	}
	if persisted.Enabled || persisted.Key != "" || persisted.RaftIP != "" || persisted.HasIncompleteJoin() {
		t.Fatalf("rejected join changed the cluster row: %+v", persisted)
	}
	if clusterService.Raft() != nil {
		t.Fatalf("rejected join initialized Raft: %v", clusterService.Raft())
	}
	var noteCount int64
	if err := db.Model(&clusterModels.ClusterNote{}).Where("id = ?", note.ID).Count(&noteCount).Error; err != nil {
		t.Fatalf("count clustered-data sentinel: %v", err)
	}
	if noteCount != 1 {
		t.Fatalf("clustered-data sentinel count = %d, want 1", noteCount)
	}
}

func TestJoinClusterClockDriftPreflightStopsBeforeAdmission(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.Cluster{},
		&clusterModels.ClusterNote{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	clusterRow := clusterModels.Cluster{
		Enabled:  false,
		Key:      "standalone-sentinel-key",
		RaftIP:   "standalone-sentinel-ip",
		RaftPort: 19180,
	}
	if err := db.Create(&clusterRow).Error; err != nil {
		t.Fatalf("create cluster sentinel: %v", err)
	}

	clusterService := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	localNodeID := strings.TrimSpace(clusterService.LocalNodeID())
	if localNodeID == "" {
		t.Skip("system UUID is unavailable; cannot satisfy JoinCluster node identity check")
	}

	leaderTime := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	stubState := &joinLeaderStubState{}
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stubState.recordPath(r.URL.Path)
		stubState.checkTransport(r)
		switch r.URL.Path {
		case "/api/health/basic":
			writeJoinLeaderStubJSON(w, http.StatusOK, internal.APIResponse[map[string]string]{
				Status: "success",
				Data: map[string]string{
					"sylveVersion": cmd.Version,
					"serverTime":   leaderTime,
				},
			})
		default:
			stubState.recordError("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	leaderIP, joinKey := startJoinLeaderTLSStub(t, stub, nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(clusterService))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID:     localNodeID,
		NodeIP:     "203.0.113.251",
		LeaderIP:   leaderIP,
		ClusterKey: joinKey,
	})
	if err != nil {
		t.Fatalf("marshal join request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/cluster/join", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var decoded handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "cluster_join_clock_drift" || !strings.HasPrefix(decoded.Error, "leader_offset=-") {
		t.Fatalf("unexpected clock drift response: %+v", decoded)
	}

	paths, admissions, stubErrors := stubState.snapshot()
	if len(stubErrors) != 0 {
		t.Fatalf("leader stub errors: %v", stubErrors)
	}
	if got, want := strings.Join(paths, ","), "/api/health/basic"; got != want {
		t.Fatalf("leader request sequence = %q, want %q", got, want)
	}
	if len(admissions) != 0 {
		t.Fatalf("leader admissions = %+v, want none", admissions)
	}

	var persisted clusterModels.Cluster
	if err := db.First(&persisted, clusterRow.ID).Error; err != nil {
		t.Fatalf("reload cluster sentinel: %v", err)
	}
	if persisted.Enabled || persisted.Key != clusterRow.Key || persisted.RaftIP != clusterRow.RaftIP ||
		persisted.RaftPort != clusterRow.RaftPort {
		t.Fatalf("rejected join mutated cluster state: got=%+v want=%+v", persisted, clusterRow)
	}
}

func TestJoinClusterInWindowClockProceedsToAdmission(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := db.Create(&clusterModels.Cluster{
		Enabled:  false,
		Key:      "standalone-sentinel-key",
		RaftIP:   "standalone-sentinel-ip",
		RaftPort: 19180,
	}).Error; err != nil {
		t.Fatalf("create cluster sentinel: %v", err)
	}

	clusterService := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	if err := clusterService.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	localNodeID := strings.TrimSpace(clusterService.LocalNodeID())
	if localNodeID == "" {
		t.Skip("system UUID is unavailable; cannot satisfy JoinCluster node identity check")
	}

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health/basic":
			writeJoinLeaderStubJSON(w, http.StatusOK, internal.APIResponse[map[string]string]{
				Status: "success",
				Data: map[string]string{
					"sylveVersion": cmd.Version,
					"serverTime":   time.Now().Format(time.RFC3339Nano),
				},
			})
		case "/api/cluster/accept-join":
			writeJoinLeaderStubJSON(w, http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "in_window_clock_reached_admission",
				Error:   "in_window_clock_reached_admission",
			})
		default:
			http.NotFound(w, r)
		}
	})
	leaderIP, joinKey := startJoinLeaderTLSStub(t, stub, nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(clusterService))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID:     localNodeID,
		NodeIP:     "203.0.113.252",
		LeaderIP:   leaderIP,
		ClusterKey: joinKey,
	})
	if err != nil {
		t.Fatalf("marshal join request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/cluster/join", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var decoded handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "in_window_clock_reached_admission" {
		t.Fatalf("message = %q, want the leader admission response", decoded.Message)
	}
}

func TestJoinClusterRefusesANodeOtherThanTheKeys(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&clusterModels.Cluster{RaftPort: cluster.ClusterRaftPort}).Error; err != nil {
		t.Fatal(err)
	}
	clusterService := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	localNodeID := strings.TrimSpace(clusterService.LocalNodeID())
	if localNodeID == "" {
		t.Skip("system UUID is unavailable; cannot satisfy JoinCluster node identity check")
	}
	stubState := &joinLeaderStubState{}
	leaderIP, _ := startJoinLeaderTLSStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stubState.recordPath(r.URL.Path)
		http.NotFound(w, r)
	}), nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(clusterService))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID: localNodeID, NodeIP: "203.0.113.253", LeaderIP: leaderIP,
		ClusterKey: "cluster-secret." + strings.Repeat("ab", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performJSONRequest(t, router, http.MethodPost, "/cluster/join", requestBody)
	var decoded handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || decoded.Message != "cluster_join_key_node_mismatch" {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if paths, _, _ := stubState.snapshot(); len(paths) != 0 {
		t.Fatalf("the other node was sent %v", paths)
	}
}

func startFollowerRaft(t *testing.T, id string) *raft.Raft {
	t.Helper()
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)
	config.Logger = hclog.NewNullLogger()
	store := raft.NewInmemStore()
	_, transport := raft.NewInmemTransport(raft.ServerAddress(id))
	r, err := raft.NewRaft(config, nil, store, store, raft.NewInmemSnapshotStore(), transport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown().Error() })
	if err := r.BootstrapCluster(raft.Configuration{Servers: []raft.Server{
		{ID: raft.ServerID(id), Address: raft.ServerAddress(id)}, {ID: "absent", Address: "absent"},
	}}).Error(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAcceptJoinOnAFollowerForwardsToTheLeader(t *testing.T) {
	joiner, member := newJoinTestCert(t).Leaf, newJoinTestCert(t).Leaf
	joinerPin, memberPin := cluster.CertPin(joiner), cluster.CertPin(member)
	db := newClusterHandlerTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterSSHIdentity{})
	if err := db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "member", SSHHost: "192.0.2.11", PublicKey: "ssh-ed25519 AAAA", CertPin: hex.EncodeToString(memberPin[:]),
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := newTestClusterService(db, "follower")
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	service.SetRaft(startFollowerRaft(t, "follower"))

	originalResolver, originalForward := resolveLeaderAPIForForward, clusterForwardHTTP
	t.Cleanup(func() { resolveLeaderAPIForForward, clusterForwardHTTP = originalResolver, originalForward })
	resolveLeaderAPIForForward = func(*cluster.Service, string, string) string { return "https://192.0.2.10:8184" }
	var forwarded []string
	var forwardedBody []byte
	var forwardedHeaders map[string]string
	clusterForwardHTTP = func(
		_ context.Context, method, url string, body []byte, headers map[string]string, _ time.Duration,
	) (clusterForwardResponse, error) {
		forwarded = append(forwarded, method+" "+url)
		forwardedBody, forwardedHeaders = body, headers
		return clusterForwardResponse{
			StatusCode: http.StatusConflict, Body: []byte(`{"status":"error","message":"guest_identity_inventory_conflict"}`),
		}, nil
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/accept-join", func(c *gin.Context) { c.Set("ClusterKey", "join-secret") }, AcceptJoin(service))
	body, err := json.Marshal(AcceptJoinRequest{
		NodeID: "joiner", NodeIP: "192.0.2.20", NodeVersion: cmd.Version, NodeHostname: "joiner", SSHPublicKey: "ssh-ed25519 AAAA",
		Inventory: cluster.BuildGuestIdentityInventoryReport(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func(cert *x509.Certificate, joinerPinHeader string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/cluster/accept-join", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if joinerPinHeader != "" {
			request.Header.Set(cluster.JoinerPinHeader, joinerPinHeader)
		}
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
		response := httptest.NewRecorder()
		service.PeerHandler(router).ServeHTTP(response, request)
		return response
	}

	response := send(joiner, hex.EncodeToString(memberPin[:]))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "guest_identity_inventory_conflict") {
		t.Fatalf("relayed answer: %d %s", response.Code, response.Body.String())
	}
	if len(forwarded) != 1 || forwarded[0] != "POST https://192.0.2.10:8184/api/cluster/accept-join" ||
		!bytes.Equal(forwardedBody, body) || forwardedHeaders[auth.ClusterKeyHeader] != "join-secret" ||
		forwardedHeaders[cluster.JoinerPinHeader] != hex.EncodeToString(joinerPin[:]) {
		t.Fatalf("handed on %v with %v: %s", forwarded, forwardedHeaders, forwardedBody)
	}

	response = send(member, hex.EncodeToString(joinerPin[:]))
	if len(forwarded) != 1 || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("an admission handed on to a follower: %d %s, forwards %v", response.Code, response.Body.String(), forwarded)
	}
}

func TestJoinClusterSendsTheAdmissionOnlyToTheKeysNode(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newClusterHandlerTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterNote{}, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&clusterModels.Cluster{RaftPort: cluster.ClusterRaftPort}).Error; err != nil {
		t.Fatal(err)
	}
	clusterService := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	localNodeID := strings.TrimSpace(clusterService.LocalNodeID())
	if localNodeID == "" {
		t.Skip("system UUID is unavailable; cannot satisfy JoinCluster node identity check")
	}
	if err := clusterService.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	impostor := newJoinTestCert(t)
	var handshakes atomic.Int32
	serverTLS := &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		if handshakes.Add(1) == 1 {
			return nil, nil
		}
		return &tls.Config{Certificates: []tls.Certificate{impostor}}, nil
	}}
	stubState := &joinLeaderStubState{}
	leaderIP, joinKey := startJoinLeaderTLSStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stubState.recordPath(r.URL.Path)
		writeJoinLeaderStubJSON(w, http.StatusOK, internal.APIResponse[map[string]string]{
			Status: "success", Data: map[string]string{"sylveVersion": cmd.Version, "serverTime": time.Now().Format(time.RFC3339Nano)},
		})
	}), serverTLS)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(clusterService))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID: localNodeID, NodeIP: "203.0.113.254", LeaderIP: leaderIP, ClusterKey: joinKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performJSONRequest(t, router, http.MethodPost, "/cluster/join", requestBody)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), "cluster_join_key_node_mismatch") {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if paths, _, _ := stubState.snapshot(); strings.Join(paths, ",") != "/api/health/basic" {
		t.Fatalf("the node at the address was sent %v", paths)
	}
}
