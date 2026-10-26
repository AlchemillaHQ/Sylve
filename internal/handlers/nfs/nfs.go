// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfsHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/nfs"
	"github.com/gin-gonic/gin"
)

type ConfigRequest struct {
	Protocol          *string   `json:"protocol"`
	V4MaxVersion      *string   `json:"v4MaxVersion"`
	NumericIDs        *bool     `json:"numericIds"`
	UDP               *bool     `json:"udp"`
	AllowNonroot      *bool     `json:"allowNonroot"`
	BindAddresses     *[]string `json:"bindAddresses"`
	MountdPort        *uint16   `json:"mountdPort"`
	MinThreads        *uint16   `json:"minThreads"`
	MaxThreads        *uint16   `json:"maxThreads"`
	KerberosPrincipal *string   `json:"kerberosPrincipal"`
}

type ShareRequest struct {
	Dataset      string    `json:"dataset"`
	Description  string    `json:"description"`
	Enabled      *bool     `json:"enabled"`
	ReadOnly     *bool     `json:"readOnly"`
	Everyone     *bool     `json:"everyone"`
	Clients      []string  `json:"clients"`
	MaprootUser  *string   `json:"maprootUser"`
	MaprootGroup *string   `json:"maprootGroup"`
	MapallUser   *string   `json:"mapallUser"`
	MapallGroup  *string   `json:"mapallGroup"`
	AllDirs      *bool     `json:"allDirs"`
	Security     *[]string `json:"security"`
}

type MutationResult struct {
	Saved       bool                `json:"saved"`
	Applied     bool                `json:"applied"`
	ApplyStatus string              `json:"applyStatus"`
	Share       *nfsModels.NFSShare `json:"share,omitempty"`
}

func writeError(c *gin.Context, err error) {
	if errors.Is(err, context.Canceled) && errors.Is(c.Request.Context().Err(), context.Canceled) {
		c.Abort()
		return
	}
	status := http.StatusInternalServerError
	message := "Unable to read or save NFS configuration."
	var data any
	var detail *nfs.ValidationError
	switch {
	case errors.Is(err, nfs.ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, nfs.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, nfs.ErrNotFound):
		status = http.StatusNotFound
	}
	if status != http.StatusInternalServerError {
		message = err.Error()
	} else {
		logger.L.Error().Err(err).Msg("nfs_configuration_request_failed")
	}
	if errors.As(err, &detail) {
		data = detail
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: message, Data: data})
}

func bindJSON(c *gin.Context, destination any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(destination)
	if err == nil {
		if err = decoder.Decode(&struct{}{}); errors.Is(err, io.EOF) {
			return true
		}
	}
	status, message := http.StatusBadRequest, "Invalid NFS request. Supply one JSON object with supported fields."
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		status, message = http.StatusRequestEntityTooLarge, "NFS request body is too large."
	}
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: message})
	return false
}

func pathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize-1)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{Status: "error", Message: "Invalid NFS share ID.", Error: "invalid_id"})
		return 0, false
	}
	return uint(id), true
}

func saved(c *gin.Context, code int, service *nfs.Service, share *nfsModels.NFSShare, err error) {
	if err != nil && !errors.Is(err, nfs.ErrApplyFailed) {
		writeError(c, err)
		return
	}
	result := MutationResult{Saved: true, Share: share, ApplyStatus: "pending"}
	message, detail := "nfs_configuration_saved", ""
	if err == nil {
		var status nfs.Status
		status, err = service.GetStatus(c.Request.Context())
		if err == nil {
			result.ApplyStatus = status.ApplyStatus
			result.Applied = status.Managed && (status.ApplyStatus == "applied" || status.ApplyStatus == "idle")
			detail = status.LastError
		}
	}
	if err != nil || detail != "" {
		code, message = http.StatusAccepted, "nfs_configuration_saved_apply_pending"
		if err != nil {
			detail = err.Error()
		}
	}
	c.JSON(code, internal.APIResponse[MutationResult]{
		Status: "success", Message: message, Error: detail, Data: result,
	})
}

func GetConfig(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := service.GetConfig(c.Request.Context())
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[nfsModels.NFSConfig]{Status: "success", Data: config})
	}
}

