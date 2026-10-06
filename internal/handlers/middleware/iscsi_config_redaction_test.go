// SPDX-License-Identifier: BSD-2-Clause

package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestISCSIConfigAuditRedactsRawAndStructuredPayloads(t *testing.T) {
	secret := `auth-group user { chap user credential-near-token }`
	for _, key := range []string{"extraTargetConfig", "extra_target_config", "EXTRA-TARGET-CONFIG"} {
		payload := sanitizeAuditPayload(map[string]any{key: secret, "applyStatus": "checked"})
		encoded, _ := json.Marshal(payload)
		if strings.Contains(string(encoded), "credential-near-token") || !strings.Contains(string(encoded), "checked") {
			t.Fatal("structured config redaction failed")
		}
	}
	for _, body := range []string{`{"extraTargetConfig":"` + secret + `"}`, `{"extraTargetConfig":"credential-near-token`} {
		db := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("UserID", uint(1))
			c.Set("Username", "admin")
			c.Set("AuthType", "sylve")
		})
		router.Use(RequestLoggerMiddleware(db, nil))
		router.PUT("/api/iscsi/config", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"data": gin.H{"extraTargetConfig": secret}})
		})
		testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", []byte(body))
		var record infoModels.AuditRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatal(err)
		}
		if strings.Contains(record.Action, "credential-near-token") || strings.Contains(record.Action, "auth-group") || !strings.Contains(record.Action, "[REDACTED]") {
			t.Fatal("raw request or response entered the audit record")
		}
	}
}
