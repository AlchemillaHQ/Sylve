// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func wireGuardErrorResponse(err error, fallbackMessage string) (int, string) {
	switch {
	case errors.Is(err, network.ErrWireGuardServiceDisabled):
		return http.StatusServiceUnavailable, network.WireGuardErrorCode(err)
	case errors.Is(err, network.ErrWireGuardServerNotInited):
		return http.StatusNotFound, network.WireGuardErrorCode(err)
	case errors.Is(err, network.ErrWireGuardServerPeerNotFound):
		return http.StatusNotFound, network.WireGuardErrorCode(err)
	case errors.Is(err, network.ErrWireGuardClientNotFound):
		return http.StatusNotFound, network.WireGuardErrorCode(err)
	case errors.Is(err, network.ErrInvalidWireGuardServer),
		errors.Is(err, network.ErrInvalidWireGuardClient),
		errors.Is(err, network.ErrWireGuardClientPrivateKeyReq),
		errors.Is(err, network.ErrWireGuardEndpointHostRequired),
		errors.Is(err, network.ErrWireGuardEndpointPortInvalid),
		errors.Is(err, network.ErrWireGuardAllowedIPsRequired),
		errors.Is(err, network.ErrWireGuardAddressesRequired),
		errors.Is(err, network.ErrWireGuardClientIPsRequired),
		errors.Is(err, network.ErrWireGuardPeerPublicKeyRequired):
		return http.StatusBadRequest, network.WireGuardErrorCode(err)
	case errors.Is(err, network.ErrWireGuardServerAlreadyInited),
		errors.Is(err, network.ErrWireGuardServerConflict),
		errors.Is(err, network.ErrWireGuardClientConflict):
		return http.StatusConflict, network.WireGuardErrorCode(err)
	default:
		return http.StatusInternalServerError, fallbackMessage
	}
}

func writeWireGuardError(c *gin.Context, err error, fallbackMessage string) {
	statusCode, code := wireGuardErrorResponse(err, fallbackMessage)
	message := fallbackMessage
	if statusCode != http.StatusInternalServerError {
		message = code
	} else {
		logger.L.Error().Err(err).Str("operation", fallbackMessage).Msg("wireguard_request_failed")
		code = network.WireGuardErrorCode(err)
	}
	c.JSON(statusCode, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   code,
		Data:    nil,
	})
}

func bindWireGuardJSON(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status:  "error",
				Message: "wireguard_request_too_large",
				Error:   "wireguard_request_too_large",
				Data:    nil,
			})
			return false
		}

		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_request",
			Error:   "invalid_wireguard_request",
			Data:    nil,
		})
		return false
	}
	return true
}

func wireGuardPeerID(c *gin.Context) (uint, bool) {
	peerID, err := utils.ParamUint(c, "peerId")
	if err != nil || peerID == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_peer_id",
			Error:   "invalid_peer_id",
			Data:    nil,
		})
		return 0, false
	}
	return peerID, true
}

func wireGuardClientID(c *gin.Context) (uint, bool) {
	clientID, err := utils.ParamUint(c, "clientId")
	if err != nil || clientID == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_client_id",
			Error:   "invalid_client_id",
			Data:    nil,
		})
		return 0, false
	}
	return clientID, true
}

// @Summary Get WireGuard server
// @Description Retrieve the initialized WireGuard server configuration and runtime status
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[networkModels.WireGuardServer] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server [get]
func GetWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		server, err := svc.GetWireGuardServer()
		if err != nil {
			writeWireGuardError(c, err, "failed_to_get_wireguard_server")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*networkModels.WireGuardServer]{
			Status:  "success",
			Message: "wireguard_server_retrieved",
			Error:   "",
			Data:    server,
		})
	}
}

