// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type streamEcho struct {
	Name   string `json:"name"`
	Issuer string `json:"issuer"`
	Body   string `json:"body"`
}

func TestNodeStreamInternalPassesHeaderRequestAndRawBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cS := cluster.NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*cluster.Service)
	cS.NodeID = "node-a"
	cluster.OnNodeStream(cS, "echo", func(_ context.Context, issuer string, req streamEcho, body io.Reader) (streamEcho, error) {
		data, err := io.ReadAll(body)
		return streamEcho{Name: req.Name, Issuer: issuer, Body: string(data)}, err
	})
	router := gin.New()
	router.POST("/stream/:name", func(c *gin.Context) { c.Set("IssuerNodeID", "node-b") }, NodeStreamInternal(cS))

	request := httptest.NewRequest(http.MethodPost, "/stream/echo", strings.NewReader("zfs stream bytes"))
	request.Header.Set(cluster.CallNodeHeader, "node-a")
	request.Header.Set(cluster.CallIndexHeader, "0")
	request.Header.Set(cluster.CallRequestHeader, `{"name":"disk"}`)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	var response internal.APIResponse[cluster.CallResult]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("status %d, body %s, err %v", recorder.Code, recorder.Body.String(), err)
	}
	var echo streamEcho
	if err := json.Unmarshal(response.Data.Data, &echo); err != nil {
		t.Fatal(err)
	}
	if echo != (streamEcho{Name: "disk", Issuer: "node-b", Body: "zfs stream bytes"}) {
		t.Fatalf("echo = %+v", echo)
	}
}
