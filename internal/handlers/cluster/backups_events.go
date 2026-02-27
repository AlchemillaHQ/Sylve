package clusterHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func BackupEvents(cS *clusterService.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardBackupEventsRequest(cS, requestedNodeID) {
			body, statusCode, err := forwardBackupEventsRequestToNode(c, cS, requestedNodeID, "/api/cluster/backups/events")
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_events_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		limit := 200
		if q := c.Query("limit"); q != "" {
			if parsed, err := strconv.Atoi(q); err == nil {
				limit = parsed
			}
		}

		jobID := uint(0)
		if q := c.Query("jobId"); q != "" {
			if parsed, err := strconv.ParseUint(q, 10, 64); err == nil {
				jobID = uint(parsed)
			}
		}

		events, err := zS.ListLocalBackupEvents(limit, jobID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]clusterModels.BackupEvent]{
			Status:  "success",
			Message: "backup_events_listed",
			Data:    events,
		})
	}
}

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
			body, statusCode, err := forwardBackupEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_event_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "get_backup_event_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*clusterModels.BackupEvent]{
			Status:  "success",
			Message: "backup_event_fetched",
			Data:    event,
		})
	}
}

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
			body, statusCode, err := forwardBackupEventsRequestToNode(c, cS, requestedNodeID, path)
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_event_progress_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
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
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "get_backup_event_progress_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*zelta.BackupEventProgress]{
			Status:  "success",
			Message: "backup_event_progress_fetched",
			Data:    progress,
		})
	}
}

func BackupEventsRemote(cS *clusterService.Service, zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestedNodeID := strings.TrimSpace(c.Query("nodeId"))
		if shouldForwardBackupEventsRequest(cS, requestedNodeID) {
			body, statusCode, err := forwardBackupEventsRequestToNode(c, cS, requestedNodeID, "/api/cluster/backups/events/remote")
			if err != nil {
				c.JSON(http.StatusBadGateway, internal.APIResponse[any]{
					Status:  "error",
					Message: "backup_events_remote_forward_failed",
					Error:   err.Error(),
					Data:    nil,
				})
				return
			}

			c.Data(statusCode, "application/json", body)
			return
		}

		pageStr := c.DefaultQuery("page", "1")
		sizeStr := c.DefaultQuery("size", "25")
		page, _ := strconv.Atoi(pageStr)
		size, _ := strconv.Atoi(sizeStr)

		sortField := c.Query("sort[0][field]")
		sortDir := c.Query("sort[0][dir]")

		jobID := uint(0)
		if q := c.Query("jobId"); q != "" {
			if parsed, err := strconv.ParseUint(q, 10, 64); err == nil {
				jobID = uint(parsed)
			}
		}

		search := c.Query("search")

		events, err := zS.ListLocalBackupEventsPaginated(page, size, sortField, sortDir, jobID, search)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "list_backup_events_failed",
				Error:   err.Error(),
				Data:    nil,
			})
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

func forwardBackupEventsRequestToNode(c *gin.Context, cS *clusterService.Service, nodeID, path string) ([]byte, int, error) {
	targetAPI, err := resolveClusterNodeAPI(cS, nodeID)
	if err != nil {
		return nil, 0, err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := cS.AuthService.CreateClusterJWT(0, hostname, "", "")
	if err != nil {
		return nil, 0, fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	query := c.Request.URL.Query()
	query.Del("nodeId")

	remoteURL := fmt.Sprintf("https://%s%s", targetAPI, path)
	if encoded := query.Encode(); encoded != "" {
		remoteURL += "?" + encoded
	}

	body, statusCode, err := utils.HTTPGetJSONRead(remoteURL, map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	})
	if err != nil {
		return nil, statusCode, err
	}

	return body, statusCode, nil
}
