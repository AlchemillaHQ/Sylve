// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
)

func TestDirectoryRequestDistinguishesOmittedAndEmptyPermissions(t *testing.T) {
	for _, test := range []struct {
		body    string
		omitted bool
	}{
		{`{"permissions":{"read":{"userIds":[]},"write":{"groupIds":[]}}}`, true},
		{`{"permissions":{"read":{"directorySids":[]},"write":{"directorySids":[]}}}`, false},
	} {
		var input UpdateSambaShareRequest
		if err := json.Unmarshal([]byte(test.body), &input); err != nil {
			t.Fatal(err)
		}
		request := directoryPermissionRequest(input.Permissions)
		if (request == nil) != test.omitted {
			t.Fatalf("wrong omission handling: %+v", request)
		}
		if request != nil && (request.ReadSIDs == nil || request.WriteSIDs == nil) {
			t.Fatal("empty arrays became omitted arrays")
		}
	}
}

func TestShareResponseIncludesDirectoryPrincipalMetadata(t *testing.T) {
	principal := directoryModels.Principal{SID: "S-1-5-21-111-222-333-1100", Name: "EXAMPLE\\alice", Kind: "user", UnixID: 1001100}
	response := mapShareResponse(sambaModels.SambaShare{DirectoryPermissions: directoryModels.SharePermissions{Write: []directoryModels.Principal{principal}}})
	if response.Permissions.Read.Directory == nil || len(response.Permissions.Write.Directory) != 1 || response.Permissions.Write.Directory[0] != principal {
		t.Fatalf("directory metadata was lost: %+v", response.Permissions)
	}
}

func TestShareErrorsMapDirectoryFailures(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{{directoryservices.ErrInvalid, http.StatusBadRequest}, {directoryservices.ErrUnavailable, http.StatusBadGateway}, {directoryservices.ErrConflict, http.StatusConflict}, {directoryservices.ErrBusy, http.StatusConflict}} {
		if status := sambaShareServiceErrorStatus(fmt.Errorf("directory: %w", test.err)); status != test.status {
			t.Fatalf("error %v: status %d", test.err, status)
		}
	}
}

func TestGlobalConfigErrorsMapDirectoryLocks(t *testing.T) {
	for _, err := range []error{directoryservices.ErrBusy, directoryservices.ErrConflict} {
		if status := sambaConfigServiceErrorStatus(fmt.Errorf("directory: %w", err)); status != http.StatusConflict {
			t.Fatalf("error %v: status %d", err, status)
		}
	}
}
