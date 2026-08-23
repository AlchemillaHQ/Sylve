// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/gin-gonic/gin"
)

func requestContext(target string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c
}

func TestParseOptionalBoolQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		target  string
		want    bool
		wantErr bool
	}{
		{target: "/", want: false},
		{target: "/?recursive=true", want: true},
		{target: "/?recursive=false", want: false},
		{target: "/?recursive=not-a-bool", wantErr: true},
		{target: "/?recursive=", wantErr: true},
		{target: "/?recursive=true&recursive=false", wantErr: true},
	}

	for _, test := range tests {
		got, err := parseOptionalBoolQuery(requestContext(test.target), "recursive")
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("target %q: got (%v, %v), want (%v, error=%v)", test.target, got, err, test.want, test.wantErr)
		}
	}
}

func TestDatasetDeletionTargetsQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	got, err := datasetDeletionTargetsQuery(requestContext(
		"/?name=tank%2Fone&guid=guid-one&name=%2Ftank%2Ftwo%2F&guid=guid-two&name=tank%2Fone&guid=guid-one",
	))
	if err != nil {
		t.Fatalf("datasetDeletionTargetsQuery returned an error: %v", err)
	}
	want := []zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: "tank/one", GUID: "guid-one"},
		{Name: "tank/two", GUID: "guid-two"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("datasetDeletionTargetsQuery = %#v, want %#v", got, want)
	}

	for _, target := range []string{
		"/",
		"/?guid=guid-one",
		"/?name=tank%2Fone",
		"/?name=tank%2Fone&guid=",
		"/?name=tank%2Fone&guid=guid-one&name=tank%2Ftwo",
		"/?name=tank%2Fone&guid=guid-one&name=tank%2Fone&guid=guid-two",
		"/?name=tank%2Fone&guid=guid-one&name=tank%2Ftwo&guid=guid-one",
	} {
		if _, err := datasetDeletionTargetsQuery(requestContext(target)); err == nil {
			t.Fatalf("datasetDeletionTargetsQuery(%q) unexpectedly succeeded", target)
		}
	}
}

func TestPositiveUintPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		raw     string
		want    uint
		wantErr bool
	}{
		{raw: "42", want: 42},
		{raw: "0", wantErr: true},
		{raw: "-1", wantErr: true},
		{raw: "job", wantErr: true},
	} {
		c := requestContext("/")
		c.Params = gin.Params{{Key: "id", Value: test.raw}}
		got, err := positiveUintPath(c, "id")
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("id %q: got (%d, %v), want (%d, error=%v)", test.raw, got, err, test.want, test.wantErr)
		}
	}
}

func TestPoolEditRequestDistinguishesOmittedAndEmptySpares(t *testing.T) {
	var omitted PoolEditRequest
	if err := json.Unmarshal([]byte(`{"properties":{"comment":"updated"}}`), &omitted); err != nil {
		t.Fatalf("decode request with omitted spares: %v", err)
	}
	if omitted.Spares != nil {
		t.Fatalf("omitted spares decoded as %#v, want nil", omitted.Spares)
	}

	var empty PoolEditRequest
	if err := json.Unmarshal([]byte(`{"spares":[]}`), &empty); err != nil {
		t.Fatalf("decode request with empty spares: %v", err)
	}
	if empty.Spares == nil || len(*empty.Spares) != 0 {
		t.Fatalf("explicit empty spares decoded as %#v, want pointer to empty slice", empty.Spares)
	}
}

func TestDeleteSnapshotRejectsInvalidRecursiveBeforeServiceCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/snapshot/:guid", DeleteSnapshot(nil))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/snapshot/123?recursive=invalid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}
