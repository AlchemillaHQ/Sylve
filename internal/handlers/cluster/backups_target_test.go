// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

type backupTargetZeltaStub struct {
	validateErr      error
	validateFn       func(*clusterModels.BackupTarget) error
	validateCalls    []clusterModels.BackupTarget
	inspection       zelta.BackupTargetValidationResult
	provisionErr     error
	provisionFn      func(*clusterModels.BackupTarget) error
	materializeErr   error
	provisionCalls   []clusterModels.BackupTarget
	materializeCalls []clusterModels.BackupTarget
	removedIDs       []uint
}

var _ backupTargetZelta = (*zelta.Service)(nil)

func (s *backupTargetZeltaStub) ValidateTarget(_ context.Context, target *clusterModels.BackupTarget) error {
	if target != nil {
		s.validateCalls = append(s.validateCalls, *target)
		if s.validateFn != nil {
			return s.validateFn(target)
		}
	}
	return s.validateErr
}

func (s *backupTargetZeltaStub) InspectTargetCandidate(
	ctx context.Context,
	target *clusterModels.BackupTarget,
) (zelta.BackupTargetValidationResult, error) {
	return s.inspection, s.ValidateTarget(ctx, target)
}

func (s *backupTargetZeltaStub) ValidateTargetCandidateReadiness(
	ctx context.Context,
	target *clusterModels.BackupTarget,
) error {
	return s.ValidateTarget(ctx, target)
}

func (s *backupTargetZeltaStub) ValidateTargetReadiness(ctx context.Context, target *clusterModels.BackupTarget) error {
	return s.ValidateTarget(ctx, target)
}

func (s *backupTargetZeltaStub) ProvisionBackupTargetRoot(_ context.Context, target *clusterModels.BackupTarget) error {
	if target != nil {
		s.provisionCalls = append(s.provisionCalls, *target)
		if s.provisionFn != nil {
			return s.provisionFn(target)
		}
	}
	return s.provisionErr
}

func (s *backupTargetZeltaStub) MaterializeBackupTargetSSHKey(target *clusterModels.BackupTarget) error {
	if target != nil {
		s.materializeCalls = append(s.materializeCalls, *target)
	}
	return s.materializeErr
}

func (s *backupTargetZeltaStub) AcquireBackupTargetSSHKey(target *clusterModels.BackupTarget) (func(), error) {
	return func() {}, s.MaterializeBackupTargetSSHKey(target)
}

func (s *backupTargetZeltaStub) RemoveSSHKey(targetID uint) {
	s.removedIDs = append(s.removedIDs, targetID)
}

func newBackupTargetRouter(cS *cluster.Service, zS backupTargetZelta) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/backups/targets", BackupTargets(cS))
	r.POST("/cluster/backups/targets", CreateBackupTarget(cS, zS))
	r.PUT("/cluster/backups/targets/:id", UpdateBackupTarget(cS, zS))
	r.DELETE("/cluster/backups/targets/:id", DeleteBackupTarget(cS, zS))
	r.POST("/cluster/backups/targets/validate/:id", ValidateBackupTarget(cS, zS))
	r.GET("/cluster/backups/targets/:id/readiness", BackupTargetReadiness(cS))
	return r
}

func TestBackupTargetsHandlerGet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		if err := db.Create(&clusterModels.BackupTarget{
			Name:       "z-target",
			SSHHost:    "user@z",
			SSHPort:    22,
			BackupRoot: "tank/z",
			Enabled:    true,
		}).Error; err != nil {
			t.Fatalf("failed to seed target z: %v", err)
		}
		if err := db.Create(&clusterModels.BackupTarget{
			Name:       "a-target",
			SSHHost:    "user@a",
			SSHPort:    22,
			BackupRoot: "tank/a",
			Enabled:    true,
		}).Error; err != nil {
			t.Fatalf("failed to seed target a: %v", err)
		}

		rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/targets", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[[]clusterModels.BackupTarget]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "backup_targets_listed" || len(resp.Data) != 2 {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if resp.Data[0].Name != "a-target" || resp.Data[1].Name != "z-target" {
			t.Fatalf("expected name ordering a-target then z-target, got %q then %q", resp.Data[0].Name, resp.Data[1].Name)
		}
	})

	t.Run("error", func(t *testing.T) {
		db := newClusterHandlerTestDB(t)
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/targets", nil)
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "list_backup_targets_failed" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})
}