// @Summary Initialize WireGuard server
// @Description Create and start the singleton WireGuard server
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body network.InitWireGuardServerRequest true "WireGuard Server Request"
// @Success 201 {object} internal.APIResponse[any] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server [post]
func InitWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.InitWireGuardServerRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		if err := svc.InitWireGuardServer(&req); err != nil {
			writeWireGuardError(c, err, "failed_to_initialize_wireguard_server")
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_initialized",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Update WireGuard server
// @Description Update the configurable WireGuard server settings and apply them
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body network.InitWireGuardServerRequest true "WireGuard Server Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server [put]
func EditWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.InitWireGuardServerRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		if err := svc.EditWireGuardServer(req); err != nil {
			writeWireGuardError(c, err, "failed_to_edit_wireguard_server")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_edited",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Set WireGuard server state
// @Description Enable or disable the initialized WireGuard server idempotently
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body network.WireGuardServerEnabledRequest true "WireGuard Server State"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server [patch]
func SetWireGuardServerEnabled(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.WireGuardServerEnabledRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		if err := svc.SetWireGuardServerEnabled(*req.Enabled); err != nil {
			writeWireGuardError(c, err, "failed_to_set_wireguard_server_state")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_state_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Deinitialize WireGuard server
// @Description Stop and permanently remove the WireGuard server and its peers
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server [delete]
func DeinitWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.DeinitWireGuardServer(); err != nil {
			writeWireGuardError(c, err, "failed_to_deinitialize_wireguard_server")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_deinitialized",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Add WireGuard server peer
// @Description Create a peer for the initialized WireGuard server and apply the updated runtime configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body network.WireGuardServerPeerRequest true "WireGuard Server Peer Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server/peer [post]
func AddWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.WireGuardServerPeerRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		peerID, err := svc.AddWireGuardServerPeer(req)
		if err != nil {
			writeWireGuardError(c, err, "failed_to_add_wireguard_server_peer")
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "wireguard_server_peer_added",
			Error:   "",
			Data:    peerID,
		})
	}
}

// @Summary Update WireGuard server peer
// @Description Replace the configurable settings for a WireGuard server peer and apply the updated runtime configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param peerId path int true "WireGuard Server Peer ID" minimum(1)
// @Param request body network.WireGuardServerPeerRequest true "WireGuard Server Peer Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server/peer/{peerId} [put]
func EditWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		peerID, ok := wireGuardPeerID(c)
		if !ok {
			return
		}

		var req network.WireGuardServerPeerRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		req.ID = &peerID
		if err := svc.EditWireGuardServerPeer(req); err != nil {
			writeWireGuardError(c, err, "failed_to_edit_wireguard_server_peer")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_peer_edited",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Set WireGuard server peer state
// @Description Enable or disable a WireGuard server peer idempotently and apply the updated runtime configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param peerId path int true "WireGuard Server Peer ID" minimum(1)
// @Param request body network.WireGuardServerPeerEnabledRequest true "WireGuard Server Peer State"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server/peer/{peerId} [patch]
func SetWireGuardServerPeerEnabled(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		peerID, ok := wireGuardPeerID(c)
		if !ok {
			return
		}

		var req network.WireGuardServerPeerEnabledRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		if err := svc.SetWireGuardServerPeerEnabled(peerID, *req.Enabled); err != nil {
			writeWireGuardError(c, err, "failed_to_set_wireguard_server_peer_state")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_peer_state_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Remove WireGuard server peer
// @Description Permanently remove a WireGuard server peer and apply the updated runtime configuration
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param peerId path int true "WireGuard Server Peer ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/server/peer/{peerId} [delete]
func RemoveWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		peerID, ok := wireGuardPeerID(c)
		if !ok {
			return
		}

		if err := svc.RemoveWireGuardServerPeer(peerID); err != nil {
			writeWireGuardError(c, err, "failed_to_remove_wireguard_server_peer")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_peer_removed",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary List WireGuard clients
// @Description Retrieve all configured outbound WireGuard clients and their runtime status
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]networkModels.WireGuardClient] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/clients [get]
func GetWireGuardClients(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clients, err := svc.GetWireGuardClients()
		if err != nil {
			writeWireGuardError(c, err, "failed_to_get_wireguard_clients")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]networkModels.WireGuardClient]{
			Status:  "success",
			Message: "wireguard_clients_retrieved",
			Error:   "",
			Data:    clients,
		})
	}
}

// @Summary Create WireGuard client
// @Description Create and apply an outbound WireGuard client configuration
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body network.WireGuardClientRequest true "WireGuard Client Request"
// @Success 201 {object} internal.APIResponse[uint] "Created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/clients [post]
func CreateWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.WireGuardClientRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		clientID, err := svc.CreateWireGuardClient(&req)
		if err != nil {
			writeWireGuardError(c, err, "failed_to_create_wireguard_client")
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[uint]{
			Status:  "success",
			Message: "wireguard_client_created",
			Error:   "",
			Data:    clientID,
		})
	}
}

// @Summary Update WireGuard client
// @Description Replace the configurable settings of an outbound WireGuard client and apply them
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param clientId path int true "WireGuard Client ID" minimum(1)
// @Param request body network.WireGuardClientRequest true "WireGuard Client Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/clients/{clientId} [put]
func EditWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID, ok := wireGuardClientID(c)
		if !ok {
			return
		}

		var req network.WireGuardClientRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		req.ID = &clientID
		if err := svc.EditWireGuardClient(&req); err != nil {
			writeWireGuardError(c, err, "failed_to_edit_wireguard_client")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_client_edited",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Set WireGuard client state
// @Description Idempotently enable or disable an outbound WireGuard client
// @Tags Network
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param clientId path int true "WireGuard Client ID" minimum(1)
// @Param request body network.WireGuardClientEnabledRequest true "WireGuard Client State"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/clients/{clientId} [patch]
func SetWireGuardClientEnabled(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID, ok := wireGuardClientID(c)
		if !ok {
			return
		}

		var req network.WireGuardClientEnabledRequest
		if !bindWireGuardJSON(c, &req) {
			return
		}

		if err := svc.SetWireGuardClientEnabled(clientID, *req.Enabled); err != nil {
			writeWireGuardError(c, err, "failed_to_set_wireguard_client_state")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_client_state_updated",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete WireGuard client
// @Description Permanently remove an outbound WireGuard client and its runtime interface
// @Tags Network
// @Produce json
// @Security BearerAuth
// @Param clientId path int true "WireGuard Client ID" minimum(1)
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /network/wireguard/clients/{clientId} [delete]
func DeleteWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID, ok := wireGuardClientID(c)
		if !ok {
			return
		}

		if err := svc.DeleteWireGuardClient(clientID); err != nil {
			writeWireGuardError(c, err, "failed_to_delete_wireguard_client")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_client_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}
