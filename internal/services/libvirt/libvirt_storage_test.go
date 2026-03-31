// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type storageResolveTestDataset struct {
	name       string
	pool       string
	guid       string
	mountpoint string
}

type storageResolveTestZFSRunner struct {
	datasets map[string]storageResolveTestDataset
}

func (r *storageResolveTestZFSRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout,
	_ io.Writer,
	_ string,
	args ...string,
) error {
	if stdout == nil {
		return nil
	}

	target := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "list", "-p", "-j", "-r":
			continue
		case "-o", "-t":
			i++
			continue
		default:
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			target = args[i]
		}
	}

	if target == "" {
		_, err := io.WriteString(stdout, `{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{}}`)
		return err
	}

	ds, ok := r.datasets[target]
	if !ok {
		_, err := io.WriteString(stdout, `{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{}}`)
		return err
	}

	out := fmt.Sprintf(
		`{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{"%s":{"name":"%s","pool":"%s","properties":{"guid":{"value":"%s"},"mountpoint":{"value":"%s"},"used":{"value":"0"},"available":{"value":"0"},"referenced":{"value":"0"},"compressratio":{"value":"1.00x"}}}}}`,
		ds.name,
		ds.name,
		ds.pool,
		ds.guid,
		ds.mountpoint,
	)
	_, err := io.WriteString(stdout, out)
	return err
}

func TestResolveFilesystemSourcePath_LoadsDatasetFromDBWhenRelationNotPreloaded(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{})

	storageDataset := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/shares/projects",
		GUID: "guid-storage-resolve-test",
	}
	if err := db.Create(&storageDataset).Error; err != nil {
		t.Fatalf("failed to seed storage dataset: %v", err)
	}

	svc := &Service{
		DB: db,
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: &storageResolveTestZFSRunner{
				datasets: map[string]storageResolveTestDataset{
					"tank/shares/projects": {
						name:       "tank/shares/projects",
						pool:       "tank",
						guid:       "guid-storage-resolve-test",
						mountpoint: "/tank/shares/projects",
					},
				},
			},
		}),
	}

	storage := vmModels.Storage{
		DatasetID: &storageDataset.ID,
		// Intentionally keep Dataset relation empty to simulate non-preloaded query path.
	}

	sourcePath, err := svc.resolveFilesystemSourcePath(context.Background(), storage)
	if err != nil {
		t.Fatalf("expected filesystem source path resolution to succeed, got: %v", err)
	}

	if sourcePath != "/tank/shares/projects" {
		t.Fatalf("expected mountpoint /tank/shares/projects, got %q", sourcePath)
	}
}
