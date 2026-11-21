// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

type namespaceDataset struct {
	mountpoint string
	typeName   gzfs.DatasetType
}

type namespaceRunner struct {
	poolName   string
	altroot    string
	datasets   map[string]namespaceDataset
	creates    [][]string
	unexpected [][]string
}

func newNamespaceRunner() *namespaceRunner {
	return &namespaceRunner{
		poolName: "tank",
		altroot:  "-",
		datasets: map[string]namespaceDataset{
			"tank": {mountpoint: "/mnt/tank", typeName: gzfs.DatasetTypeFilesystem},
		},
	}
}

func (r *namespaceRunner) explicitMountpoints() map[string]string {
	explicit := map[string]string{}
	for _, command := range r.creates {
		if mountpoint, ok := createMountpointOption(command); ok {
			explicit[command[len(command)-1]] = mountpoint
		}
	}
	return explicit
}

func createMountpointOption(args []string) (string, bool) {
	for i := 1; i+1 < len(args); i++ {
		if args[i] != "-o" {
			continue
		}
		if mountpoint, ok := strings.CutPrefix(args[i+1], "mountpoint="); ok {
			return mountpoint, true
		}
	}
	return "", false
}

func (r *namespaceRunner) addFilesystem(name, mountpoint string) {
	r.datasets[name] = namespaceDataset{
		mountpoint: mountpoint,
		typeName:   gzfs.DatasetTypeFilesystem,
	}
}

func (r *namespaceRunner) writeJSON(stdout io.Writer, value any) error {
	if stdout == nil {
		return nil
	}
	return json.NewEncoder(stdout).Encode(value)
}

func (r *namespaceRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout io.Writer,
	_ io.Writer,
	name string,
	args ...string,
) error {
	if name == "zpool" && len(args) > 0 && args[0] == "list" {
		return r.writeJSON(stdout, map[string]any{
			"output_version": map[string]any{"command": "zpool list"},
			"pools": map[string]any{
				r.poolName: map[string]any{
					"name": r.poolName,
					"properties": map[string]any{
						"altroot": map[string]any{"value": r.altroot},
					},
				},
			},
		})
	}
	if name != "zfs" || len(args) == 0 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}

	switch args[0] {
	case "list":
		datasetName := args[len(args)-2]
		datasets := map[string]any{}
		if dataset, ok := r.datasets[datasetName]; ok {
			datasets[datasetName] = map[string]any{
				"name": datasetName,
				"type": string(dataset.typeName),
				"pool": r.poolName,
				"properties": map[string]any{
					"guid":       map[string]any{"value": datasetName + "-guid"},
					"mountpoint": map[string]any{"value": dataset.mountpoint},
				},
			}
		}
		return r.writeJSON(stdout, map[string]any{
			"output_version": map[string]any{"command": "zfs list"},
			"datasets":       datasets,
		})
	case "create":
		r.creates = append(r.creates, slices.Clone(args))
		datasetName := args[len(args)-1]
		mountpoint, explicit := createMountpointOption(args)
		if !explicit {
			mountpoint = r.datasets[path.Dir(datasetName)].mountpoint
			if mountpoint != "none" && mountpoint != "legacy" {
				mountpoint = path.Join(mountpoint, path.Base(datasetName))
			}
		}
		r.datasets[datasetName] = namespaceDataset{
			mountpoint: mountpoint,
			typeName:   gzfs.DatasetTypeFilesystem,
		}
		return nil
	default:
		r.unexpected = append(r.unexpected, slices.Clone(args))
		return fmt.Errorf("unexpected zfs command: %v", args)
	}
}

func (r *namespaceRunner) client() *gzfs.Client {
	return gzfs.NewClient(gzfs.Options{Runner: r})
}

func TestEnsureSylveNamespaceCreatesInheritedDatasets(t *testing.T) {
	runner := newNamespaceRunner()
	created, err := EnsureSylveNamespace(t.Context(), runner.client(), runner.poolName)
	if err != nil {
		t.Fatalf("EnsureSylveNamespace returned an error: %v", err)
	}
	if len(created) != len(requiredSylveDatasetSuffixes) {
		t.Fatalf("created %d datasets, want %d", len(created), len(requiredSylveDatasetSuffixes))
	}

	var names []string
	for _, command := range runner.creates {
		for _, arg := range command {
			if strings.Contains(strings.ToLower(arg), "mountpoint=") {
				t.Fatalf("create command assigned an explicit mountpoint: %v", command)
			}
		}
		names = append(names, command[len(command)-1])
	}
	wantNames := []string{
		"tank/sylve",
		"tank/sylve/virtual-machines",
		"tank/sylve/jails",
		"tank/sylve/bootstraps",
		"tank/sylve/downloads",
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("created datasets = %v, want %v", names, wantNames)
	}

}