func SetConfig(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ConfigRequest
		if !bindJSON(c, &request) {
			return
		}
		if request.Protocol == nil || request.V4MaxVersion == nil || request.NumericIDs == nil || request.UDP == nil || request.AllowNonroot == nil || request.BindAddresses == nil || request.MountdPort == nil || request.MinThreads == nil || request.MaxThreads == nil || request.KerberosPrincipal == nil {
			writeError(c, nfs.ErrInvalidRequest)
			return
		}
		err := service.SetConfig(c.Request.Context(), nfsModels.NFSConfig{
			Protocol: *request.Protocol, V4MaxVersion: *request.V4MaxVersion,
			NumericIDs: *request.NumericIDs, UDP: *request.UDP, AllowNonroot: *request.AllowNonroot,
			BindAddresses: *request.BindAddresses, MountdPort: *request.MountdPort,
			MinThreads: *request.MinThreads, MaxThreads: *request.MaxThreads,
			KerberosPrincipal: *request.KerberosPrincipal,
		})
		saved(c, http.StatusOK, service, nil, err)
	}
}

func GetShares(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		shares, err := service.GetShares(c.Request.Context())
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]nfsModels.NFSShare]{Status: "success", Data: shares})
	}
}

func SaveShare(service *nfs.Service, create bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var id uint
		if !create {
			var ok bool
			id, ok = pathID(c)
			if !ok {
				return
			}
		}
		var request ShareRequest
		if !bindJSON(c, &request) {
			return
		}
		// Missing booleans must not turn a partial/old request into read-write or
		// unrestricted access. PUT is a complete replacement, not a partial patch.
		if request.Enabled == nil || request.ReadOnly == nil || request.Everyone == nil {
			writeError(c, nfs.ErrInvalidRequest)
			return
		}
		// Old/incomplete replacements must not silently remove identity restrictions.
		if !create && (request.MaprootUser == nil || request.MaprootGroup == nil || request.MapallUser == nil || request.MapallGroup == nil || request.AllDirs == nil || request.Security == nil) {
			writeError(c, nfs.ErrInvalidRequest)
			return
		}
		value := func(input *string) string {
			if input == nil {
				return ""
			}
			return *input
		}
		var security []string
		if request.Security != nil {
			security = *request.Security
		}
		share, err := service.SaveShare(c.Request.Context(), id, nfsModels.NFSShareConfig{
			Dataset: request.Dataset, Description: request.Description, Enabled: *request.Enabled,
			ReadOnly: *request.ReadOnly, Everyone: *request.Everyone, Clients: request.Clients,
			MaprootUser: value(request.MaprootUser), MaprootGroup: value(request.MaprootGroup),
			MapallUser: value(request.MapallUser), MapallGroup: value(request.MapallGroup),
			AllDirs:  request.AllDirs != nil && *request.AllDirs,
			Security: security,
		})
		status := http.StatusOK
		if create {
			status = http.StatusCreated
		}
		saved(c, status, service, &share, err)
	}
}

func SetShareEnabled(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		var request struct {
			Enabled *bool `json:"enabled"`
		}
		if !bindJSON(c, &request) {
			return
		}
		if request.Enabled == nil {
			writeError(c, nfs.ErrInvalidRequest)
			return
		}
		err := service.SetShareEnabled(c.Request.Context(), id, *request.Enabled)
		saved(c, http.StatusOK, service, nil, err)
	}
}

func DeleteShare(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		err := service.DeleteShare(c.Request.Context(), id)
		saved(c, http.StatusOK, service, nil, err)
	}
}

func GetStatus(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := service.GetStatus(c.Request.Context())
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[nfs.Status]{Status: "success", Data: status})
	}
}

func GetDatasets(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		datasets, err := service.GetDatasets(c.Request.Context())
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]nfs.DatasetOption]{Status: "success", Data: datasets})
	}
}

func PreviewExports(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		preview, err := service.PreviewExports(c.Request.Context())
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[nfs.Preview]{Status: "success", Data: preview})
	}
}

func Apply(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			Confirm *bool `json:"confirm"`
		}
		if !bindJSON(c, &request) {
			return
		}
		if request.Confirm == nil || !*request.Confirm {
			writeError(c, nfs.ErrInvalidRequest)
			return
		}
		err := service.Apply(c.Request.Context())
		saved(c, http.StatusOK, service, nil, err)
	}
}
