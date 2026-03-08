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
	"strconv"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

type backupTargetZeltaStub struct {
	validateErr   error
	validateCalls []clusterModels.BackupTarget
	removedIDs    []uint
}

var _ backupTargetZelta = (*zelta.Service)(nil)

func (s *backupTargetZeltaStub) ValidateTarget(_ context.Context, target *clusterModels.BackupTarget) error {
	if target != nil {
		s.validateCalls = append(s.validateCalls, *target)
	}
	return s.validateErr
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
	return r
}

func setBackupTargetSaveSSHKeyStub(
	t *testing.T,
	fn func(targetID uint, keyData string) (string, error),
) {
	t.Helper()

	orig := saveBackupTargetSSHKey
	saveBackupTargetSSHKey = fn
	t.Cleanup(func() {
		saveBackupTargetSSHKey = orig
	})
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

	t.Run("save ssh key failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		setBackupTargetSaveSSHKeyStub(t, func(_ uint, _ string) (string, error) {
			return "", errors.New("save_failed")
		})

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", baseBody)
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "save_ssh_key_failed" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("target validation failure removes temporary key", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{validateErr: errors.New("validation_failed")}
		r := newBackupTargetRouter(cS, zStub)

		setBackupTargetSaveSSHKeyStub(t, func(targetID uint, _ string) (string, error) {
			return "/tmp/test-key-" + strconv.FormatUint(uint64(targetID), 10), nil
		})

		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/backups/targets", baseBody)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response json: %v", err)
		}
		if resp.Message != "target_validation_failed" {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if len(zStub.removedIDs) != 1 {
			t.Fatalf("expected temporary key cleanup call, got %d remove call(s)", len(zStub.removedIDs))
		}
	})

	t.Run("propose failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		setBackupTargetSaveSSHKeyStub(t, func(_ uint, _ string) (string, error) {
			return "/tmp/key", nil
		})

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

		setBackupTargetSaveSSHKeyStub(t, func(_ uint, _ string) (string, error) {
			return "/tmp/created-key", nil
		})

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
		if created.SSHKeyPath != "/tmp/created-key" {
			t.Fatalf("expected saved ssh key path, got %q", created.SSHKeyPath)
		}
		if len(zStub.validateCalls) != 1 {
			t.Fatalf("expected one validate call, got %d", len(zStub.validateCalls))
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
			SSHKeyPath: "/tmp/old-key",
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

	t.Run("save ssh key failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		setBackupTargetSaveSSHKeyStub(t, func(_ uint, _ string) (string, error) {
			return "", errors.New("save_failed")
		})

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"target-updated",
			"sshHost":"user@host",
			"sshKey":"new-key",
			"backupRoot":"tank/new"
		}`))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("target validation failure", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{validateErr: errors.New("validate_failed")}
		r := newBackupTargetRouter(cS, zStub)

		setBackupTargetSaveSSHKeyStub(t, func(_ uint, _ string) (string, error) {
			return "/tmp/key-updated", nil
		})

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"target-updated",
			"sshHost":"user@host",
			"sshKey":"new-key",
			"backupRoot":"tank/new"
		}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
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

	t.Run("success", func(t *testing.T) {
		db := newClusterHandlerTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
		cS := &cluster.Service{DB: db}
		target := seedTarget(t, cS)
		zStub := &backupTargetZeltaStub{}
		r := newBackupTargetRouter(cS, zStub)

		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/backups/targets/"+strconv.FormatUint(uint64(target.ID), 10), []byte(`{
			"name":"target-updated",
			"sshHost":"user@host-updated",
			"backupRoot":"tank/new",
			"description":"updated"
		}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}

		var updated clusterModels.BackupTarget
		if err := db.First(&updated, target.ID).Error; err != nil {
			t.Fatalf("failed to fetch updated target: %v", err)
		}
		if updated.Name != "target-updated" || updated.SSHHost != "user@host-updated" || updated.BackupRoot != "tank/new" {
			t.Fatalf("unexpected updated target: %+v", updated)
		}
		if updated.SSHKey != "old-key" {
			t.Fatalf("expected ssh key to remain old-key when no new key provided, got %q", updated.SSHKey)
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
			Enabled:    true,
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
	})
}
