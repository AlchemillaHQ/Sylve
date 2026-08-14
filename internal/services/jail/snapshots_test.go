// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type jailSnapshotRunnerDataset struct {
	datasetType gzfs.DatasetType
	mountPoint  string
	pool        string
}

type jailSnapshotRunner struct {
	datasets    map[string]jailSnapshotRunnerDataset
	commands    [][]string
	sawCanceled bool
}

func (r *jailSnapshotRunner) Run(
	ctx context.Context,
	_ io.Reader,
	stdout io.Writer,
	_ io.Writer,
	name string,
	args ...string,
) error {
	if ctx.Err() != nil {
		r.sawCanceled = true
	}
	r.commands = append(r.commands, append([]string{name}, args...))
	if name != "zfs" || len(args) == 0 {
		return fmt.Errorf("unsupported command: %s %v", name, args)
	}

	switch args[0] {
	case "list":
		target := jailSnapshotTargetArg(args)
		recursive := slicesContainString(args, "-r")
		datasets := make(map[string]any)
		for datasetName, dataset := range r.datasets {
			if target != "" && datasetName != target &&
				(!recursive || (!strings.HasPrefix(datasetName, target+"/") &&
					!strings.HasPrefix(datasetName, target+"@"))) {
				continue
			}
			datasets[datasetName] = jailSnapshotDatasetJSON(datasetName, dataset)
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"datasets": datasets})
	case "destroy", "mount", "rollback":
		return nil
	default:
		return fmt.Errorf("unsupported zfs args: %v", args)
	}
}

func jailSnapshotTargetArg(args []string) string {
	target := ""
	skip := 0
	for index, arg := range args {
		if index == 0 {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		switch arg {
		case "-o", "-t":
			skip = 1
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		target = arg
	}
	return target
}

func slicesContainString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func jailSnapshotDatasetJSON(name string, dataset jailSnapshotRunnerDataset) map[string]any {
	mountPoint := dataset.mountPoint
	if mountPoint == "" {
		mountPoint = "/" + strings.SplitN(name, "@", 2)[0]
	}
	pool := dataset.pool
	if pool == "" {
		pool = strings.SplitN(name, "/", 2)[0]
	}

	return map[string]any{
		"name": name,
		"pool": pool,
		"type": dataset.datasetType,
		"properties": map[string]any{
			"guid":          map[string]any{"value": "1"},
			"mountpoint":    map[string]any{"value": mountPoint},
			"used":          map[string]any{"value": "0"},
			"available":     map[string]any{"value": "0"},
			"referenced":    map[string]any{"value": "0"},
			"compressratio": map[string]any{"value": "1.00x"},
		},
	}
}

func newJailSnapshotTestService(runner *jailSnapshotRunner) *Service {
	return &Service{GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}
}

func TestDetachedJailSnapshotContextSurvivesCanceledParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := detachedJailSnapshotContext(parent, time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("detached context inherited parent cancellation: %v", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("detached context is missing its bounded deadline")
	}
}

func TestCollectJailSnapshotTargetsUsesExactRecursiveSnapshotAndDeepestFirstOrder(t *testing.T) {
	const root = "zroot/sylve/jails/420"
	const snapshot = "sjs_before_upgrade_1"
	runner := &jailSnapshotRunner{datasets: map[string]jailSnapshotRunnerDataset{
		root + "@" + snapshot:                {datasetType: gzfs.DatasetTypeSnapshot},
		root + "/data@" + snapshot:           {datasetType: gzfs.DatasetTypeSnapshot},
		root + "/data/nested@" + snapshot:    {datasetType: gzfs.DatasetTypeSnapshot},
		root + "/data@" + snapshot + "-copy": {datasetType: gzfs.DatasetTypeSnapshot},
		root + "@unrelated":                  {datasetType: gzfs.DatasetTypeSnapshot},
	}}

	targets, missing, err := newJailSnapshotTestService(runner).
		collectJailSnapshotTargets(context.Background(), root, snapshot, true)
	if err != nil {
		t.Fatalf("collect targets: %v", err)
	}
	if missing {
		t.Fatal("root snapshot was incorrectly reported missing")
	}
	want := []string{
		root + "/data/nested@" + snapshot,
		root + "/data@" + snapshot,
		root + "@" + snapshot,
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
}

func TestCollectJailSnapshotTargetsAllowsMissingRootForDeleteReconciliation(t *testing.T) {
	const root = "zroot/sylve/jails/421"
	const snapshot = "sjs_partial_1"
	runner := &jailSnapshotRunner{datasets: map[string]jailSnapshotRunnerDataset{
		root + "/data@" + snapshot: {datasetType: gzfs.DatasetTypeSnapshot},
	}}

	targets, missing, err := newJailSnapshotTestService(runner).
		collectJailSnapshotTargets(context.Background(), root, snapshot, false)
	if err != nil {
		t.Fatalf("collect targets: %v", err)
	}
	if !missing {
		t.Fatal("missing root snapshot was not reported")
	}
	if want := []string{root + "/data@" + snapshot}; !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
}

func TestCleanupCreatedJailSnapshotUsesDetachedRecursiveDestroy(t *testing.T) {
	const root = "zroot/sylve/jails/422"
	const snapshot = "sjs_cleanup_1"
	runner := &jailSnapshotRunner{datasets: map[string]jailSnapshotRunnerDataset{
		root + "@" + snapshot: {datasetType: gzfs.DatasetTypeSnapshot},
	}}
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	newJailSnapshotTestService(runner).cleanupCreatedJailSnapshot(parent, root, snapshot)
	if runner.sawCanceled {
		t.Fatal("snapshot cleanup reused the canceled request context")
	}
	wantDestroy := []string{"zfs", "destroy", "-r", root + "@" + snapshot}
	found := false
	for _, command := range runner.commands {
		if reflect.DeepEqual(command, wantDestroy) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recursive destroy command not found in %#v", runner.commands)
	}
}

func TestReparentAndDeleteJailSnapshotRecordRepairsLineage(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.JailSnapshot{})
	root := jailModels.JailSnapshot{JailID: 1, CTID: 423, Name: "root", SnapshotName: "root", RootDataset: "zroot/jail"}
	if err := db.Create(&root).Error; err != nil {
		t.Fatalf("create root snapshot: %v", err)
	}
	middle := jailModels.JailSnapshot{JailID: 1, CTID: 423, ParentSnapshotID: &root.ID, Name: "middle", SnapshotName: "middle", RootDataset: "zroot/jail"}
	if err := db.Create(&middle).Error; err != nil {
		t.Fatalf("create middle snapshot: %v", err)
	}
	child := jailModels.JailSnapshot{JailID: 1, CTID: 423, ParentSnapshotID: &middle.ID, Name: "child", SnapshotName: "child", RootDataset: "zroot/jail"}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child snapshot: %v", err)
	}

	if err := reparentAndDeleteJailSnapshotRecord(db, middle); err != nil {
		t.Fatalf("delete middle snapshot: %v", err)
	}
	var refreshedChild jailModels.JailSnapshot
	if err := db.First(&refreshedChild, child.ID).Error; err != nil {
		t.Fatalf("reload child snapshot: %v", err)
	}
	if refreshedChild.ParentSnapshotID == nil || *refreshedChild.ParentSnapshotID != root.ID {
		t.Fatalf("child parent = %v, want root ID %d", refreshedChild.ParentSnapshotID, root.ID)
	}
	if err := db.First(&jailModels.JailSnapshot{}, middle.ID).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("middle snapshot lookup error = %v, want record not found", err)
	}
}

