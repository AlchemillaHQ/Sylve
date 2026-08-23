// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/gin-gonic/gin"
)

func RequireJailReplicationTopologyMutable(jailService *jail.Service, parameter string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, parameter)
		if !ok {
			c.Abort()
			return
		}
		if err := jailService.RequireJailStorageTopologyMutable(ctID); err != nil {
			status := http.StatusInternalServerError
			message := "replication_topology_check_failed"
			if strings.Contains(err.Error(), "replication_run_in_progress") {
				status = http.StatusConflict
				message = "replication_run_in_progress"
			} else if strings.Contains(err.Error(), "replication_storage_topology_change_requires_policy_disabled") {
				status = http.StatusConflict
				message = "replication_storage_topology_change_requires_policy_disabled"
			}
			c.AbortWithStatusJSON(status, internal.APIResponse[any]{
				Status: "error", Message: message, Error: err.Error(), Data: nil,
			})
			return
		}
		c.Next()
	}
}
