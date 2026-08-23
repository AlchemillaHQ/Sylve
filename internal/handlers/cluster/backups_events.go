// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	backupEventDefaultLimit = 200
	backupEventMaxLimit     = 500
	backupEventDefaultPage  = 1
	backupEventDefaultSize  = 25
	backupEventMaxSize      = 100
)

type backupEventListQuery struct {
	NodeID string
	Limit  int
	JobID  uint
}

type backupEventPageQuery struct {
	NodeID    string
	Page      int
	Size      int
	SortField string
	SortDir   string
	JobID     uint
	Search    string
}

func backupEventJobIDQuery(c *gin.Context) (uint, error) {
	raw, present := c.GetQuery("jobId")
	if !present {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed == 0 || parsed > backupJobMaxSafeQueryID {
		return 0, fmt.Errorf("jobId must be a positive JavaScript-safe integer")
	}
	return uint(parsed), nil
}

func backupEventBoundedIntQuery(c *gin.Context, name string, fallback, maximum int) (int, error) {
	raw, present := c.GetQuery(name)
	if !present {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed == 0 || parsed > uint64(maximum) {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return int(parsed), nil
}

func parseBackupEventListQuery(c *gin.Context) (backupEventListQuery, error) {
	query := backupEventListQuery{
		NodeID: strings.TrimSpace(c.Query("nodeId")),
		Limit:  backupEventDefaultLimit,
	}
	var err error
	query.Limit, err = backupEventBoundedIntQuery(
		c, "limit", backupEventDefaultLimit, backupEventMaxLimit,
	)
	if err != nil {
		return query, err
	}
	query.JobID, err = backupEventJobIDQuery(c)
	return query, err
}

func parseBackupEventPageQuery(c *gin.Context) (backupEventPageQuery, error) {
	query := backupEventPageQuery{
		NodeID: strings.TrimSpace(c.Query("nodeId")),
		Page:   backupEventDefaultPage,
		Size:   backupEventDefaultSize,
	}
	var err error
	maxInt := int(^uint(0) >> 1)
	query.Page, err = backupEventBoundedIntQuery(c, "page", backupEventDefaultPage, maxInt)
	if err != nil {
		return query, err
	}
	query.Size, err = backupEventBoundedIntQuery(c, "size", backupEventDefaultSize, backupEventMaxSize)
	if err != nil {
		return query, err
	}
	if query.Page-1 > maxInt/query.Size {
		return query, fmt.Errorf("page and size produce an invalid offset")
	}

	query.JobID, err = backupEventJobIDQuery(c)
	if err != nil {
		return query, err
	}
	query.Search = c.Query("search")

	rawField, fieldPresent := c.GetQuery("sort[0][field]")
	rawDir, dirPresent := c.GetQuery("sort[0][dir]")
	if !fieldPresent {
		if dirPresent {
			return query, fmt.Errorf("sort direction requires a sort field")
		}
		return query, nil
	}

	allowedSortFields := map[string]string{
		"id":              "id",
		"sourceDataset":   "source_dataset",
		"source_dataset":  "source_dataset",
		"targetEndpoint":  "target_endpoint",
		"target_endpoint": "target_endpoint",
		"mode":            "mode",
		"status":          "status",
		"startedAt":       "started_at",
		"started_at":      "started_at",
		"completedAt":     "completed_at",
		"completed_at":    "completed_at",
		"error":           "error",
	}
	query.SortField = allowedSortFields[strings.TrimSpace(rawField)]
	if query.SortField == "" {
		return query, fmt.Errorf("unsupported sort field")
	}
	query.SortDir = "asc"
	if dirPresent {
		query.SortDir = strings.ToLower(strings.TrimSpace(rawDir))
		if query.SortDir != "asc" && query.SortDir != "desc" {
			return query, fmt.Errorf("sort direction must be asc or desc")
		}
	}
	return query, nil
}

func writeBackupEventQueryError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status: "error", Message: "invalid_backup_event_query", Error: err.Error(), Data: nil,
	})
}

func writeBackupEventStorageError(c *gin.Context, operation string, err error) {
	logger.L.Error().Err(err).Str("operation", operation).Msg("backup_event_storage_failed")
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status: "error", Message: operation, Error: operation, Data: nil,
	})
}