func TestEnsureSylveNamespacePreservesUsableCustomMountpoints(t *testing.T) {
	runner := newNamespaceRunner()
	for i, suffix := range requiredSylveDatasetSuffixes {
		mountpoint := filepath.Join("/mnt/custom", fmt.Sprintf("dataset-%d", i))
		runner.addFilesystem(runner.poolName+"/"+suffix, mountpoint)
	}

	created, err := EnsureSylveNamespace(t.Context(), runner.client(), runner.poolName)
	if err != nil {
		t.Fatalf("EnsureSylveNamespace returned an error: %v", err)
	}
	if len(created) != 0 || len(runner.creates) != 0 || len(runner.unexpected) != 0 {
		t.Fatalf(
			"existing datasets were changed: created=%d createCommands=%v unexpected=%v",
			len(created),
			runner.creates,
			runner.unexpected,
		)
	}
}

func TestEnsureSylveNamespaceRejectsUnusableExistingMountpoint(t *testing.T) {
	runner := newNamespaceRunner()
	runner.addFilesystem("tank/sylve", "none")

	created, err := EnsureSylveNamespace(t.Context(), runner.client(), runner.poolName)
	if err == nil {
		t.Fatal("expected unusable mountpoint to be rejected")
	}
	if len(created) != 0 || len(runner.creates) != 0 {
		t.Fatalf("unexpected mutation: created=%v creates=%v", created, runner.creates)
	}
}

func TestEnsureSylveNamespaceMountsUnderPoolNameWhenRootHasNoUsableMountpoint(t *testing.T) {
	for _, rootMountpoint := range []string{"none", "legacy", "/"} {
		t.Run(rootMountpoint, func(t *testing.T) {
			runner := newNamespaceRunner()
			runner.addFilesystem("tank", rootMountpoint)

			created, err := EnsureSylveNamespace(t.Context(), runner.client(), runner.poolName)
			if err != nil {
				t.Fatalf("EnsureSylveNamespace returned an error: %v", err)
			}
			if len(created) != len(requiredSylveDatasetSuffixes) {
				t.Fatalf("created %d datasets, want %d", len(created), len(requiredSylveDatasetSuffixes))
			}

			want := map[string]string{"tank/sylve": "/tank/sylve"}
			if explicit := runner.explicitMountpoints(); !maps.Equal(explicit, want) {
				t.Fatalf("explicit mountpoints = %v, want %v", explicit, want)
			}
			for _, dataset := range created {
				if dataset.Mountpoint != "/"+dataset.Name {
					t.Fatalf("%s mountpoint = %q, want %q", dataset.Name, dataset.Mountpoint, "/"+dataset.Name)
				}
			}
		})
	}
}

func TestEnsureContainerNamespaceInheritsExistingSylveUnderUnmountedRoot(t *testing.T) {
	runner := newNamespaceRunner()
	runner.addFilesystem("tank", "none")
	runner.addFilesystem("tank/sylve", "/tank/sylve")

	created, err := EnsureContainerNamespace(t.Context(), runner.client(), runner.poolName)
	if err != nil {
		t.Fatalf("EnsureContainerNamespace returned an error: %v", err)
	}
	if explicit := runner.explicitMountpoints(); len(explicit) != 0 {
		t.Fatalf("nested datasets were given explicit mountpoints: %v", explicit)
	}
	var mountpoints []string
	for _, dataset := range created {
		mountpoints = append(mountpoints, dataset.Mountpoint)
	}
	if want := []string{"/tank/sylve/containers", "/tank/sylve/containers/images"}; !slices.Equal(mountpoints, want) {
		t.Fatalf("mountpoints = %v, want %v", mountpoints, want)
	}
}

func TestEnsureSylveNamespaceRejectsUnmountedRootWithoutUsablePoolPath(t *testing.T) {
	runner := newNamespaceRunner()
	runner.poolName = "tank:backup"
	runner.datasets = map[string]namespaceDataset{
		"tank:backup": {mountpoint: "none", typeName: gzfs.DatasetTypeFilesystem},
	}

	created, err := EnsureSylveNamespace(t.Context(), runner.client(), runner.poolName)
	if err == nil || !strings.Contains(err.Error(), "pool_root_mountpoint_not_usable") {
		t.Fatalf("error = %v, want pool_root_mountpoint_not_usable", err)
	}
	if len(created) != 0 || len(runner.creates) != 0 {
		t.Fatalf("unexpected mutation: created=%v creates=%v", created, runner.creates)
	}
}

func TestEnsureSylveNamespaceRejectsAltrootBeforeMutation(t *testing.T) {
	runner := newNamespaceRunner()
	runner.altroot = "/tmp/import"

	created, err := EnsureSylveNamespace(t.Context(), runner.client(), runner.poolName)
	if err == nil {
		t.Fatal("expected altroot pool to be rejected")
	}
	if len(created) != 0 || len(runner.creates) != 0 {
		t.Fatalf("altroot rejection mutated datasets: created=%v commands=%v", created, runner.creates)
	}
}

