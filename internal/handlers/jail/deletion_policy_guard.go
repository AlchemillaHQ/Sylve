// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/gin-gonic/gin"
)

func RequireJailDeletionDetached(jailService *jail.Service, parameter string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, parameter)
		if !ok {
			c.Abort()
			return
		}
		if err := jailService.RequireJailDeletionDetached(ctID); err != nil {
			status := http.StatusInternalServerError
			message := "replication_policy_delete_check_failed"
			if errors.Is(err, clusterModels.ErrGuestDeleteRequiresReplicationPolicyRemoved) {
				status = http.StatusConflict
				message = "guest_delete_requires_replication_policy_removed"
			} else if errors.Is(err, clusterModels.ErrGuestDeleteRequiresBackupJobsRemoved) {
				status = http.StatusConflict
				message = "guest_delete_requires_backup_jobs_removed"
			}
			c.AbortWithStatusJSON(status, internal.APIResponse[any]{
				Status: "error", Message: message, Error: err.Error(), Data: nil,
			})
			return
		}
		c.Next()
	}
}