func TestBackupTargetsHandlerCreate(t *testing.T) {
	baseBody := []byte(`{
		"name":"target-a",
		"sshHost":"user@host-a",
		"sshPort":22,
		"sshKey":"ssh-key-data",
		"backupRoot":"tank/backups-a",
		"description":"target a",
		"enabled":true
	}`)

	t.Run("invalid request", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", []byte(`{"name":"x"}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("managed private key is required", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", []byte(`{
			"name":"target-a","sshHost":"user@host-a","sshKeyPath":"/external/key",
			"backupRoot":"tank/backups-a"
		}`))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "managed_ssh_key_required") {
			t.Fatalf("expected managed key rejection, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("managed key staging failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{validateErr: errors.New("stage_backup_target_ssh_key_failed: disk full")}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", baseBody)
		if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "save_ssh_key_failed") {
			t.Fatalf("expected staging error, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("target validation failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{validateErr: errors.New("validation_failed")}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", baseBody)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "target_validation_failed") {
			t.Fatalf("expected validation rejection, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("propose failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", []byte(`{
			"name":"bad-target",
			"sshHost":"user@host:22",
			"sshPort":22,
			"backupRoot":"tank/backups"
		}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "backup_target_create_failed" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("success", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", baseBody)
		if rr.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "backup_target_created" {
			t.Fatalf("unexpected response: %+v", resp)
		}

		var created clusterModels.BackupTarget
		if err := db.Where("name = ?", "target-a").First(&created).Error; err != nil {
			t.Fatalf("failed to fetch created backup target: %v", err)
		}
		if created.SSHKeyPath != "" {
			t.Fatalf("expected temporary ssh key path not to be persisted, got %q", created.SSHKeyPath)
		}
		if created.SSHKey != "ssh-key-data" {
			t.Fatalf("expected ssh key material to be persisted, got %q", created.SSHKey)
		}
		if len(zStub.validateCalls) != 1 || zStub.validateCalls[0].ID != 0 ||
			zStub.validateCalls[0].SSHKey != "ssh-key-data" || zStub.validateCalls[0].SSHKeyPath != "" {
			t.Fatalf("unexpected create validation candidate: %+v", zStub.validateCalls)
		}
	})

	t.Run("post-commit key materialization failure is reported without changing committed key", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{materializeErr: errors.New("materialize_target_ssh_key: disk full")}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", baseBody)
		if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "save_ssh_key_failed") {
			t.Fatalf("expected materialization error, got %d body=%s", rr.Code, rr.Body.String())
		}
		var committed clusterModels.BackupTarget
		if err := db.Where("name = ?", "target-a").First(&committed).Error; err != nil {
			t.Fatalf("committed target missing: %v", err)
		}
		if committed.SSHKey != "ssh-key-data" || committed.SSHKeyPath != "" {
			t.Fatalf("committed key changed: %+v", committed)
		}
	})

	t.Run("missing root is durably prepared before provisioning", func(t *testing.T) {
		db := newClusterHandlerTestDB(t,
			&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{}, &clusterModels.BackupJob{},
		)
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{inspection: zelta.BackupTargetValidationResult{RootProvisioningRequired: true}}
		zStub.provisionFn = func(target *clusterModels.BackupTarget) error {
			var pending int64
			if err := db.Model(&clusterModels.BackupTargetProvisionOperation{}).
				Where("target_id = ? AND state = ?", target.ID, clusterModels.BackupTargetProvisionStatePending).
				Count(&pending).Error; err != nil || pending != 1 {
				return errors.New("provision_operation_not_durable")
			}
			var visible int64
			if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Count(&visible).Error; err != nil || visible != 0 {
				return errors.New("target_visible_before_provision_completion")
			}
			return nil
		}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", []byte(`{
			"name":"provisioned","sshHost":"user@host","sshKey":"key",
			"backupRoot":"tank/new","createBackupRoot":true,"enabled":true
		}`))
		if rr.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
		}
		if len(zStub.provisionCalls) != 1 || len(zStub.materializeCalls) != 1 {
			t.Fatalf("provision calls=%+v materialize=%+v", zStub.provisionCalls, zStub.materializeCalls)
		}
		var operation clusterModels.BackupTargetProvisionOperation
		if err := db.First(&operation).Error; err != nil || operation.State != clusterModels.BackupTargetProvisionStateCompleted {
			t.Fatalf("operation=%+v err=%v", operation, err)
		}
	})

	t.Run("definite provisioning failure leaves failed intent and no target", func(t *testing.T) {
		db := newClusterHandlerTestDB(t,
			&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{}, &clusterModels.BackupJob{},
		)
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{
			inspection:   zelta.BackupTargetValidationResult{RootProvisioningRequired: true},
			provisionErr: errors.New("backup_root_create_failed: permission denied"),
		}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", []byte(`{
			"name":"failed","sshHost":"user@host","sshKey":"key",
			"backupRoot":"tank/failed","createBackupRoot":true
		}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
		var operation clusterModels.BackupTargetProvisionOperation
		if err := db.First(&operation).Error; err != nil || operation.State != clusterModels.BackupTargetProvisionStateFailed {
			t.Fatalf("operation=%+v err=%v", operation, err)
		}
		var count int64
		if err := db.Model(&clusterModels.BackupTarget{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("target count=%d err=%v", count, err)
		}
	})

	t.Run("ambiguous provisioning failure remains pending for reconciliation", func(t *testing.T) {
		db := newClusterHandlerTestDB(t,
			&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{}, &clusterModels.BackupJob{},
		)
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{
			inspection: zelta.BackupTargetValidationResult{RootProvisioningRequired: true},
			provisionErr: &zelta.BackupTargetProvisionError{
				Err: errors.New("backup_root_create_verify_failed"), Ambiguous: true,
			},
		}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", []byte(`{
			"name":"pending","sshHost":"user@host","sshKey":"key",
			"backupRoot":"tank/pending","createBackupRoot":true
		}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
		var operation clusterModels.BackupTargetProvisionOperation
		if err := db.First(&operation).Error; err != nil || operation.State != clusterModels.BackupTargetProvisionStatePending {
			t.Fatalf("operation=%+v err=%v", operation, err)
		}
	})
}

