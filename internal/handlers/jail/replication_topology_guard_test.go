// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestRequireJailReplicationTopologyMutableBlocksDestructiveHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &clusterModels.ReplicationPolicy{})
	guest := jailModels.Jail{CTID: 811, Name: "guarded-jail"}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	policy := clusterModels.ReplicationPolicy{
		ID: 11, Name: "jail-policy", GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: guest.CTID,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "0 * * * *", Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	svc := &jail.Service{DB: db}
	router := gin.New()
	called := false
	router.DELETE("/jails/:id/snapshots/:snapshotId",
		RequireJailReplicationTopologyMutable(svc, "id"),
		func(c *gin.Context) { called = true; c.Status(http.StatusNoContent) },
	)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/jails/811/snapshots/1", nil))
	if response.Code != http.StatusConflict || called {
		t.Fatalf("protected destructive handler ran: status=%d called=%v", response.Code, called)
	}
}
