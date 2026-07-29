// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"encoding/json"
	"net/http"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestValidateBackupJobSafetyInternalReturnsTypedRunnerReceipt(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{},
	)
	vm := vmModels.VM{RID: 700, Name: "handler-vm"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	dataset := vmModels.VMStorageDataset{
		Pool: "fast", Name: "fast/sylve/virtual-machines/700/disk0", GUID: "handler-guid",
	}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("seed dataset: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeZVol, Pool: "fast", Enable: true, DatasetID: &dataset.ID,
	}).Error; err != nil {
		t.Fatalf("seed storage: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/validation", ValidateBackupJobSafetyInternal(&clusterService.Service{DB: db, NodeID: "node-b"}))
	rr := performJSONRequest(t, router, http.MethodPost, "/validation", []byte(`{
		"expectedNodeId":"node-b",
		"mode":"vm",
		"sourceDataset":"fast/sylve/virtual-machines/700",
		"recursive":true
	}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response handlerAPIResponse[clusterService.BackupJobSafetyValidationResult]
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Data.Valid || response.Data.NodeID != "node-b" ||
		response.Data.GuestID != 700 || response.Data.FriendlySource != "handler-vm" {
		t.Fatalf("response = %+v", response.Data)
	}
}

func TestValidateBackupJobSafetyInternalFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/validation", ValidateBackupJobSafetyInternal(nil))
	rr := performJSONRequest(t, router, http.MethodPost, "/validation", []byte(`{"mode":"dataset"}`))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("nil service status = %d: %s", rr.Code, rr.Body.String())
	}
}
