// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

type wireGuardServerHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupWireGuardServerHandlerRouter(t *testing.T, bodyLimit int64, serviceEnabled bool) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{},
	)
	services := []models.AvailableService{}
	if serviceEnabled {
		services = append(services, models.WireGuard)
	}
	if err := db.Create(&models.BasicSettings{Services: services}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := router.Group("/network/wireguard/server")
	if bodyLimit > 0 {
		server.Use(middleware.LimitRequestBody(bodyLimit))
	}
	server.GET("", GetWireGuardServer(svc))
	server.POST("", InitWireGuardServer(svc))
	server.PUT("", EditWireGuardServer(svc))
	server.PATCH("", SetWireGuardServerEnabled(svc))
	server.DELETE("", DeinitWireGuardServer(svc))
	server.POST("/peer", AddWireGuardServerPeer(svc))
	server.PUT("/peer/:peerId", EditWireGuardServerPeer(svc))
	server.PATCH("/peer/:peerId", SetWireGuardServerPeerEnabled(svc))
	server.DELETE("/peer/:peerId", RemoveWireGuardServerPeer(svc))

	clients := router.Group("/network/wireguard/clients")
	if bodyLimit > 0 {
		clients.Use(middleware.LimitRequestBody(bodyLimit))
	}
	clients.GET("", GetWireGuardClients(svc))
	clients.POST("", CreateWireGuardClient(svc))
	clients.PUT("/:clientId", EditWireGuardClient(svc))
	clients.PATCH("/:clientId", SetWireGuardClientEnabled(svc))
	clients.DELETE("/:clientId", DeleteWireGuardClient(svc))
	return router, db
}

func decodeWireGuardServerHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) wireGuardServerHandlerResponse {
	t.Helper()
	var response wireGuardServerHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func seedWireGuardServerHandler(t *testing.T, db *gorm.DB, enabled bool) networkModels.WireGuardServer {
	t.Helper()
	server := networkModels.WireGuardServer{
		Enabled:    true,
		Port:       51820,
		Addresses:  []string{"10.210.0.1/24"},
		PrivateKey: "server-private-key",
		PublicKey:  "server-public-key",
		MTU:        1420,
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatalf("seed wireguard server: %v", err)
	}
	if !enabled {
		if err := db.Model(&server).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable seeded wireguard server: %v", err)
		}
		server.Enabled = false
	}
	return server
}

func seedWireGuardServerPeerHandler(
	t *testing.T,
	db *gorm.DB,
	serverID uint,
	enabled bool,
) networkModels.WireGuardServerPeer {
	t.Helper()
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	peer := networkModels.WireGuardServerPeer{
		Name:              "laptop",
		Enabled:           enabled,
		WireGuardServerID: serverID,
		PrivateKey:        privateKey.String(),
		PublicKey:         privateKey.PublicKey().String(),
		ClientIPs:         []string{"10.210.0.2/32"},
		RoutableIPs:       []string{},
	}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatalf("seed wireguard peer: %v", err)
	}
	if !enabled {
		if err := db.Model(&peer).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable seeded wireguard peer: %v", err)
		}
		peer.Enabled = false
	}
	return peer
}

func wireGuardClientHandlerRequest(t *testing.T, name string, enabled bool) []byte {
	t.Helper()
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	peerPrivateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"name":          name,
		"enabled":       enabled,
		"endpointHost":  "198.51.100.20",
		"endpointPort":  51820,
		"privateKey":    privateKey.String(),
		"peerPublicKey": peerPrivateKey.PublicKey().String(),
		"allowedIPs":    []string{"10.50.0.0/16"},
		"addresses":     []string{"10.250.0.2/32"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestWireGuardServerReadStatusAndKeyResponse(t *testing.T) {
	t.Run("service disabled", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, false)
		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/wireguard/server", nil)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusServiceUnavailable || response.Error != "wireguard_service_disabled" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("server not initialized", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/wireguard/server", nil)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "wireguard_server_not_initialized" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("server and peer private keys returned", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		server := seedWireGuardServerHandler(t, db, true)
		peer := networkModels.WireGuardServerPeer{
			Name:              "laptop",
			Enabled:           true,
			WireGuardServerID: server.ID,
			PrivateKey:        "peer-private-key",
			PublicKey:         "peer-public-key",
			PreSharedKey:      "peer-pre-shared-key",
			ClientIPs:         []string{"10.210.0.2/32"},
		}
		if err := db.Create(&peer).Error; err != nil {
			t.Fatalf("seed wireguard peer: %v", err)
		}

		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/wireguard/server", nil)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusOK || response.Status != "success" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}

		var data map[string]any
		if err := json.Unmarshal(response.Data, &data); err != nil {
			t.Fatalf("decode server data: %v", err)
		}
		if data["privateKey"] != "server-private-key" {
			t.Fatalf("server private key was not preserved in the response: %s", response.Data)
		}
		peers, ok := data["peers"].([]any)
		if !ok || len(peers) != 1 {
			t.Fatalf("unexpected peer response: %s", response.Data)
		}
		peerData, ok := peers[0].(map[string]any)
		if !ok || peerData["privateKey"] != "peer-private-key" {
			t.Fatalf("peer export key was not preserved in the in-memory response: %s", response.Data)
		}
	})
}

