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
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func wireGuardErrorResponse(err error, fallbackMessage string) (int, string) {
	switch {
	case errors.Is(err, network.ErrWireGuardServiceDisabled):
		return http.StatusServiceUnavailable, network.ErrWireGuardServiceDisabled.Error()
	case errors.Is(err, network.ErrWireGuardServerNotInited):
		return http.StatusNotFound, network.ErrWireGuardServerNotInited.Error()
	case errors.Is(err, network.ErrWireGuardServerPeerNotFound):
		return http.StatusNotFound, network.ErrWireGuardServerPeerNotFound.Error()
	case errors.Is(err, network.ErrWireGuardClientNotFound):
		return http.StatusNotFound, network.ErrWireGuardClientNotFound.Error()
	case errors.Is(err, network.ErrWireGuardClientPrivateKeyReq):
		return http.StatusBadRequest, network.ErrWireGuardClientPrivateKeyReq.Error()
	default:
		return http.StatusInternalServerError, fallbackMessage
	}
}

func writeWireGuardError(c *gin.Context, err error, fallbackMessage string) {
	statusCode, message := wireGuardErrorResponse(err, fallbackMessage)
	c.JSON(statusCode, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   err.Error(),
		Data:    nil,
	})
}

func GetWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		server, err := svc.GetWireGuardServer()
		if err != nil {
			if errors.Is(err, network.ErrWireGuardServerNotInited) {
				c.JSON(http.StatusOK, internal.APIResponse[any]{
					Status:  "success",
					Message: network.ErrWireGuardServerNotInited.Error(),
					Error:   "",
					Data:    nil,
				})
				return
			}

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

func InitWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.InitWireGuardServerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.InitWireGuardServer(&req); err != nil {
			writeWireGuardError(c, err, "failed_to_initialize_wireguard_server")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_initialized",
			Error:   "",
			Data:    nil,
		})
	}
}

func EditWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.InitWireGuardServerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
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

func ToggleWireGuardServer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.ToggleWireGuardServer(); err != nil {
			writeWireGuardError(c, err, "failed_to_toggle_wireguard_server")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_toggled",
			Error:   "",
			Data:    nil,
		})
	}
}

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

func AddWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.WireGuardServerPeerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.AddWireGuardServerPeer(req); err != nil {
			writeWireGuardError(c, err, "failed_to_add_wireguard_server_peer")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_peer_added",
			Error:   "",
			Data:    nil,
		})
	}
}

func EditWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		peerID, err := utils.ParamUint(c, "peerId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_peer_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var req network.WireGuardServerPeerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
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

func ToggleWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		peerID, err := utils.ParamUint(c, "peerId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_peer_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.ToggleWireGuardServerPeer(peerID); err != nil {
			writeWireGuardError(c, err, "failed_to_toggle_wireguard_server_peer")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_peer_toggled",
			Error:   "",
			Data:    nil,
		})
	}
}

func RemoveWireGuardServerPeer(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		peerID, err := utils.ParamUint(c, "peerId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_peer_id",
				Error:   err.Error(),
				Data:    nil,
			})
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

func RemoveWireGuardServerPeers(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.RemoveWireGuardServerPeersRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.RemoveWireGuardServerPeers(req.IDs); err != nil {
			writeWireGuardError(c, err, "failed_to_remove_wireguard_server_peers")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_server_peers_removed",
			Error:   "",
			Data:    nil,
		})
	}
}

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

func CreateWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req network.WireGuardClientRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.CreateWireGuardClient(&req); err != nil {
			writeWireGuardError(c, err, "failed_to_create_wireguard_client")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_client_created",
			Error:   "",
			Data:    nil,
		})
	}
}

func EditWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID, err := utils.ParamUint(c, "clientId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_client_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var req network.WireGuardClientRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
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

func DeleteWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID, err := utils.ParamUint(c, "clientId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_client_id",
				Error:   err.Error(),
				Data:    nil,
			})
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

func ToggleWireGuardClient(svc *network.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID, err := utils.ParamUint(c, "clientId")
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_client_id",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := svc.ToggleWireGuardClient(clientID); err != nil {
			writeWireGuardError(c, err, "failed_to_toggle_wireguard_client")
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "wireguard_client_toggled",
			Error:   "",
			Data:    nil,
		})
	}
}
