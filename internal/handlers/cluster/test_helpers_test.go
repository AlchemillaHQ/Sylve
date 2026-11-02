// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http/httptest"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type handlerAPIResponse[T any] struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    T      `json:"data"`
	Error   string `json:"error"`
}

func newClusterHandlerTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()
	migrateModels = append(
		migrateModels,
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupTargetRestoreOperation{},
		&clusterModels.BackupTargetNodeReadiness{},
		&clusterModels.BackupTargetSSHHostTrust{},
		&clusterModels.ClusterOption{},
		&clusterModels.ReplicationTransitionEvent{},
	)
	db := testutil.NewSQLiteTestDB(t, migrateModels...)
	if err := containerModels.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func performJSONRequest(t *testing.T, r *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	return testutil.PerformJSONRequest(t, r, method, path, body)
}

func seedBackupTargetHostTrust(t *testing.T, database *gorm.DB, target *clusterModels.BackupTarget) {
	t.Helper()
	endpoint, err := clusterModels.BackupTargetSSHEndpointFingerprint(target)
	if err != nil {
		t.Fatal(err)
	}
	change := clusterModels.BackupTargetSSHHostTrustChange{TargetID: target.ID, EndpointFingerprint: endpoint,
		PublicKey: testutil.SSHHostKey(t), OccurredAt: time.Now().UTC()}
	if err := clusterModels.ApplyBackupTargetSSHHostTrustTxn(database, "initialize_v1", &change); err != nil {
		t.Fatal(err)
	}
}
