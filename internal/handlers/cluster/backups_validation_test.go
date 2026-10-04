// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/remoteexec"
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

func TestValidateBackupTargetInternalReturnsNodeBoundReceipt(t *testing.T) {
	service, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()
	db := service.DB
	target := clusterModels.BackupTarget{
		ID: 9, Name: "target", SSHHost: "root@backup", SSHPort: 22, BackupRoot: "tank/backups",
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	seedBackupTargetHostTrust(t, db, &target)
	service.NodeID = "node-b"
	service.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error { return nil })
	request := clusterService.BackupTargetValidationRequest{
		HostKeyRevision: 1,
		ExpectedNodeID:  "node-b", TargetID: target.ID,
		TargetFingerprint: clusterModels.BackupTargetConnectivityFingerprint(&target),
	}
	body, _ := json.Marshal(request)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	future := service.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatal(err)
	}
	router.Use(func(c *gin.Context) { c.Set("IssuerNodeID", string(future.Configuration().Servers[0].ID)) })
	router.POST("/target-validation", ValidateBackupTargetInternal(service))
	rr := performJSONRequest(t, router, http.MethodPost, "/target-validation", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var response handlerAPIResponse[clusterModels.BackupTargetNodeReadinessUpdate]
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Data.ValidationSucceeded || response.Data.NodeID != "node-b" ||
		response.Data.TargetID != target.ID {
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

func TestInternalHostKeyInstallRequiresMemberAndCannotReplaceTrust(t *testing.T) {
	service, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()
	target := clusterModels.BackupTarget{ID: 9, Name: "backup", SSHHost: "root@backup", BackupRoot: "tank/backups"}
	if err := service.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.InitializeBackupTargetHostTrust(); err != nil {
		t.Fatal(err)
	}
	trust, err := clusterModels.GetBackupTargetSSHHostTrust(service.DB, &target)
	if err != nil {
		t.Fatal(err)
	}
	configuration := service.Raft.GetConfiguration()
	if err := configuration.Error(); err != nil {
		t.Fatal(err)
	}
	member := string(configuration.Configuration().Servers[0].ID)
	key := testutil.SSHHostKey(t)
	for _, scenario := range []struct {
		name, issuer string
		revision     uint64
		want         int
	}{
		{"no issuer", "", 1, 403}, {"removed issuer", "removed", 1, 403},
		{"no revision", member, 0, 400}, {"install", member, 1, 200}, {"replace", member, 2, 409},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("IssuerNodeID", scenario.issuer) })
			router.POST("/install", InstallBackupTargetHostKeyInternal(service))
			change := clusterModels.BackupTargetSSHHostTrustChange{TargetID: target.ID, EndpointFingerprint: trust.EndpointFingerprint, ExpectedRevision: scenario.revision, PublicKey: key}
			if scenario.name == "replace" {
				change.PublicKey = testutil.SSHHostKey(t)
			}
			body, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			response := performJSONRequest(t, router, http.MethodPost, "/install", body)
			if response.Code != scenario.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, scenario.want, response.Body.String())
			}
			current, err := clusterModels.GetBackupTargetSSHHostTrust(service.DB, &target)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.name == "install" {
				var receipt handlerAPIResponse[clusterService.BackupTargetHostKeyInstallResult]
				if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
					t.Fatal(err)
				}
				fingerprint, _ := remoteexec.SSHHostKeyFingerprint(key)
				if current.PublicKey != key || receipt.Data.Fingerprint != fingerprint || receipt.Data.Revision != 2 || receipt.Data.AppliedIndex == 0 {
					t.Fatalf("install receipt=%+v trust=%+v", receipt.Data, current)
				}
			} else if scenario.name == "replace" {
				if current.PublicKey != key || current.Revision != 2 || !strings.Contains(response.Body.String(), "install_conflict") {
					t.Fatal("internal route replaced an approved key")
				}
			} else if current.PublicKey != "" || current.Revision != 1 {
				t.Fatal("rejected issuer changed trust")
			}
		})
	}
}