func TestBackupTargetsHandlerUpdate(t *testing.T) {
	seedTarget := func(t *testing.T, db any) clusterModels.BackupTarget {
		t.Helper()

		gormDB := db.(*cluster.Service).DB
		target := clusterModels.BackupTarget{
			Name:       "target-old",
			SSHHost:    "user@old-host",
			SSHPort:    22,
			SSHKey:     "old-key",
			BackupRoot: "tank/old",
			Enabled:    true,
		}
		if err := gormDB.Create(&target).Error; err != nil {
			t.Fatalf("failed to seed target: %v", err)
		}
		return target
	}

	t.Run("invalid id", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/abc", []byte(`{}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("invalid request", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/1", []byte(`{"name":"x"}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/999", []byte(`{
			"name":"target-updated",
			"sshHost":"user@host",
			"backupRoot":"tank/new"
		}`))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("legacy target metadata update does not require connectivity or key conversion", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := clusterModels.BackupTarget{
			Name: "legacy", SSHHost: "user@host", SSHPort: 22,
			SSHKeyPath: "/legacy/node-local/key", BackupRoot: "tank/old", Enabled: true,
		}
		if err := db.Create(&target).Error; err != nil {
			t.Fatalf("seed legacy target: %v", err)
		}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"legacy-renamed","sshHost":"user@host","backupRoot":"tank/old"
		}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected metadata update, got %d body=%s", rr.Code, rr.Body.String())
		}
		if len(zStub.validateCalls) != 0 {
			t.Fatalf("metadata update performed connectivity validation: %+v", zStub.validateCalls)
		}
	})

	t.Run("invalid replacement leaves committed key unchanged", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable target: %v", err)
		}
		target.Enabled = false
		zStub := &backupTargetZeltaStub{validateErr: errors.New("validate_failed")}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"target-old","sshHost":"user@old-host","sshKey":"new-key","backupRoot":"tank/old","enabled":false
		}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
		var stored clusterModels.BackupTarget
		if err := db.First(&stored, target.ID).Error; err != nil {
			t.Fatalf("load target after rejected replacement: %v", err)
		}
		if stored.SSHKey != "old-key" || stored.SSHKeyPath != "" {
			t.Fatalf("rejected replacement changed committed identity: %+v", stored)
		}
		if len(zStub.validateCalls) != 1 || zStub.validateCalls[0].ID != target.ID ||
			zStub.validateCalls[0].SSHKey != "new-key" || zStub.validateCalls[0].SSHKeyPath != "" {
			t.Fatalf("replacement validation candidate: %+v", zStub.validateCalls)
		}
	})

	t.Run("key activation failure leaves committed disabled key unchanged", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable target: %v", err)
		}
		zStub := &backupTargetZeltaStub{materializeErr: errors.New("activate_ssh_key: disk full")}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPut,
			"/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
				"name":"target-old","sshHost":"user@old-host","sshKey":"new-key","backupRoot":"tank/old","enabled":false
			}`))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
		}
		var stored clusterModels.BackupTarget
		if err := db.First(&stored, target.ID).Error; err != nil {
			t.Fatalf("load target: %v", err)
		}
		if stored.SSHKey != "old-key" || stored.Enabled {
			t.Fatalf("failed activation changed committed target: %+v", stored)
		}
	})

	t.Run("valid managed key replacement", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable target: %v", err)
		}
		target.Enabled = false
		zStub := &backupTargetZeltaStub{validateFn: func(candidate *clusterModels.BackupTarget) error {
			if candidate.SSHKey != "valid-new-key" {
				return errors.New("wrong_validation_key")
			}
			return nil
		}}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPut,
			"/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
				"name":"target-old","sshHost":"user@old-host","sshKey":"valid-new-key","backupRoot":"tank/old","enabled":false
			}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		var stored clusterModels.BackupTarget
		if err := db.First(&stored, target.ID).Error; err != nil {
			t.Fatalf("load target: %v", err)
		}
		if stored.SSHKey != "valid-new-key" || stored.SSHKeyPath != "" {
			t.Fatalf("valid replacement not committed exactly: %+v", stored)
		}
	})

	t.Run("mutation failure leaves committed managed key usable", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		if err := db.Create(&clusterModels.BackupTarget{
			Name: "name-conflict", SSHHost: "user@other", SSHKey: "other-key", BackupRoot: "tank/other",
		}).Error; err != nil {
			t.Fatalf("seed conflict: %v", err)
		}
		originalKeyDir := zelta.SSHKeyDirectory
		zelta.SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
		t.Cleanup(func() { zelta.SSHKeyDirectory = originalKeyDir })
		if err := os.MkdirAll(zelta.SSHKeyDirectory, 0700); err != nil {
			t.Fatalf("create managed key dir: %v", err)
		}
		canonicalPath, err := zelta.SaveSSHKey(target.ID, "old-key")
		if err != nil {
			t.Fatalf("materialize committed key: %v", err)
		}

		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)
		rr := performJSONRequest(t, r, http.MethodPut,
			"/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
				"name":"name-conflict","sshHost":"user@old-host","backupRoot":"tank/old"
			}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected mutation failure, got %d body=%s", rr.Code, rr.Body.String())
		}
		var stored clusterModels.BackupTarget
		if err := db.First(&stored, target.ID).Error; err != nil {
			t.Fatalf("load target: %v", err)
		}
		keyContent, readErr := os.ReadFile(canonicalPath)
		if stored.SSHKey != "old-key" || readErr != nil || string(keyContent) != "old-key\n" {
			t.Fatalf("failed mutation changed committed key: target=%+v content=%q err=%v", stored, string(keyContent), readErr)
		}
	})

	t.Run("immutable endpoint and root changes are rejected before validation", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)
		path := "/cluster/backups/targets/" + strconv.FormatUint(uint64(target.ID), 10)
		rootChange := performJSONRequest(t, r, http.MethodPut, path, []byte(`{
			"name":"target-old","sshHost":"user@old-host","backupRoot":"tank/other"
		}`))
		if rootChange.Code != http.StatusBadRequest || !strings.Contains(rootChange.Body.String(), "backup_target_root_immutable") {
			t.Fatalf("root change code=%d body=%s", rootChange.Code, rootChange.Body.String())
		}
		endpointChange := performJSONRequest(t, r, http.MethodPut, path, []byte(`{
			"name":"target-old","sshHost":"user@other","backupRoot":"tank/old"
		}`))
		if endpointChange.Code != http.StatusBadRequest || !strings.Contains(endpointChange.Body.String(), "backup_target_endpoint_immutable") {
			t.Fatalf("endpoint change code=%d body=%s", endpointChange.Code, endpointChange.Body.String())
		}
		if len(zStub.validateCalls) != 0 {
			t.Fatalf("immutable edits reached validation: %+v", zStub.validateCalls)
		}
	})

	t.Run("propose failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"target-updated",
			"sshHost":"user@host:22",
			"backupRoot":"tank/new"
		}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "backup_target_update_failed" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("disable succeeds while unreachable and enable requires validation", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{validateErr: errors.New("target_unreachable")}
		r := newBackupTargetRouter(cS, zStub)
		path := "/cluster/backups/targets/" + strconv.FormatUint(uint64(target.ID), 10)
		disable := performJSONRequest(t, r, http.MethodPut, path, []byte(`{
			"name":"target-old","sshHost":"user@old-host","backupRoot":"tank/old","enabled":false
		}`))
		if disable.Code != http.StatusOK || len(zStub.validateCalls) != 0 {
			t.Fatalf("disable code=%d calls=%+v body=%s", disable.Code, zStub.validateCalls, disable.Body.String())
		}
		enable := performJSONRequest(t, r, http.MethodPut, path, []byte(`{
			"name":"target-old","sshHost":"user@old-host","backupRoot":"tank/old","enabled":true
		}`))
		if enable.Code != http.StatusBadRequest || len(zStub.validateCalls) != 1 {
			t.Fatalf("enable code=%d calls=%+v body=%s", enable.Code, zStub.validateCalls, enable.Body.String())
		}
		var stored clusterModels.BackupTarget
		if err := db.First(&stored, target.ID).Error; err != nil || stored.Enabled {
			t.Fatalf("failed enable changed target=%+v err=%v", stored, err)
		}
	})

	t.Run("success", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"target-updated",
			"sshHost":"user@old-host",
			"backupRoot":"tank/old",
			"description":"updated"
		}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}

		var updated clusterModels.BackupTarget
		if err := db.First(&updated, target.ID).Error; err != nil {
			t.Fatalf("failed to fetch updated target: %v", err)
		}
		if updated.Name != "target-updated" || updated.SSHHost != "user@old-host" || updated.BackupRoot != "tank/old" {
			t.Fatalf("unexpected updated target: %+v", updated)
		}
		if updated.SSHKey != "old-key" || updated.SSHKeyPath != "" {
			t.Fatalf("expected managed key to remain node-local when no replacement is provided, got %+v", updated)
		}
		if len(zStub.validateCalls) != 0 {
			t.Fatalf("metadata update performed connectivity validation: %+v", zStub.validateCalls)
		}
	})
}

