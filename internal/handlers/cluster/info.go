package clusterHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

// @Summary Sync Cluster Health
// @Description Internal endpoint used by the Raft Leader to broadcast cluster health to followers
// @Tags Cluster Internal
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param payload body []clusterServiceInterfaces.NodeHealthSync true "Array of node health states"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /internal/cluster/sync-health [post]
func SyncHealth(clusterService *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload []clusterServiceInterfaces.NodeHealthSync

		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_payload",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := clusterService.SyncClusterHealth(payload); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_sync_health",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "cluster_health_synced",
			Error:   "",
			Data:    nil,
		})
	}
}