func TestWireGuardServerMutationStatusesAndBodyLimits(t *testing.T) {
	t.Run("duplicate initialization is conflict", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		seedWireGuardServerHandler(t, db, true)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/wireguard/server", []byte(`{"port":51820}`))
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusConflict || response.Error != "wireguard_server_already_initialized" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("explicit false state is accepted and idempotent", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		server := seedWireGuardServerHandler(t, db, false)
		rr := performNetworkJSONRequest(t, router, http.MethodPatch, "/network/wireguard/server", []byte(`{"enabled":false}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		var stored networkModels.WireGuardServer
		if err := db.First(&stored, server.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Enabled {
			t.Fatal("explicit false was not preserved")
		}
	})

	t.Run("enabled state is required", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		seedWireGuardServerHandler(t, db, false)
		rr := performNetworkJSONRequest(t, router, http.MethodPatch, "/network/wireguard/server", []byte(`{}`))
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_wireguard_request" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("missing update target", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		rr := performNetworkJSONRequest(t, router, http.MethodPut, "/network/wireguard/server", []byte(`{"port":51820}`))
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "wireguard_server_not_initialized" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("absent delete is idempotent", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/wireguard/server", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("oversized request", func(t *testing.T) {
		const limit = 64
		router, _ := setupWireGuardServerHandlerRouter(t, limit, true)
		body, err := json.Marshal(map[string]any{
			"port":      51820,
			"addresses": []string{strings.Repeat("1", limit)},
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/wireguard/server", body)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusRequestEntityTooLarge || response.Error != "wireguard_request_too_large" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}

func TestWireGuardServerPeerMutationStatusesAndValidation(t *testing.T) {
	t.Run("create returns 201 and peer id", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		seedWireGuardServerHandler(t, db, false)
		body := []byte(`{"name":"laptop","clientIPs":["10.210.0.2/32"]}`)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/wireguard/server/peer", body)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusCreated || response.Status != "success" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		var peerID uint
		if err := json.Unmarshal(response.Data, &peerID); err != nil || peerID == 0 {
			t.Fatalf("invalid created peer id %s: %v", response.Data, err)
		}
	})

	t.Run("invalid private key is bad request", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		seedWireGuardServerHandler(t, db, false)
		body := []byte(`{"name":"laptop","privateKey":"invalid","clientIPs":["10.210.0.2/32"]}`)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/wireguard/server/peer", body)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "wireguard_invalid_peer_private_key" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("zero peer id is rejected", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		body := []byte(`{"name":"laptop","clientIPs":["10.210.0.2/32"]}`)
		rr := performNetworkJSONRequest(t, router, http.MethodPut, "/network/wireguard/server/peer/0", body)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_peer_id" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("explicit false peer state is idempotent", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		server := seedWireGuardServerHandler(t, db, false)
		peer := seedWireGuardServerPeerHandler(t, db, server.ID, true)
		for range 2 {
			rr := performNetworkJSONRequest(
				t,
				router,
				http.MethodPatch,
				fmt.Sprintf("/network/wireguard/server/peer/%d", peer.ID),
				[]byte(`{"enabled":false}`),
			)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
			}
		}
		var stored networkModels.WireGuardServerPeer
		if err := db.First(&stored, peer.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Enabled {
			t.Fatal("peer was not disabled")
		}
	})

	t.Run("missing enabled state is rejected", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		server := seedWireGuardServerHandler(t, db, false)
		peer := seedWireGuardServerPeerHandler(t, db, server.ID, true)
		rr := performNetworkJSONRequest(
			t,
			router,
			http.MethodPatch,
			fmt.Sprintf("/network/wireguard/server/peer/%d", peer.ID),
			[]byte(`{}`),
		)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing peer is not found", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		seedWireGuardServerHandler(t, db, false)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/wireguard/server/peer/999", nil)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusNotFound || response.Error != "wireguard_server_peer_not_found" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("oversized peer request", func(t *testing.T) {
		const limit = 64
		router, _ := setupWireGuardServerHandlerRouter(t, limit, true)
		body := []byte(`{"name":"` + strings.Repeat("x", limit) + `","clientIPs":["10.210.0.2/32"]}`)
		rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/wireguard/server/peer", body)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusRequestEntityTooLarge || response.Error != "wireguard_request_too_large" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}

func TestWireGuardClientStatusesValidationAndKeyResponse(t *testing.T) {
	t.Run("service disabled list", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, false)
		rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/wireguard/clients", nil)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusServiceUnavailable || response.Error != "wireguard_service_disabled" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("create returns 201 and preserves disabled state", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		rr := performNetworkJSONRequest(
			t,
			router,
			http.MethodPost,
			"/network/wireguard/clients",
			wireGuardClientHandlerRequest(t, "office", false),
		)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusCreated || response.Status != "success" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
		var clientID uint
		if err := json.Unmarshal(response.Data, &clientID); err != nil || clientID == 0 {
			t.Fatalf("invalid created client id %s: %v", response.Data, err)
		}

		var stored networkModels.WireGuardClient
		if err := db.First(&stored, clientID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Enabled {
			t.Fatal("explicit disabled state was not preserved")
		}

		rr = performNetworkJSONRequest(t, router, http.MethodGet, "/network/wireguard/clients", nil)
		response = decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusOK || !strings.Contains(string(response.Data), stored.PrivateKey) {
			t.Fatalf("client private key was not preserved in response: status=%d data=%s", rr.Code, response.Data)
		}
	})

	t.Run("duplicate name is conflict", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		for attempt := range 2 {
			rr := performNetworkJSONRequest(
				t,
				router,
				http.MethodPost,
				"/network/wireguard/clients",
				wireGuardClientHandlerRequest(t, "duplicate", false),
			)
			if attempt == 0 && rr.Code != http.StatusCreated {
				t.Fatalf("first create status=%d body=%s", rr.Code, rr.Body.String())
			}
			if attempt == 1 {
				response := decodeWireGuardServerHandlerResponse(t, rr)
				if rr.Code != http.StatusConflict || response.Error != "wireguard_client_name_conflict" {
					t.Fatalf("status=%d response=%+v", rr.Code, response)
				}
			}
		}
	})

	t.Run("explicit state is idempotent", func(t *testing.T) {
		router, db := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		create := performNetworkJSONRequest(
			t,
			router,
			http.MethodPost,
			"/network/wireguard/clients",
			wireGuardClientHandlerRequest(t, "disabled", false),
		)
		response := decodeWireGuardServerHandlerResponse(t, create)
		var clientID uint
		if err := json.Unmarshal(response.Data, &clientID); err != nil {
			t.Fatal(err)
		}

		for range 2 {
			rr := performNetworkJSONRequest(
				t,
				router,
				http.MethodPatch,
				fmt.Sprintf("/network/wireguard/clients/%d", clientID),
				[]byte(`{"enabled":false}`),
			)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		}

		var stored networkModels.WireGuardClient
		if err := db.First(&stored, clientID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Enabled {
			t.Fatal("client was unexpectedly enabled")
		}
	})

	t.Run("invalid id and missing state are rejected", func(t *testing.T) {
		router, _ := setupWireGuardServerHandlerRouter(t, networkService.MaxRequestBodyBytes, true)
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/wireguard/clients/0", nil)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_client_id" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}

		rr = performNetworkJSONRequest(t, router, http.MethodPatch, "/network/wireguard/clients/1", []byte(`{}`))
		response = decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_wireguard_request" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})

	t.Run("oversized request", func(t *testing.T) {
		const limit = 64
		router, _ := setupWireGuardServerHandlerRouter(t, limit, true)
		rr := performNetworkJSONRequest(
			t,
			router,
			http.MethodPost,
			"/network/wireguard/clients",
			[]byte(`{"name":"`+strings.Repeat("x", limit)+`"}`),
		)
		response := decodeWireGuardServerHandlerResponse(t, rr)
		if rr.Code != http.StatusRequestEntityTooLarge || response.Error != "wireguard_request_too_large" {
			t.Fatalf("status=%d response=%+v", rr.Code, response)
		}
	})
}