func TestBackupTargetsHandlerDelete(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/targets/abc", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("propose failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t)
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/targets/1", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "backup_target_delete_failed" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("success and key cleanup", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		target := clusterModels.BackupTarget{
			Name:       "target-delete",
			SSHHost:    "user@delete",
			SSHPort:    22,
			BackupRoot: "tank/delete",
			Enabled:    false,
		}
		if err := db.Create(&target).Error; err != nil {
			t.Fatalf("failed to seed target: %v", err)
		}

		rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}

		if len(zStub.removedIDs) != 1 || zStub.removedIDs[0] != target.ID {
			t.Fatalf("expected RemoveSSHKey to be called with %d, got %#v", target.ID, zStub.removedIDs)
		}

		var count int64
		if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Count(&count).Error; err != nil {
			t.Fatalf("failed to count deleted target: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected target to be deleted, found %d row(s)", count)
		}
	})
}

func TestBackupTargetsHandlerValidateEndpoint(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets/validate/abc", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets/validate/99", nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("validation failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := clusterModels.BackupTarget{
			Name:       "target-validate",
			SSHHost:    "user@validate",
			SSHPort:    22,
			BackupRoot: "tank/validate",
			Enabled:    true,
		}
		if err := db.Create(&target).Error; err != nil {
			t.Fatalf("failed to seed target: %v", err)
		}

		zStub := &backupTargetZeltaStub{validateErr: errors.New("validate_failed")}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets/validate/"+strconv.FormatUint(uint64(target.ID), 10), nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := clusterModels.BackupTarget{
			Name:       "target-validate",
			SSHHost:    "user@validate",
			SSHPort:    22,
			BackupRoot: "tank/validate",
			Enabled:    true,
		}
		if err := db.Create(&target).Error; err != nil {
			t.Fatalf("failed to seed target: %v", err)
		}

		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets/validate/"+strconv.FormatUint(uint64(target.ID), 10), nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "target_validated" {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if len(zStub.validateCalls) != 1 {
			t.Fatalf("expected one validate call, got %d", len(zStub.validateCalls))
		}
		var readiness clusterModels.BackupTargetNodeReadiness
		if err := db.Where("target_id = ?", target.ID).First(&readiness).Error; err != nil {
			t.Fatalf("load readiness: %v", err)
		}
		if !readiness.ValidationSucceeded || readiness.NodeID == "" {
			t.Fatalf("stored readiness: %+v", readiness)
		}

		rr = performJSONRequest(t, r, http.MethodGet,
			"/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10)+"/readiness", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("readiness status=%d body=%s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "targetFingerprint") {
			t.Fatalf("internal target fingerprint leaked in readiness response: %s", rr.Body.String())
		}
		var readinessResponse handlerAPIResponse[[]clusterModels.BackupTargetNodeReadinessStatus]
		decodeErr := json.Unmarshal(rr.Body.Bytes(), &readinessResponse)
		if decodeErr != nil || len(readinessResponse.Data) == 0 || !readinessResponse.Data[0].Ready {
			t.Fatalf("readiness response=%+v err=%v", readinessResponse, decodeErr)
		}
	})
}

