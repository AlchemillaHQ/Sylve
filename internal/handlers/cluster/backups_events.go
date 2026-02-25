package clusterHandlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func BackupEvents(zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

func BackupEventByID(zS *zelta.Service) gin.HandlerFunc {
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

func BackupEventProgressByID(zS *zelta.Service) gin.HandlerFunc {
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

func BackupEventsRemote(zS *zelta.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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
