// SPDX-License-Identifier: BSD-2-Clause

package libvirtHandlers

import (
	"net/http"

	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func RequireVMReplicationTopologyMutable(libvirtService *libvirt.Service, parameter string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, err := utils.ParamUint(c, parameter)
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if err := libvirtService.RequireVMStorageTopologyMutable(rid); err != nil {
			writeVMStorageTopologyGuardError(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}