func TestRestoreJailDatabaseFromSnapshotRestoresConfigAndKeepsNormalizedIdentity(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.JailHooks{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&jailModels.JailSnapshot{},
	)
	current := jailModels.Jail{
		CTID:       424,
		Name:       "live-name",
		Hostname:   "current-host",
		ResolvConf: "nameserver 192.0.2.1\n",
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	if err := db.Create(&jailModels.Storage{
		JailID: current.ID,
		Pool:   "zroot",
		GUID:   "root-guid",
		Name:   "root",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("create current storage: %v", err)
	}
	if err := db.Create(&jailModels.JailHooks{
		JailID: current.ID,
		Phase:  jailModels.JailHookPhasePreStart,
		Script: "old hook",
	}).Error; err != nil {
		t.Fatalf("create current hook: %v", err)
	}

	restored := jailModels.Jail{
		ID:          current.ID,
		CTID:        current.CTID,
		Name:        current.Name,
		Hostname:    "snapshot-host",
		Description: "restored description",
		ResolvConf:  "nameserver 198.51.100.53\n",
		Storages: []jailModels.Storage{{
			Pool:   "zroot",
			GUID:   "root-guid",
			Name:   "root",
			IsBase: true,
		}},
		JailHooks: []jailModels.JailHooks{{
			Phase:   jailModels.JailHookPhasePostStart,
			Enabled: true,
			Script:  "restored hook",
		}},
	}
	if err := (&Service{DB: db}).restoreJailDatabaseFromSnapshot(current.CTID, restored); err != nil {
		t.Fatalf("restore jail database: %v", err)
	}

	var got jailModels.Jail
	if err := db.First(&got, current.ID).Error; err != nil {
		t.Fatalf("reload jail: %v", err)
	}
	if got.CTID != current.CTID || got.Name != current.Name {
		t.Fatalf("identity changed to name=%q ctid=%d", got.Name, got.CTID)
	}
	if got.Hostname != restored.Hostname || got.Description != restored.Description || got.ResolvConf != restored.ResolvConf {
		t.Fatalf("snapshot config was not restored: %+v", got)
	}

	var hooks []jailModels.JailHooks
	if err := db.Where("jid = ?", current.ID).Find(&hooks).Error; err != nil {
		t.Fatalf("reload hooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].Script != "restored hook" {
		t.Fatalf("hooks = %+v, want restored hook", hooks)
	}
}

func TestUsableJailSnapshotMountpointRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{raw: "/zroot/sylve/jails/425", want: "/zroot/sylve/jails/425", ok: true},
		{raw: "/zroot/sylve/../sylve/jails/425", want: "/zroot/sylve/jails/425", ok: true},
		{raw: "legacy"},
		{raw: "none"},
		{raw: "relative/path"},
		{raw: "/"},
	}
	for _, test := range tests {
		got, ok := usableJailSnapshotMountpoint(test.raw)
		if got != test.want || ok != test.ok {
			t.Errorf("usableJailSnapshotMountpoint(%q) = (%q, %t), want (%q, %t)", test.raw, got, ok, test.want, test.ok)
		}
	}
}

func TestNormalizeRestoredJailSnapshotStoragesRejectsDuplicateBaseDataset(t *testing.T) {
	current := &jailModels.Jail{
		ID:   12,
		CTID: 426,
		Storages: []jailModels.Storage{{
			JailID: 12,
			GUID:   "root-guid",
			IsBase: true,
		}},
	}
	restored := []jailModels.Storage{
		{GUID: "saved-root-guid", IsBase: true},
		{GUID: "root-guid", IsBase: false},
	}

	_, err := (&Service{}).normalizeRestoredJailSnapshotStorages(
		context.Background(),
		current,
		restored,
	)
	if err == nil || !strings.Contains(err.Error(), "restored_jail_storage_duplicate") {
		t.Fatalf("duplicate root storage error = %v", err)
	}
}
