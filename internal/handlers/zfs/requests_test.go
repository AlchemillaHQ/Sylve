// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
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
		"/?name=tank%2Fone&guid=guid-one&name=%2Ftank%2Ftwo%2F&guid=guid-two&name=tank%2Fone&guid=guid-one&name=tank%2Fone%40snap&guid=snapshot-guid&name=backup%2Fone%40snap&guid=snapshot-guid",
	))
	if err != nil {
		t.Fatalf("datasetDeletionTargetsQuery returned an error: %v", err)
	}
	want := []zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: "tank/one", GUID: "guid-one"},
		{Name: "tank/two", GUID: "guid-two"},
		{Name: "tank/one@snap", GUID: "snapshot-guid"},
		{Name: "backup/one@snap", GUID: "snapshot-guid"},
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

func TestSnapshotNameQuery(t *testing.T) {
	for _, test := range []struct {
		target, want string
		wantErr      bool
	}{
		{target: "/"},
		{target: "/?name=backup%2Freplica%40selected", want: "backup/replica@selected"},
		{target: "/?name=", wantErr: true},
		{target: "/?name=source%40selected&name=replica%40selected", wantErr: true},
	} {
		got, err := snapshotNameQuery(requestContext(test.target))
		if got != test.want || (err != nil) != test.wantErr {
			t.Fatalf("target %q: got %q, %v", test.target, got, err)
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

func TestPoolDeviceRequestsAcceptGUIDsAndLongPaths(t *testing.T) {
	path := "/dev/gptid/71ef7b95-80bb-11ef-b012-00155d123456"
	for _, old := range []string{"da0", "18446744073709551615", path} {
		request := zfsServiceInterfaces.ReplaceDevice{Old: old, New: path}
		if err := binding.Validator.ValidateStruct(&request); err != nil {
			t.Fatalf("replacement %q -> %q rejected: %v", old, path, err)
		}
		attach := zfsServiceInterfaces.AttachDevice{Device: old, New: path}
		if err := binding.Validator.ValidateStruct(&attach); err != nil {
			t.Fatalf("attachment %q -> %q rejected: %v", old, path, err)
		}
		detach := DetachRequest{Device: old}
		if err := binding.Validator.ValidateStruct(&detach); err != nil {
			t.Fatalf("detach target %q rejected: %v", old, err)
		}
	}
	for _, invalid := range []string{"", strings.Repeat("a", 1025)} {
		for _, request := range []zfsServiceInterfaces.AttachDevice{
			{Device: invalid, New: path},
			{Device: "123", New: invalid},
		} {
			if err := binding.Validator.ValidateStruct(&request); err == nil {
				t.Fatalf("invalid attachment request accepted: %#v", request)
			}
		}
		for _, request := range []zfsServiceInterfaces.ReplaceDevice{
			{Old: invalid, New: path},
			{Old: "123", New: invalid},
		} {
			if err := binding.Validator.ValidateStruct(&request); err == nil {
				t.Fatalf("invalid replacement request accepted: %#v", request)
			}
		}
		request := DetachRequest{Device: invalid}
		if err := binding.Validator.ValidateStruct(&request); err == nil {
			t.Fatalf("invalid detach target accepted: %q", invalid)
		}
	}
}

func TestPoolDeviceRequestsPreserveConfirmationPreconditions(t *testing.T) {
	data := []byte(`{"old":"202","new":"/dev/da7","device":"202","expectedNewIdentity":"disk-identity","expectedParentGuid":"1001","expectedMemberGuids":["202","203"]}`)
	var replacement zfsServiceInterfaces.ReplaceDevice
	if err := json.Unmarshal(data, &replacement); err != nil {
		t.Fatal(err)
	}
	if err := binding.Validator.ValidateStruct(&replacement); err != nil {
		t.Fatal(err)
	}
	var detach DetachRequest
	if err := json.Unmarshal(data, &detach); err != nil {
		t.Fatal(err)
	}
	var attach zfsServiceInterfaces.AttachDevice
	if err := json.Unmarshal(data, &attach); err != nil {
		t.Fatal(err)
	}
	if err := binding.Validator.ValidateStruct(&attach); err != nil {
		t.Fatal(err)
	}
	if attach.Device != "202" || attach.New != "/dev/da7" || attach.ExpectedNewIdentity != "disk-identity" || attach.ExpectedParentGUID != "1001" || !reflect.DeepEqual(attach.ExpectedMemberGUIDs, []string{"202", "203"}) {
		t.Fatalf("attachment preconditions lost: %+v", attach)
	}
	if replacement.ExpectedNewIdentity != "disk-identity" || detach.ExpectedParentGUID != "1001" || !reflect.DeepEqual(detach.ExpectedMemberGUIDs, []string{"202", "203"}) {
		t.Fatalf("device preconditions lost: replacement=%+v detach=%+v", replacement, detach)
	}
	for _, options := range []zfsServiceInterfaces.PoolDeviceMutationOptions{
		{ExpectedNewIdentity: strings.Repeat("x", 1025)},
		{ExpectedParentGUID: strings.Repeat("1", 21)},
		{ExpectedMemberGUIDs: []string{strings.Repeat("1", 21)}},
		{ExpectedMemberGUIDs: make([]string, 4097)},
	} {
		request := zfsServiceInterfaces.ReplaceDevice{
			Old: "202", New: "/dev/da7", PoolDeviceMutationOptions: options,
		}
		if err := binding.Validator.ValidateStruct(&request); err == nil {
			t.Fatal("unbounded device preconditions accepted")
		}
		attach := zfsServiceInterfaces.AttachDevice{Device: "202", New: "/dev/da7", PoolDeviceMutationOptions: options}
		if err := binding.Validator.ValidateStruct(&attach); err == nil {
			t.Fatal("unbounded attachment preconditions accepted")
		}
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
