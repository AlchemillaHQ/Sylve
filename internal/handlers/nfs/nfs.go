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

type ShareEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

type ApplyRequest struct {
	Confirm *bool `json:"confirm"`
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

// @Summary Get NFS settings
// @Description Return the saved NFS server settings. Before NFS is configured, the defaults are NFSv3, a maximum NFSv4 version of 4.2, automatic threads and all interfaces.
// @Tags NFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[nfsModels.NFSConfig] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/config [get]
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

// @Summary Update NFS settings
// @Description Validate and save the NFS server settings; every field is required. protocol is v3, v4 or v3v4 and v4MaxVersion is 4.0, 4.1 or 4.2. udp needs NFSv3. Thread bounds are both 0 for automatic, or 1 to 256 with the minimum not above the maximum. bindAddresses takes at most 32 local unicast addresses; empty means all interfaces. When NFS is enabled, the NFS daemons restart, which interrupts connected clients. Validation failures carry field, code and message as data. If the change was saved but could not be applied, the response is 202 with message nfs_configuration_saved_apply_pending and the error.
// @Tags NFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ConfigRequest true "NFS settings"
// @Success 200 {object} internal.APIResponse[MutationResult] "Saved"
// @Success 202 {object} internal.APIResponse[MutationResult] "Saved, apply pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/config [put]
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

// @Summary List NFS shares
// @Description List the NFS share rules ordered by ID. Each rule reports its dataset's current name and mountpoint, a state of active, disabled, dataset_missing or dataset_unmounted, and datasetBlock when new shares refuse its dataset.
// @Tags NFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]nfs.ShareView] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/shares [get]
func GetShares(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		shares, err := service.ShareViews(c.Request.Context())
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]nfs.ShareView]{Status: "success", Data: shares})
	}
}

// @Summary Create an NFS share
// @Description Create an NFS share rule for a ZFS filesystem given by its GUID. enabled, readOnly and everyone are required. clients takes IPv4 or IPv6 addresses and CIDRs, at most 64, and must be empty when everyone is set. security takes sys, krb5, krb5i and krb5p and defaults to sys. A maproot or mapall group needs the matching user, and maproot and mapall cannot be combined. The root file system, system paths, boot environments, Sylve data and datasets containing them are refused with code dataset_not_shareable and a block. Enabled rules on the same dataset must not overlap. Saving an enabled share restarts the NFS daemons. Validation failures carry field, code and message as data. If the change was saved but could not be applied, the response is 202 with message nfs_configuration_saved_apply_pending and the error.
// @Tags NFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ShareRequest true "NFS share"
// @Success 201 {object} internal.APIResponse[MutationResult] "Created"
// @Success 202 {object} internal.APIResponse[MutationResult] "Saved, apply pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/shares [post]
func CreateShare(service *nfs.Service) gin.HandlerFunc {
	return saveShare(service, true)
}

// @Summary Update an NFS share
// @Description Replace an NFS share rule. As well as the fields create requires, maprootUser, maprootGroup, mapallUser, mapallGroup, allDirs and security are required. The dataset is checked like create when the rule is enabled and its access changes, or when its dataset changes. Changing only the description does not restart the NFS daemons. Validation failures carry field, code and message as data. If the change was saved but could not be applied, the response is 202 with message nfs_configuration_saved_apply_pending and the error.
// @Tags NFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Param request body ShareRequest true "NFS share"
// @Success 200 {object} internal.APIResponse[MutationResult] "Updated"
// @Success 202 {object} internal.APIResponse[MutationResult] "Saved, apply pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/shares/{id} [put]
func UpdateShare(service *nfs.Service) gin.HandlerFunc {
	return saveShare(service, false)
}

func saveShare(service *nfs.Service, create bool) gin.HandlerFunc {
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

// @Summary Enable or disable an NFS share
// @Description Enable or disable an NFS share rule. Enabling checks the dataset like create; disabling never needs the dataset. Validation failures carry field, code and message as data. If the change was saved but could not be applied, the response is 202 with message nfs_configuration_saved_apply_pending and the error.
// @Tags NFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Param request body ShareEnabledRequest true "Enabled state"
// @Success 200 {object} internal.APIResponse[MutationResult] "Updated"
// @Success 202 {object} internal.APIResponse[MutationResult] "Saved, apply pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/shares/{id}/enabled [put]
func SetShareEnabled(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		var request ShareEnabledRequest
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

// @Summary Delete an NFS share
// @Description Delete an NFS share rule and apply the remaining rules. If the change was saved but could not be applied, the response is 202 with message nfs_configuration_saved_apply_pending and the error.
// @Tags NFS
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Share ID"
// @Success 200 {object} internal.APIResponse[MutationResult] "Deleted"
// @Success 202 {object} internal.APIResponse[MutationResult] "Deleted, apply pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/shares/{id} [delete]
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

// @Summary Get NFS status
// @Description Report whether NFS is enabled in Sylve and managed by it, the apply status (stopped, pending, applying, starting, applied or idle), the number of enabled rules and exported filesystems, the last apply error and when the runtime was last checked. sharesVersion and configVersion change whenever the saved shares or settings change.
// @Tags NFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[nfs.Status] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/status [get]
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

// @Summary List datasets for NFS shares
// @Description List the filesystems in Sylve's usable pools for the share editor, plus any dataset a share refers to. available is false for datasets new shares cannot use, with reason and code, and block for refused datasets. A saved dataset that no longer exists has code dataset_missing.
// @Tags NFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]nfs.DatasetOption] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/datasets [get]
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

// @Summary Preview NFS exports
// @Description Render the exports file the saved rules would produce, without applying it. A 409 response explains why the rules cannot be exported.
// @Tags NFS
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[nfs.Preview] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/preview [get]
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

// @Summary Apply NFS configuration
// @Description Apply the saved NFS settings and share rules again, restarting the NFS daemons, which interrupts connected clients; confirm must be true. If the apply fails, the response is 202 with message nfs_configuration_saved_apply_pending and the error.
// @Tags NFS
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ApplyRequest true "Confirmation"
// @Success 200 {object} internal.APIResponse[MutationResult] "Applied"
// @Success 202 {object} internal.APIResponse[MutationResult] "Apply pending"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 413 {object} internal.APIResponse[any] "Payload Too Large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /nfs/apply [post]
func Apply(service *nfs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ApplyRequest
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