func TestRestoreFromTargetEnqueueError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "job already running",
			err:         errors.New("backup_job_already_running"),
			wantStatus:  http.StatusConflict,
			wantMessage: "backup_job_already_running",
		},
		{
			name:        "destination durably reserved",
			err:         errors.New("restore_destination_reserved: dataset=zroot/restored holder=node-a"),
			wantStatus:  http.StatusConflict,
			wantMessage: "restore_destination_already_running",
		},
		{
			name:        "reservation leader unavailable",
			err:         errors.New("leader_not_available"),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "restore_reservation_unavailable",
		},
		{
			name:        "guest ID occupied",
			err:         errors.New("guest_id_already_in_use: 100"),
			wantStatus:  http.StatusConflict,
			wantMessage: "restore_guest_destination_conflict",
		},
		{
			name:        "destination dataset exists",
			err:         errors.New("restore_destination_guest_dataset_exists"),
			wantStatus:  http.StatusConflict,
			wantMessage: "restore_guest_destination_conflict",
		},
		{
			name:        "inventory unavailable",
			err:         errors.New("guest_identity_inventory_unavailable: node offline"),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "restore_guest_identity_unavailable",
		},
		{
			name:        "observability unavailable",
			err:         errors.New("prepare_async_audit_record: database locked"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "restore_observability_unavailable",
		},
		{
			name:        "inventory scan failed",
			err:         errors.New("guest_identity_inventory_scan_failed"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "restore_precheck_failed",
		},
		{
			name:        "destination ZFS check failed",
			err:         errors.New("restore_destination_dataset_check_failed"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "restore_precheck_failed",
		},
		{
			name:        "guest kind mismatch",
			err:         errors.New("restore_guest_destination_kind_mismatch"),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "restore_guest_destination_invalid",
		},
		{
			name:        "non-canonical destination",
			err:         errors.New("restore_guest_destination_must_be_canonical_root"),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "restore_guest_destination_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotMessage := restoreFromTargetEnqueueError(tt.err)
			if gotStatus != tt.wantStatus || gotMessage != tt.wantMessage {
				t.Fatalf(
					"restoreFromTargetEnqueueError() = (%d, %q), want (%d, %q)",
					gotStatus,
					gotMessage,
					tt.wantStatus,
					tt.wantMessage,
				)
			}
		})
	}
}

func TestHasForwardedRestoreResponse(t *testing.T) {
	if !hasForwardedRestoreResponse([]byte(`{"status":"error"}`), http.StatusConflict) {
		t.Fatal("expected remote conflict response to be preserved")
	}
	if hasForwardedRestoreResponse(nil, 0) {
		t.Fatal("transport failure without a response must not be preserved")
	}
}
