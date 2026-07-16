// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func TestAcceptJoinPreflightRejectsMalformedInventoryPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/accept-join", AcceptJoin(nil))

	body := []byte(fmt.Sprintf(
		`{"nodeId":"node-1","nodeIp":"192.0.2.10","clusterKey":"secret","nodeVersion":%q,"preflight":true,"inventory":{"entries":"not-an-array"}}`,
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

func TestAcceptJoinPreflightStillRejectsVersionMismatchBeforeServiceUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/accept-join", AcceptJoin(nil))

	body := []byte(`{"nodeId":"node-1","nodeIp":"192.0.2.10","clusterKey":"secret","nodeVersion":"0.0.0","preflight":true,"inventory":{"entries":[],"conflicts":[],"digest":"bad"}}`)
	response := performJSONRequest(t, router, http.MethodPost, "/cluster/accept-join", body)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", response.Code, response.Body.String())
	}

	var decoded handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "cluster_version_mismatch" {
		t.Fatalf("message = %q, want cluster_version_mismatch", decoded.Message)
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
	router.POST("/cluster", CreateCluster(nil, &cluster.Service{DB: db, NodeID: "node-create"}, nil))
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

func startJoinLeaderTLSStub(t *testing.T, handler http.Handler) string {
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
		server.StartTLS()
		t.Cleanup(server.Close)
		return candidate.host
	}

	t.Skipf("cluster HTTPS test port unavailable: %s", strings.Join(listenErrors, "; "))
	return ""
}

func TestJoinClusterLeaderConflictPreflightLeavesStandaloneStateUntouched(t *testing.T) {
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
	note := clusterModels.ClusterNote{Title: "sentinel", Content: "must survive rejected join"}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create clustered-data sentinel: %v", err)
	}
	vm := vmModels.VM{RID: 407, Name: "joiner-vm-407"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create joiner VM: %v", err)
	}

	clusterService := &cluster.Service{DB: db}
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
			if !admission.Preflight {
				stubState.recordError("non-preflight admission reached leader")
			}
			if admission.NodeID != localNodeID {
				stubState.recordError("admission node ID = %q, want %q", admission.NodeID, localNodeID)
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
	leaderIP := startJoinLeaderTLSStub(t, stub)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(&auth.Service{DB: db}, clusterService, nil, nil))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID:     localNodeID,
		NodeIP:     "203.0.113.250",
		LeaderIP:   leaderIP,
		ClusterKey: "cluster-secret",
	})
	if err != nil {
		t.Fatalf("marshal join request: %v", err)
	}
	response := performJSONRequest(t, router, http.MethodPost, "/cluster/join", requestBody)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want leader preflight conflict: %s", response.Code, response.Body.String())
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
	if len(admissions) != 1 || !admissions[0].Preflight {
		t.Fatalf("leader admissions = %+v, want exactly one preflight", admissions)
	}

	var persisted clusterModels.Cluster
	if err := db.First(&persisted, clusterRow.ID).Error; err != nil {
		t.Fatalf("reload cluster sentinel: %v", err)
	}
	if persisted.Enabled || persisted.Key != clusterRow.Key || persisted.RaftIP != clusterRow.RaftIP ||
		persisted.RaftPort != clusterRow.RaftPort {
		t.Fatalf("rejected join mutated cluster state: got=%+v want=%+v", persisted, clusterRow)
	}
	if clusterService.Raft != nil {
		t.Fatalf("rejected join initialized Raft: %v", clusterService.Raft)
	}
	var noteCount int64
	if err := db.Model(&clusterModels.ClusterNote{}).Where("id = ?", note.ID).Count(&noteCount).Error; err != nil {
		t.Fatalf("count clustered-data sentinel: %v", err)
	}
	if noteCount != 1 {
		t.Fatalf("clustered-data sentinel count = %d, want 1", noteCount)
	}
}

func TestJoinClusterInventoryChangeAfterPreflightLeavesStandaloneStateUntouched(t *testing.T) {
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
	if err := db.Create(&vmModels.VM{RID: 408, Name: "joiner-vm-408"}).Error; err != nil {
		t.Fatalf("create initial VM: %v", err)
	}

	clusterService := &cluster.Service{DB: db}
	localNodeID := strings.TrimSpace(clusterService.LocalNodeID())
	if localNodeID == "" {
		t.Skip("system UUID is unavailable; cannot satisfy JoinCluster node identity check")
	}

	stubState := &joinLeaderStubState{}
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stubState.recordPath(r.URL.Path)
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
			if !admission.Preflight {
				stubState.recordError("non-preflight admission reached leader")
			}
			if err := db.Create(&jailModels.Jail{CTID: 409, Name: "late-jail-409"}).Error; err != nil {
				stubState.recordError("create late jail: %v", err)
				writeJoinLeaderStubJSON(w, http.StatusInternalServerError, internal.APIResponse[any]{
					Status: "error", Message: "test_setup_failed", Error: err.Error(),
				})
				return
			}
			writeJoinLeaderStubJSON(w, http.StatusOK, internal.APIResponse[cluster.GuestIdentityInventoryReport]{
				Status: "success",
				Data:   admission.Inventory,
			})
		default:
			stubState.recordError("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	leaderIP := startJoinLeaderTLSStub(t, stub)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join", JoinCluster(&auth.Service{DB: db}, clusterService, nil, nil))
	requestBody, err := json.Marshal(JoinClusterRequest{
		NodeID:     localNodeID,
		NodeIP:     "203.0.113.250",
		LeaderIP:   leaderIP,
		ClusterKey: "cluster-secret",
	})
	if err != nil {
		t.Fatalf("marshal join request: %v", err)
	}
	response := performJSONRequest(t, router, http.MethodPost, "/cluster/join", requestBody)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want inventory-change conflict: %s", response.Code, response.Body.String())
	}
	var decoded handlerAPIResponse[cluster.GuestIdentityInventoryReport]
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "joining_inventory_changed_before_start" {
		t.Fatalf("message = %q, want joining_inventory_changed_before_start", decoded.Message)
	}

	paths, admissions, stubErrors := stubState.snapshot()
	if len(stubErrors) != 0 {
		t.Fatalf("leader stub errors: %v", stubErrors)
	}
	if got, want := strings.Join(paths, ","), "/api/health/basic,/api/cluster/accept-join"; got != want {
		t.Fatalf("leader request sequence = %q, want %q", got, want)
	}
	if len(admissions) != 1 || !admissions[0].Preflight {
		t.Fatalf("leader admissions = %+v, want exactly one preflight", admissions)
	}

	var persisted clusterModels.Cluster
	if err := db.First(&persisted, clusterRow.ID).Error; err != nil {
		t.Fatalf("reload cluster sentinel: %v", err)
	}
	if persisted.Enabled || persisted.Key != clusterRow.Key || persisted.RaftIP != clusterRow.RaftIP ||
		persisted.RaftPort != clusterRow.RaftPort {
		t.Fatalf("inventory-change rejection mutated cluster state: got=%+v want=%+v", persisted, clusterRow)
	}
	if clusterService.Raft != nil {
		t.Fatalf("inventory-change rejection initialized Raft: %v", clusterService.Raft)
	}
}
