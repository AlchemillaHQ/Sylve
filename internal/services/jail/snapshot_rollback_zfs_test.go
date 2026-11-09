// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func requireImmutable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !isImmutable(info) {
		t.Fatalf("%s isn't system-immutable: %v", path, err)
	}
}

func TestIntegrationJailSnapshotRollbackIgnoresConfigInTheDatasetRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	previousConfig := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{Jails: internal.JailsConfig{DisableDevFS: true}}
	t.Cleanup(func() { config.ParsedConfig = previousConfig })
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	const ctID = 870
	if err := svc.CreateJail(t.Context(), creationSourceRequest(source, pool, ctID)); err != nil {
		t.Fatal(err)
	}
	root, err := client.ZFS.Get(t.Context(), fmt.Sprintf("%s/sylve/jails/%d", pool, ctID), false)
	if err != nil {
		t.Fatal(err)
	}
	sylveDir := filepath.Join(root.Mountpoint, ".sylve")
	requireImmutable(t, sylveDir)
	requireImmutable(t, filepath.Join(sylveDir, "jail.json"))

	setAdditionalOptions := func(value string) {
		t.Helper()
		if err := svc.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", ctID).Update("additional_options", value).Error; err != nil {
			t.Fatal(err)
		}
	}
	setAdditionalOptions("# at snapshot time")
	if err := svc.WriteJailJSON(ctID); err != nil {
		t.Fatal(err)
	}

	// Host config as an older Sylve staged it, or as a jail could slip it in before the snapshot.
	if out, err := exec.Command("chflags", "noschg", sylveDir).CombinedOutput(); err != nil {
		t.Fatalf("chflags: %v: %s", err, out)
	}
	planted := filepath.Join(sylveDir, "host-config")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, fmt.Sprintf("%d.conf", ctID)), []byte("exec.prestart += \"/planted\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const snapshotName = "sjs_legacy_1"
	if _, err := root.Snapshot(t.Context(), snapshotName, true); err != nil {
		t.Fatal(err)
	}
	jail, err := svc.GetJailByCTID(ctID)
	if err != nil {
		t.Fatal(err)
	}
	record := jailModels.JailSnapshot{JailID: jail.ID, CTID: ctID, Name: "legacy", SnapshotName: snapshotName, RootDataset: root.Name}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	setAdditionalOptions("# after the snapshot")
	if _, err := svc.RollbackJailSnapshot(t.Context(), ctID, record.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	restored, err := svc.GetJailByCTID(ctID)
	if err != nil || restored.AdditionalOptions != "# at snapshot time" {
		t.Fatalf("additional options = %q, %v; want the snapshot's", restored.AdditionalOptions, err)
	}
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(filepath.Join(jailsPath, fmt.Sprint(ctID), fmt.Sprintf("%d.conf", ctID)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "# at snapshot time") || strings.Contains(string(conf), "/planted") {
		t.Fatalf("host config wasn't rebuilt from the restored settings:\n%s", conf)
	}
	if _, err := os.Lstat(planted); !os.IsNotExist(err) {
		t.Fatalf("staged host config survived the rollback: %v", err)
	}
	requireImmutable(t, sylveDir)
	requireImmutable(t, filepath.Join(sylveDir, "jail.json"))

	created, err := svc.CreateJailSnapshot(t.Context(), ctID, "fresh", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshotSylve := filepath.Join(root.Mountpoint, ".zfs", "snapshot", created.SnapshotName, ".sylve")
	if _, err := os.Lstat(filepath.Join(snapshotSylve, "host-config")); !os.IsNotExist(err) {
		t.Fatalf("new snapshot carries host config: %v", err)
	}
	requireImmutable(t, filepath.Join(snapshotSylve, "jail.json"))
}
