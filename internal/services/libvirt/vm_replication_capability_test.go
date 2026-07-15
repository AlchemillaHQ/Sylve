// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package libvirt

import (
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestHasEnabledVMFilesystemStorage(t *testing.T) {
	storages := []vmModels.Storage{
		{Type: vmModels.VMStorageTypeZVol, Enable: true},
		{Type: vmModels.VMStorageTypeFilesystem, Enable: false},
	}
	if hasEnabledVMFilesystemStorage(storages) {
		t.Fatal("disabled filesystem storage made the VM replication-ineligible")
	}
	storages = append(storages, vmModels.Storage{Type: vmModels.VMStorageTypeFilesystem, Enable: true})
	if !hasEnabledVMFilesystemStorage(storages) {
		t.Fatal("enabled filesystem storage was not reported")
	}
}