// @Summary List Backup Events
// @Description List recent backup events from the local or selected cluster node
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Maximum events (1-500)" default(200)
// @Param jobId query int false "Backup Job ID"
// @Param nodeId query string false "Cluster node ID"
// @Success 200 {object} internal.APIResponse[[]clusterModels.BackupEvent] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Forwarding Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/backups/events [get]
func BackupEvents(cS *clusterService.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := parseBackupEventListQuery(c)
		if err != nil {
			writeBackupEventQueryError(c, err)
			return
		}
		if shouldForwardBackupEventsRequest(cS, query.NodeID) {
			response, err := forwardBackupEventsRequestToNode(c, cS, query.NodeID, "/api/cluster/backups/events")
			if err != nil {
				writeBackupNodeForwardError(
					c, "backup_events_remote_forward_failed", "backup_events_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
			return
		}

		events, err := zS.ListLocalBackupEvents(query.Limit, query.JobID)
		if err != nil {
			writeBackupEventStorageError(c, "list_backup_events_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupEvent]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
}

// @Summary Get a Backup Event
// @Description Get a backup event from the local or selected cluster node
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Event ID"
// @Param nodeId query string false "Cluster node ID"
// @Success 200 {object} internal.APIResponse[clusterModels.BackupEvent] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Event or Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Forwarding Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/backups/events/{id} [get]
func BackupEventByID(cS *clusterService.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_event_id",
				Error:   "invalid_event_id",
				Data:    nil,
			})
			return
		}

		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardBackupEventsRequest(cS, requestedNodeID) {
			path := fmt.Sprintf("/api/cluster/backups/events/%d", id64)
			response, err := forwardBackupEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				writeBackupNodeForwardError(
					c, "backup_event_remote_forward_failed", "backup_events_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
			return
		}

		event, err := zS.GetLocalBackupEvent(uint(id64))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_event_not_found",
					Error:   "backup_event_not_found",
					Data:    nil,
				})
				return
			}
			writeBackupEventStorageError(c, "get_backup_event_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*clusterModels.BackupEvent]{
			Status:  "success",
			Message: "backup_event_fetched",
			Data:    event,
		})
	}
}

// @Summary Get Backup Event Progress
// @Description Get live progress for a backup event from the local or selected cluster node
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Backup Event ID"
// @Param nodeId query string false "Cluster node ID"
// @Success 200 {object} internal.APIResponse[zelta.BackupEventProgress] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Backup Event or Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Forwarding Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/backups/events/{id}/progress [get]
func BackupEventProgressByID(cS *clusterService.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id64 == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_event_id",
				Error:   "invalid_event_id",
				Data:    nil,
			})
			return
		}

		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardBackupEventsRequest(cS, requestedNodeID) {
			path := fmt.Sprintf("/api/cluster/backups/events/%d/progress", id64)
			response, err := forwardBackupEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				writeBackupNodeForwardError(
					c, "backup_event_progress_remote_forward_failed", "backup_events_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
			return
		}

		progress, err := zS.GetBackupEventProgress(c.Request.Context(), uint(id64))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_event_not_found",
					Error:   "backup_event_not_found",
					Data:    nil,
				})
				return
			}
			writeBackupEventStorageError(c, "get_backup_event_progress_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupEventProgress]{
			Status:  "success",
			Message: "backup_event_progress_fetched",
			Data:    progress,
		})
	}
}

// @Summary List Backup Events with Pagination
// @Description List paginated backup events for the remote table from the local or selected cluster node
// @Tags Cluster Backups
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number" default(1)
// @Param size query int false "Page size (1-100)" default(25)
// @Param jobId query int false "Backup Job ID"
// @Param nodeId query string false "Cluster node ID"
// @Param search query string false "Search source, target, status, or error"
// @Param sort[0][field] query string false "Sort field"
// @Param sort[0][dir] query string false "Sort direction (asc or desc)"
// @Success 200 {object} internal.APIResponse[zelta.BackupEventsResponse] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Cluster Node Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Remote Node Failure"
// @Failure 503 {object} internal.APIResponse[any] "Forwarding Service Unavailable"
// @Failure 504 {object} internal.APIResponse[any] "Remote Node Timeout"
// @Router /cluster/backups/events/remote [get]
func BackupEventsRemote(cS *clusterService.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := parseBackupEventPageQuery(c)
		if err != nil {
			writeBackupEventQueryError(c, err)
			return
		}
		if shouldForwardBackupEventsRequest(cS, query.NodeID) {
			response, err := forwardBackupEventsRequestToNode(c, cS, query.NodeID, "/api/cluster/backups/events/remote")
			if err != nil {
				writeBackupNodeForwardError(
					c, "backup_events_remote_forward_failed", "backup_events_node_not_found", err,
				)
				return
			}

			writeClusterForwardResponse(c, response)
			return
		}

		events, err := zS.ListLocalBackupEventsPaginated(
			query.Page,
			query.Size,
			query.SortField,
			query.SortDir,
			query.JobID,
			query.Search,
		)
		if err != nil {
			writeBackupEventStorageError(c, "list_backup_events_failed", err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupEventsResponse]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
}

func shouldForwardBackupEventsRequest(cS *clusterService.Service, requestedNodeID string) bool {
	requestedNodeID = strings.TrimSpace(requestedNodeID)
	if requestedNodeID == "" || cS == nil {
		return false
	}

	localNodeID := ""
	if detail := cS.Detail(); detail != nil {
		localNodeID = strings.TrimSpace(detail.NodeID)
	}

	return localNodeID == "" || requestedNodeID != localNodeID
}

func forwardBackupEventsRequestToNode(
	c *gin.Context,
	cS *clusterService.Service,
	nodeID string,
	path string,
) (clusterForwardResponse, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return clusterForwardResponse{}, err
	}

	query := c.Request.URL.Query()
	query.Del("nodeId")

	remoteURL := fmt.Sprintf("https://%s%s", targetAPI, path)
	if encoded := query.Encode(); encoded != "" {
		remoteURL += "?" + encoded
	}

	return performClusterForward(
		c,
		cS,
		http.MethodGet,
		remoteURL,
		nil,
		clusterForwardShortRead,
	)
}
