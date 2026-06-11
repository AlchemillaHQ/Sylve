// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

func TestNonNumericIDParamReturns400(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.BackupJob{}, &clusterModels.BackupEvent{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationEvent{},
	)
	cS := &cluster.Service{DB: db}
	zS := &zelta.Service{DB: db}

	type testCase struct {
		router *gin.Engine
		method string
		path   string
		body   []byte
	}

	cases := []struct {
		name string
		tc   testCase
	}{
		{"backups target abc", testCase{newBackupsRouter(cS), http.MethodGet, "/cluster/backups/targets/abc/running-job-ids", nil}},
		{"backups target 0", testCase{newBackupsRouter(cS), http.MethodGet, "/cluster/backups/targets/0/running-job-ids", nil}},

		{"backups event abc", testCase{newBackupEventsRouter(cS, zS), http.MethodGet, "/cluster/backups/events/abc", nil}},
		{"backups event 0", testCase{newBackupEventsRouter(cS, zS), http.MethodGet, "/cluster/backups/events/0", nil}},

		{"backups event progress abc", testCase{newBackupEventsRouter(cS, zS), http.MethodGet, "/cluster/backups/events/abc/progress", nil}},
		{"backups event progress 0", testCase{newBackupEventsRouter(cS, zS), http.MethodGet, "/cluster/backups/events/0/progress", nil}},

		{"replication event 0", testCase{newReplicationRouter(cS), http.MethodGet, "/cluster/replication/events/0", nil}},
		{"replication event abc", testCase{newReplicationRouter(cS), http.MethodGet, "/cluster/replication/events/abc", nil}},

		{"replication policy update abc", testCase{newReplicationRouter(cS), http.MethodPut, "/cluster/replication/policies/abc", []byte(`{}`)}},
		{"replication policy update 0", testCase{newReplicationRouter(cS), http.MethodPut, "/cluster/replication/policies/0", []byte(`{}`)}},

		{"replication policy delete abc", testCase{newReplicationRouter(cS), http.MethodDelete, "/cluster/replication/policies/abc", nil}},
		{"replication policy delete 0", testCase{newReplicationRouter(cS), http.MethodDelete, "/cluster/replication/policies/0", nil}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := performJSONRequest(t, c.tc.router, c.tc.method, c.tc.path, c.tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s %s, got %d: %s", c.tc.method, c.tc.path, rr.Code, rr.Body.String())
			}
		})
	}
}
