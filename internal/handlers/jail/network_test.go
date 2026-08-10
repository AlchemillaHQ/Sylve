// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type mockJailNetworkService struct {
	setFn    func(uint, bool, bool) (jailServiceInterfaces.JailNetworkInheritanceResult, error)
	addFn    func(uint, jailServiceInterfaces.AddJailNetworkRequest) (*jailModels.Network, error)
	editFn   func(uint, uint, jailServiceInterfaces.EditJailNetworkRequest) (*jailModels.Network, error)
	deleteFn func(uint, uint) error
}

func (m *mockJailNetworkService) SetInheritance(ctID uint, ipv4 bool, ipv6 bool) (jailServiceInterfaces.JailNetworkInheritanceResult, error) {
	if m.setFn != nil {
		return m.setFn(ctID, ipv4, ipv6)
	}
	return jailServiceInterfaces.JailNetworkInheritanceResult{CTID: ctID, InheritIPv4: ipv4, InheritIPv6: ipv6, RemovedNetworkIDs: []uint{}}, nil
}

func (m *mockJailNetworkService) AddNetwork(ctID uint, req jailServiceInterfaces.AddJailNetworkRequest) (*jailModels.Network, error) {
	if m.addFn != nil {
		return m.addFn(ctID, req)
	}
	return &jailModels.Network{ID: 1, JailID: ctID, Name: req.Name}, nil
}

func (m *mockJailNetworkService) EditNetwork(ctID uint, networkID uint, req jailServiceInterfaces.EditJailNetworkRequest) (*jailModels.Network, error) {
	if m.editFn != nil {
		return m.editFn(ctID, networkID, req)
	}
	return &jailModels.Network{ID: networkID, JailID: ctID}, nil
}

func (m *mockJailNetworkService) DeleteNetwork(ctID uint, networkID uint) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctID, networkID)
	}
	return nil
}

func TestJailNetworkErrorStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want int
	}{
		{err: errors.New("invalid_mac"), want: http.StatusBadRequest},
		{err: errors.New("replication_lease_not_owned"), want: http.StatusForbidden},
		{err: errors.New("network_not_found"), want: http.StatusNotFound},
		{err: errors.New("jail_network_change_requires_inactive"), want: http.StatusConflict},
		{err: errors.New("network_service_unavailable"), want: http.StatusServiceUnavailable},
		{err: errors.New("failed_to_write_rc_conf"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		if got := jailNetworkErrorStatus(test.err); got != test.want {
			t.Fatalf("jailNetworkErrorStatus(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestAddJailNetworkHandlerUsesPathCTIDAndReturnsCreatedNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotCTID uint
	router := gin.New()
	router.POST("/jail/:ctid/networks", AddNetwork(&mockJailNetworkService{
		addFn: func(ctID uint, req jailServiceInterfaces.AddJailNetworkRequest) (*jailModels.Network, error) {
			gotCTID = ctID
			return &jailModels.Network{ID: 9, JailID: 3, Name: req.Name, SwitchID: 4, SwitchType: "standard"}, nil
		},
	}))
	response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/jail/42/networks", []byte(`{"name":"lan0","switchName":"LAN"}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", response.Code, response.Body.String())
	}
	if gotCTID != 42 {
		t.Fatalf("service CTID = %d, want 42", gotCTID)
	}
	var envelope internal.APIResponse[jailModels.Network]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.ID != 9 || envelope.Data.Name != "lan0" {
		t.Fatalf("unexpected network response: %+v", envelope.Data)
	}
}

func TestPatchJailNetworkHandlerPreservesExplicitFalseAndDistinctIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/jail/:ctid/networks/:networkId", EditNetwork(&mockJailNetworkService{
		editFn: func(ctID uint, networkID uint, req jailServiceInterfaces.EditJailNetworkRequest) (*jailModels.Network, error) {
			if ctID != 42 || networkID != 7 {
				t.Fatalf("unexpected identities ctid=%d network=%d", ctID, networkID)
			}
			if req.DHCP == nil || *req.DHCP || req.DefaultGateway == nil || *req.DefaultGateway {
				t.Fatalf("explicit false values were not preserved: %+v", req)
			}
			return &jailModels.Network{ID: networkID, JailID: 3, SwitchID: 4, SwitchType: "standard"}, nil
		},
	}))
	response := testutil.PerformJSONRequest(t, router, http.MethodPatch, "/jail/42/networks/7", []byte(`{"dhcp":false,"defaultGateway":false}`))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestSetJailNetworkInheritanceRequiresBothFieldsButAcceptsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/jail/:ctid/network/inheritance", SetNetworkInheritance(&mockJailNetworkService{}))

	missing := testutil.PerformJSONRequest(t, router, http.MethodPut, "/jail/42/network/inheritance", []byte(`{"ipv4":false}`))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("expected missing ipv6 to return 400, got %d", missing.Code)
	}
	valid := testutil.PerformJSONRequest(t, router, http.MethodPut, "/jail/42/network/inheritance", []byte(`{"ipv4":false,"ipv6":false}`))
	if valid.Code != http.StatusOK {
		t.Fatalf("expected explicit false values to return 200, got %d body=%s", valid.Code, valid.Body.String())
	}
}
