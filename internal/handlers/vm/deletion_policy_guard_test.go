// SPDX-License-Identifier: BSD-2-Clause

package libvirtHandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestRequireVMDeletionDetachedBlocksDisabledPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &clusterModels.ReplicationPolicy{})
	if err := db.Create(&clusterModels.ReplicationPolicy{
		ID: 91, Name: "disabled-vm-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 811, Enabled: false,
	}).Error; err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}

	service := &libvirt.Service{DB: db}
	router := gin.New()
	called := false
	router.DELETE("/vm/:id", RequireVMDeletionDetached(service, "id"), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/vm/811", nil))
	if response.Code != http.StatusConflict || called {
		t.Fatalf("disabled-policy VM deletion ran: status=%d called=%v", response.Code, called)
	}
}
