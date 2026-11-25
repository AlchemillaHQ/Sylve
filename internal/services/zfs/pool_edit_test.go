// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestPoolPropertiesRequireStorageGuard(t *testing.T) {
	for _, property := range []string{"autoexpand", "autoreplace", "autotrim", "comment", "delegation", "failmode"} {
		t.Run(property, func(t *testing.T) {
			if poolPropertiesRequireStorageGuard(map[string]string{property: "value"}) {
				t.Fatalf("non-disruptive pool property %q requires unused storage", property)
			}
		})
	}
	if poolPropertiesRequireStorageGuard(nil) {
		t.Fatal("an omitted property payload requires a storage guard")
	}
	for _, property := range []string{"readonly", "altroot", "cachefile", "unknown", "Comment", " comment "} {
		t.Run(property, func(t *testing.T) {
			if !poolPropertiesRequireStorageGuard(map[string]string{"comment": "notes", property: "value"}) {
				t.Fatalf("property %q bypassed the storage guard in a mixed edit", property)
			}
		})
	}
}

type editPoolRunner struct {
	setCalls      int
	setProperties map[string]string
}

func (r *editPoolRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, args ...string) error {
	if name != "zpool" || len(args) == 0 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	switch args[0] {
	case "list":
		return json.NewEncoder(stdout).Encode(map[string]any{
			"pools": map[string]any{
				"tank": map[string]any{"name": "tank", "pool_guid": "101", "state": "ONLINE"},
			},
		})
	case "set":
		if len(args) != 3 || args[2] != "tank" {
			return fmt.Errorf("unexpected pool property mutation: %v", args)
		}
		property, value, ok := strings.Cut(args[1], "=")
		if !ok {
			return fmt.Errorf("missing property value: %v", args)
		}
		r.setCalls++
		r.setProperties[property] = value
		return nil
	default:
		return fmt.Errorf("unexpected zpool command: %v", args)
	}
}

func newPoolEditTestService(t *testing.T) (*Service, *editPoolRunner) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{},
		&zfsModels.PoolScrubRun{}, &zfsModels.PoolScrubSchedulerLease{})
	runner := &editPoolRunner{setProperties: make(map[string]string)}
	client := gzfs.NewClient(gzfs.Options{Runner: runner})
	return NewZfsService(database, nil, nil, client).(*Service), runner
}

func addPoolEditDownloadReference(t *testing.T, service *Service) {
	t.Helper()
	if err := service.DB.Create(&utilitiesModels.Downloads{
		DownloadStorage: utilitiesModels.DownloadStorage{StoragePool: "tank", StorageDatasetGUID: "download-guid"},
		UUID:            "pool-edit-download", Path: "payload", URL: "source",
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestEditPoolAllowsNonDisruptiveSettingsWhileStorageIsUsed(t *testing.T) {
	for _, receiving := range []bool{false, true} {
		t.Run(fmt.Sprintf("receiving=%t", receiving), func(t *testing.T) {
			service, runner := newPoolEditTestService(t)
			if receiving {
				defer service.DownloadStorage.ReadLock("tank")()
			} else {
				addPoolEditDownloadReference(t, service)
			}
			topologyCalls := 0
			service.StorageTopologyGuard = func(context.Context, ...string) (func(), error) {
				topologyCalls++
				return nil, errors.New("storage topology is in use")
			}
			for _, props := range []map[string]string{
				{"comment": "Updated notes"},
				{"comment": "Updated notes", "autoexpand": "on", "autotrim": "off",
					"delegation": "on", "autoreplace": "off", "failmode": "wait"},
			} {
				before := runner.setCalls
				if err := service.EditPool(t.Context(), "101", props, nil); err != nil {
					t.Fatalf("non-disruptive settings were blocked: %v", err)
				}
				if topologyCalls != 0 || runner.setCalls-before != len(props) {
					t.Fatalf("edit consulted topology or lost settings: guard=%d sets=%d", topologyCalls, runner.setCalls-before)
				}
				for property, value := range props {
					if runner.setProperties[property] != value {
						t.Fatalf("property %s = %q, want %q", property, runner.setProperties[property], value)
					}
				}
			}
		})
	}
}

func TestEditPoolOtherPropertiesStillProtectDownloadStorage(t *testing.T) {
	for _, receiving := range []bool{false, true} {
		t.Run(fmt.Sprintf("receiving=%t", receiving), func(t *testing.T) {
			service, runner := newPoolEditTestService(t)
			if receiving {
				defer service.DownloadStorage.ReadLock("tank")()
			} else {
				addPoolEditDownloadReference(t, service)
			}
			for _, property := range []string{"readonly", "altroot", "cachefile", "unknown"} {
				props := map[string]string{"comment": "Updated notes", property: "value"}
				if err := service.EditPool(t.Context(), "101", props, nil); !errors.Is(err, downloadstorage.ErrInUse) || !errors.Is(err, ErrConflict) {
					t.Fatalf("property %s bypassed download-storage protection: %v", property, err)
				}
				if runner.setCalls != 0 {
					t.Fatal("a mixed property edit partially applied before storage admission")
				}
			}
		})
	}
}

func TestEditPoolOtherPropertiesStillConsultTopologyGuard(t *testing.T) {
	service, runner := newPoolEditTestService(t)
	guarded := false
	service.StorageTopologyGuard = func(_ context.Context, targets ...string) (func(), error) {
		guarded = true
		if len(targets) != 1 || targets[0] != "tank" {
			t.Fatalf("unexpected topology targets: %v", targets)
		}
		return nil, errors.New("disable NFS first")
	}
	if err := service.EditPool(t.Context(), "101", map[string]string{"cachefile": "none"}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("pool edit bypassed topology coordination: %v", err)
	}
	if !guarded || runner.setCalls != 0 {
		t.Fatalf("guarded=%t setCalls=%d", guarded, runner.setCalls)
	}
}

func TestEditPoolCommentStillProtectsActiveScrubs(t *testing.T) {
	service, runner := newPoolEditTestService(t)
	if err := service.DB.Create(&zfsModels.PoolScrubRun{
		RunKey: "active", PoolGUID: "101", PoolName: "tank", Source: "manual",
		RequestedAt: time.Now().UTC(), Status: "running",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.EditPool(t.Context(), "101", map[string]string{"comment": "notes"}, nil); !errors.Is(err, db.ErrPoolScrubActive) {
		t.Fatalf("comment edit bypassed the active scrub guard: %v", err)
	}
	if runner.setCalls != 0 {
		t.Fatal("comment changed during an active scrub")
	}
}

func TestDeletePoolStillProtectsDownloadStorage(t *testing.T) {
	service, runner := newPoolEditTestService(t)
	addPoolEditDownloadReference(t, service)
	if err := service.DeletePool(t.Context(), "101"); !errors.Is(err, downloadstorage.ErrInUse) || !errors.Is(err, ErrConflict) {
		t.Fatalf("pool deletion bypassed download-storage protection: %v", err)
	}
	if runner.setCalls != 0 {
		t.Fatal("pool settings changed during a refused deletion")
	}
}