func TestEnsureContainerNamespaceProvisionsImageStore(t *testing.T) {
	runner := newNamespaceRunner()
	created, err := EnsureContainerNamespace(t.Context(), runner.client(), "tank")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, dataset := range created {
		names = append(names, dataset.Name)
	}
	if !slices.Equal(names, []string{"tank/sylve", "tank/sylve/containers", "tank/sylve/containers/images"}) {
		t.Fatalf("unexpected container namespace: %v", names)
	}
	if created, err := EnsureContainerNamespace(t.Context(), runner.client(), "tank"); err != nil || len(created) != 0 {
		t.Fatalf("namespace retry was not idempotent: %v", err)
	}
}

func TestIntegrationEnsureSylveNamespaceInheritsDedicatedPoolMountpoint(t *testing.T) {
	poolName, client := zfstest.DedicatedPool(t)
	root, err := client.ZFS.Get(t.Context(), poolName, false)
	if err != nil || root == nil {
		t.Fatalf("get dedicated pool root: dataset=%v error=%v", root, err)
	}
	rootMountpoint, err := FilesystemMountpoint(root)
	if err != nil {
		t.Fatalf("resolve dedicated pool root: %v", err)
	}
	defaultMountpoint := filepath.Join(string(filepath.Separator), poolName)
	if rootMountpoint == defaultMountpoint {
		t.Fatalf("integration pool unexpectedly uses synthesized default mountpoint %q", rootMountpoint)
	}
	if _, err := os.Lstat(defaultMountpoint); err == nil {
		t.Fatalf("unexpected compatibility alias at %q", defaultMountpoint)
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect default mountpoint %q: %v", defaultMountpoint, err)
	}

	created, err := EnsureSylveNamespace(t.Context(), client, poolName)
	if err != nil {
		t.Fatalf("EnsureSylveNamespace returned an error: %v", err)
	}
	t.Cleanup(func() {
		for i := len(created) - 1; i >= 0; i-- {
			if err := created[i].Destroy(context.Background(), false, false); err != nil {
				t.Errorf("clean up %s: %v", created[i].Name, err)
			}
		}
	})
	if len(created) != len(requiredSylveDatasetSuffixes) {
		t.Fatalf("created %d datasets, want %d", len(created), len(requiredSylveDatasetSuffixes))
	}
	for _, dataset := range created {
		mountpoint, err := FilesystemMountpoint(dataset)
		if err != nil {
			t.Fatalf("resolve %s: %v", dataset.Name, err)
		}
		suffix := strings.TrimPrefix(dataset.Name, poolName+"/")
		expected := filepath.Join(rootMountpoint, filepath.FromSlash(suffix))
		if mountpoint != expected {
			t.Fatalf("%s mountpoint = %q, want inherited path %q", dataset.Name, mountpoint, expected)
		}
	}
}

func TestIntegrationEnsureSylveNamespaceUnderUnmountedPoolRoot(t *testing.T) {
	poolName, client := zfstest.DedicatedPool(t)
	root, err := client.ZFS.Get(t.Context(), poolName, false)
	if err != nil || root == nil {
		t.Fatalf("get dedicated pool root: dataset=%v error=%v", root, err)
	}
	if err := root.SetProperties(t.Context(), "mountpoint", "none"); err != nil {
		t.Fatalf("unmount dedicated pool root: %v", err)
	}

	base := filepath.Join(string(filepath.Separator), poolName)
	created, err := EnsureSylveNamespace(t.Context(), client, poolName)
	t.Cleanup(func() {
		for i := len(created) - 1; i >= 0; i-- {
			if err := created[i].Destroy(context.Background(), false, false); err != nil {
				t.Errorf("clean up %s: %v", created[i].Name, err)
			}
		}
		for _, dir := range []string{filepath.Join(base, "sylve"), base} {
			if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove mountpoint directory %s: %v", dir, err)
			}
		}
	})
	if err != nil {
		t.Fatalf("EnsureSylveNamespace returned an error: %v", err)
	}
	if len(created) != len(requiredSylveDatasetSuffixes) {
		t.Fatalf("created %d datasets, want %d", len(created), len(requiredSylveDatasetSuffixes))
	}
	for _, dataset := range created {
		mountpoint, err := FilesystemMountpoint(dataset)
		if err != nil {
			t.Fatalf("resolve %s: %v", dataset.Name, err)
		}
		if mountpoint != "/"+dataset.Name {
			t.Fatalf("%s mountpoint = %q, want %q", dataset.Name, mountpoint, "/"+dataset.Name)
		}
		if mounted := dataset.Properties["mounted"].Value; mounted != "yes" {
			t.Fatalf("%s mounted = %q, want yes", dataset.Name, mounted)
		}
	}
}
