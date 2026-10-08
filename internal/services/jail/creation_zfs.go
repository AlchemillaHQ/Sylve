// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var sourceDatasetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]*$`)

type sourceMember struct {
	Dataset      string
	GUID         string
	SnapshotGUID string
	MountRel     string
	Properties   map[string]string
}

type sourceTree struct {
	Members []sourceMember
}

func (s *Service) creationZFS() gzfs.Cmd {
	if s.creationCommand != nil {
		return *s.creationCommand
	}
	return gzfs.Cmd{Bin: "zfs"}
}

func validateZFSSourceIdentity(source jailServiceInterfaces.ZFSSource) error {
	if !sourceDatasetName.MatchString(source.Dataset) || !strings.Contains(source.Dataset, "/") || source.GUID == "" {
		return fmt.Errorf("invalid_zfs_source")
	}
	for _, component := range strings.Split(source.Dataset, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("invalid_zfs_source")
		}
	}
	return nil
}

func (s *Service) ListZFSSources(ctx context.Context) ([]*gzfs.Dataset, error) {
	if s.GZFS == nil {
		return nil, fmt.Errorf("zfs_client_not_initialized")
	}
	datasets, err := s.GZFS.ZFS.List(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_zfs_sources: %w", err)
	}
	mounts, err := utils.RunCommandWithContext(ctx, "mount", "-p")
	if err != nil {
		return nil, fmt.Errorf("failed_to_inspect_zfs_source_mounts: %w", err)
	}
	return s.filterAvailableZFSSources(ctx, filterZFSSources(datasets, mounts), datasets)
}

func canonicalJailSourceCTID(dataset string) (uint, bool) {
	parts := strings.Split(dataset, "/")
	if len(parts) != 4 || parts[1] != "sylve" || parts[2] != "jails" {
		return 0, false
	}
	ctID, err := strconv.ParseUint(parts[3], 10, 32)
	if err != nil || ctID == 0 || ctID > uint64(clusterModels.GuestIdentityMaxID) || strconv.FormatUint(ctID, 10) != parts[3] {
		return 0, false
	}
	return uint(ctID), true
}

func filterZFSSources(datasets []*gzfs.Dataset, mounts string) []*gzfs.Dataset {
	mounted := map[string]string{}
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == "zfs" {
			mounted[fields[0]] = fields[1]
		}
	}
	sources := make([]*gzfs.Dataset, 0)
	for _, dataset := range datasets {
		_, relative, _ := strings.Cut(dataset.Name, "/")
		_, jailRoot := canonicalJailSourceCTID(dataset.Name)
		if dataset.Type != gzfs.DatasetTypeFilesystem || (relative == "sylve" || strings.HasPrefix(relative, "sylve/")) && !jailRoot ||
			dataset.Properties["encryption"].Value != "off" || dataset.Properties["readonly"].Value == "on" {
			continue
		}
		if err := validateZFSSourceIdentity(jailServiceInterfaces.ZFSSource{Dataset: dataset.Name, GUID: dataset.GUID}); err != nil {
			continue
		}
		root, err := validateFilesystemDatasetMountpoint(dataset, dataset.Name, dataset.GUID)
		if err != nil || mounted[dataset.Name] != root || validateCopiedRoot(root, "") != nil {
			continue
		}
		members := make([]*gzfs.Dataset, 0)
		for _, member := range datasets {
			if member.Type != gzfs.DatasetTypeSnapshot && (member.Name == dataset.Name || strings.HasPrefix(member.Name, dataset.Name+"/")) {
				members = append(members, member)
			}
		}
		usable := true
		for _, member := range members {
			if member.Properties["encryption"].Value != "off" || member.Properties["readonly"].Value == "on" || mounted[member.Name] != member.Mountpoint {
				usable = false
				break
			}
		}
		if !usable {
			continue
		}
		tree, err := describeSourceTree(dataset, members)
		if err != nil || validateSourceRootMounts(root, tree, mounts) != nil {
			continue
		}
		sources = append(sources, dataset)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources
}

func (s *Service) filterAvailableZFSSources(ctx context.Context, candidates, datasets []*gzfs.Dataset) ([]*gzfs.Dataset, error) {
	sources := make([]*gzfs.Dataset, 0, len(candidates))
	for _, candidate := range candidates {
		if _, jailRoot := canonicalJailSourceCTID(candidate.Name); !jailRoot {
			sources = append(sources, candidate)
			continue
		}
		source := jailServiceInterfaces.ZFSSource{Dataset: candidate.Name, GUID: candidate.GUID}
		blocked, err := s.sourceOperationBlocker(ctx, source)
		if err != nil {
			return nil, err
		}
		if blocked != "" {
			continue
		}
		var members []*gzfs.Dataset
		for _, dataset := range datasets {
			if dataset.Type != gzfs.DatasetTypeSnapshot && (dataset.Name == candidate.Name || strings.HasPrefix(dataset.Name, candidate.Name+"/")) {
				members = append(members, dataset)
			}
		}
		registered, err := s.sourceTreeRegistered(ctx, members)
		if err != nil {
			return nil, err
		}
		if registered {
			continue
		}
		for _, member := range members {
			policy, err := s.GZFS.ZFS.GetProperty(ctx, member.Name, "sylve:replication-policy-id")
			if err != nil {
				return nil, err
			}
			if policy.Value != "" && policy.Value != "-" {
				blocked = "zfs_source_protected"
				break
			}
		}
		if blocked != "" {
			continue
		}
		if err := s.requireSourceStopped(ctx, candidate.Mountpoint); err != nil {
			if strings.HasPrefix(err.Error(), "zfs_source_jail_running") {
				continue
			}
			return nil, err
		}
		sources = append(sources, candidate)
	}
	return sources, nil
}

func (s *Service) sourceTreeRegistered(ctx context.Context, datasets []*gzfs.Dataset) (bool, error) {
	guids := make([]string, 0, len(datasets))
	for _, dataset := range datasets {
		if dataset.Type != gzfs.DatasetTypeSnapshot {
			guids = append(guids, dataset.GUID)
		}
	}
	var count int64
	err := s.DB.WithContext(ctx).Model(&jailModels.Storage{}).Where("guid IN ?", guids).Count(&count).Error
	return count != 0, err
}

func (s *Service) inspectZFSSource(ctx context.Context, source jailServiceInterfaces.ZFSSource, pool string) (sourceTree, error) {
	var tree sourceTree
	if err := validateZFSSourceIdentity(source); err != nil {
		return tree, err
	}
	_, relative, _ := strings.Cut(source.Dataset, "/")
	_, jailRoot := canonicalJailSourceCTID(source.Dataset)
	if (relative == "sylve" || strings.HasPrefix(relative, "sylve/")) && !jailRoot {
		return tree, fmt.Errorf("zfs_source_protected: internal_dataset")
	}
	if err := s.requireSourceOperationAllowed(ctx, source); err != nil {
		return tree, err
	}
	root, err := s.GZFS.ZFS.Get(ctx, source.Dataset, false)
	if err != nil || root == nil {
		return tree, fmt.Errorf("zfs_source_not_found: %v", err)
	}
	if root.GUID != source.GUID || root.Type != gzfs.DatasetTypeFilesystem {
		return tree, fmt.Errorf("zfs_source_identity_changed")
	}
	rootMount, err := validateFilesystemDatasetMountpoint(root, source.Dataset, source.GUID)
	if err != nil || rootMount == "/" {
		return tree, fmt.Errorf("zfs_source_layout_unsupported")
	}
	parentName := pool + "/sylve/jails"
	if source.Dataset == parentName || strings.HasPrefix(parentName, source.Dataset+"/") || strings.Contains(source.Dataset, "/sylve/jails/create-") {
		return tree, fmt.Errorf("zfs_source_destination_overlap")
	}
	parent, err := s.GZFS.ZFS.Get(ctx, parentName, false)
	if err != nil || parent == nil {
		return tree, fmt.Errorf("jail_dataset_mountpoint_not_usable: destination_parent_missing")
	}
	if _, err := validateFilesystemDatasetMountpoint(parent, parentName, ""); err != nil {
		return tree, err
	}
	if parent.Properties["encryption"].Value != "off" {
		return tree, fmt.Errorf("zfs_source_encryption_unsupported: destination")
	}
	datasets, err := s.GZFS.ZFS.List(ctx, true, source.Dataset)
	if err != nil {
		return tree, fmt.Errorf("failed_to_inspect_zfs_source: %w", err)
	}
	tree, err = describeSourceTree(root, datasets)
	if err != nil {
		return tree, err
	}
	if jailRoot {
		registered, err := s.sourceTreeRegistered(ctx, datasets)
		if err != nil {
			return tree, err
		}
		if registered {
			return tree, fmt.Errorf("zfs_source_protected: registered_jail_storage")
		}
	}
	var referenced uint64
	for _, ds := range datasets {
		if ds.Type == gzfs.DatasetTypeSnapshot {
			continue
		}
		if ds.Properties["encryption"].Value != "off" {
			return tree, fmt.Errorf("zfs_source_encryption_unsupported: %s", ds.Name)
		}
		policy, err := s.GZFS.ZFS.GetProperty(ctx, ds.Name, "sylve:replication-policy-id")
		if err != nil {
			return tree, err
		}
		if policy.Value != "" && policy.Value != "-" || ds.Properties["readonly"].Value == "on" {
			return tree, fmt.Errorf("zfs_source_protected: read_only_or_replication_protected")
		}
		if err := s.checkCreationMount(ctx, ds.Name, ds.Mountpoint); err != nil {
			return tree, err
		}
		referenced += ds.Referenced
	}
	if err := s.requireSourceStopped(ctx, rootMount); err != nil {
		return tree, err
	}
	if err := validateCopiedRoot(rootMount, ""); err != nil {
		return tree, err
	}
	if err := requireSourceRootMounts(ctx, rootMount, tree); err != nil {
		return tree, err
	}
	if referenced > parent.Available {
		return tree, fmt.Errorf("zfs_source_insufficient_space")
	}
	return tree, nil
}

func (s *Service) requireSourceOperationAllowed(ctx context.Context, source jailServiceInterfaces.ZFSSource) error {
	blocked, err := s.sourceOperationBlocker(ctx, source)
	if err != nil {
		return err
	}
	if blocked != "" {
		return errors.New(blocked)
	}
	return nil
}

func (s *Service) sourceOperationBlocker(ctx context.Context, source jailServiceInterfaces.ZFSSource) (string, error) {
	ctID, jailRoot := canonicalJailSourceCTID(source.Dataset)
	if s.DB.Migrator().HasTable(&jailModels.JailCreation{}) {
		var operations []jailModels.JailCreation
		if err := s.DB.WithContext(ctx).Where("active_ct_id IS NOT NULL").Find(&operations).Error; err != nil {
			return "", err
		}
		for _, operation := range operations {
			var request jailServiceInterfaces.CreateJailRequest
			if err := json.Unmarshal([]byte(operation.Request), &request); err != nil {
				return "", fmt.Errorf("jail_creation_guard_unavailable: %w", err)
			}
			root := fmt.Sprintf("%s/sylve/jails/%d", request.Pool, operation.CTID)
			if jailRoot && operation.CTID == ctID || source.Dataset == root || strings.HasPrefix(source.Dataset, root+"/") || strings.HasPrefix(root, source.Dataset+"/") {
				return "jail_creation_in_progress: source_is_not_ready", nil
			}
		}
	}
	if !jailRoot {
		return "", nil
	}
	var registered int64
	if err := s.DB.WithContext(ctx).Model(&jailModels.Jail{}).Where("ct_id = ?", ctID).Count(&registered).Error; err != nil {
		return "", err
	}
	if registered != 0 {
		return "zfs_source_protected: registered_jail", nil
	}
	if s.DB.Migrator().HasTable(&taskModels.GuestLifecycleTask{}) {
		var active int64
		if err := s.DB.WithContext(ctx).Model(&taskModels.GuestLifecycleTask{}).
			Where("guest_type = ? AND guest_id = ? AND status IN ?", "jail", ctID,
				[]string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning}).Count(&active).Error; err != nil {
			return "", err
		}
		if active != 0 {
			return "zfs_source_protected: lifecycle_operation", nil
		}
	}
	for _, model := range []any{&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{}} {
		if !s.DB.Migrator().HasTable(model) {
			continue
		}
		query := s.DB.WithContext(ctx).Model(model).Where("guest_type = ? AND guest_id = ?", "jail", ctID)
		if _, policy := model.(*clusterModels.ReplicationPolicy); policy {
			query = query.Where("enabled = ? OR transition_state IN ?", true, []string{
				clusterModels.ReplicationTransitionStateDemoting, clusterModels.ReplicationTransitionStateCatchup,
				clusterModels.ReplicationTransitionStatePromoting, clusterModels.ReplicationTransitionStateRollingBack,
			})
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return "", err
		}
		if count != 0 {
			return "zfs_source_protected: guest_operation_or_replication_policy", nil
		}
	}
	if s.DB.Migrator().HasTable(&clusterModels.ReplicationRunOperation{}) {
		var active int64
		if err := s.DB.WithContext(ctx).Model(&clusterModels.ReplicationRunOperation{}).
			Joins("JOIN replication_policies ON replication_policies.id = replication_run_operations.policy_id").
			Where("replication_policies.guest_type = ? AND replication_policies.guest_id = ?", "jail", ctID).Count(&active).Error; err != nil {
			return "", err
		}
		if active != 0 {
			return "zfs_source_protected: replication_run", nil
		}
	}
	return "", nil
}

func requireSourceRootMounts(ctx context.Context, root string, tree sourceTree) error {
	output, err := utils.RunCommandWithContext(ctx, "mount", "-p")
	if err != nil {
		return err
	}
	return validateSourceRootMounts(root, tree, output)
}

func validateSourceRootMounts(root string, tree sourceTree, mounts string) error {
	known := map[string]bool{}
	for _, member := range tree.Members {
		known[member.Dataset] = true
	}
	// Inspect nested system directories too: a thin jail may keep /bin in its
	// root dataset but still depend on an external release for libraries/tools.
	paths := []string{"bin/sh", "etc", "usr", "var", "sbin", "lib", "libexec", "usr/bin", "usr/sbin", "usr/lib", "usr/libexec", "usr/share"}
	for index, required := range paths {
		path, err := filepath.EvalSymlinks(filepath.Join(root, required))
		if index >= 4 && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("zfs_source_layout_unsupported: %s", required)
		}
		closest, owned := "", false
		for _, line := range strings.Split(mounts, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			mountpoint := fields[1]
			if (path == mountpoint || strings.HasPrefix(path, strings.TrimSuffix(mountpoint, "/")+"/")) && len(mountpoint) > len(closest) {
				closest, owned = mountpoint, fields[2] == "zfs" && known[fields[0]]
			}
		}
		if !owned {
			return fmt.Errorf("zfs_source_layout_unsupported: external_mount_supplies_%s", required)
		}
	}
	return nil
}

func describeSourceTree(root *gzfs.Dataset, datasets []*gzfs.Dataset) (sourceTree, error) {
	var tree sourceTree
	rootMount, err := validateFilesystemDatasetMountpoint(root, root.Name, root.GUID)
	if err != nil {
		return tree, err
	}
	seenMounts := map[string]bool{}
	sort.Slice(datasets, func(i, j int) bool { return datasets[i].Name < datasets[j].Name })
	for _, ds := range datasets {
		if ds.Type == gzfs.DatasetTypeSnapshot {
			continue
		}
		if ds.Name != root.Name && !strings.HasPrefix(ds.Name, root.Name+"/") {
			return tree, fmt.Errorf("zfs_source_layout_unsupported")
		}
		mountpoint, err := validateFilesystemDatasetMountpoint(ds, ds.Name, ds.GUID)
		if err != nil {
			return tree, fmt.Errorf("zfs_source_layout_unsupported: %s", ds.Name)
		}
		rel, err := filepath.Rel(rootMount, mountpoint)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || rel == "." && ds.Name != root.Name {
			return tree, fmt.Errorf("zfs_source_layout_unsupported: child_mountpoint_outside_root")
		}
		if rel == "." {
			rel = ""
		}
		if seenMounts[rel] {
			return tree, fmt.Errorf("zfs_source_layout_unsupported: ambiguous_child_mountpoint")
		}
		seenMounts[rel] = true
		props := map[string]string{}
		for _, property := range []string{"compression", "recordsize", "aclmode", "aclinherit", "atime"} {
			if value := ds.Properties[property].Value; value != "" && value != "-" {
				props[property] = value
			}
		}
		tree.Members = append(tree.Members, sourceMember{Dataset: ds.Name, GUID: ds.GUID, MountRel: rel, Properties: props})
	}
	if len(tree.Members) == 0 || tree.Members[0].Dataset != root.Name || tree.Members[0].GUID != root.GUID {
		return tree, fmt.Errorf("zfs_source_identity_changed")
	}
	return tree, nil
}

func (s *Service) resolveCreationSnapshots(ctx context.Context, snapshotName, snapshotGUID string, tree *sourceTree) error {
	var txg string
	for i := range tree.Members {
		member := &tree.Members[i]
		name := member.Dataset + "@" + snapshotName
		snapshot, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil || snapshot == nil || snapshot.Type != gzfs.DatasetTypeSnapshot {
			return fmt.Errorf("jail_creation_snapshot_missing: %s", name)
		}
		if i == 0 && snapshot.GUID != snapshotGUID {
			return fmt.Errorf("zfs_source_identity_changed")
		}
		creationTXG, err := s.GZFS.ZFS.GetProperty(ctx, snapshot.Name, "createtxg")
		if err != nil {
			return err
		}
		if creationTXG.Value == "" || creationTXG.Value == "-" || txg != "" && creationTXG.Value != txg {
			return fmt.Errorf("jail_creation_snapshot_capture_inconsistent")
		}
		txg = creationTXG.Value
		member.SnapshotGUID = snapshot.GUID
	}
	return nil
}

func (s *Service) requireSourceStopped(ctx context.Context, root string) error {
	var paths []string
	var err error
	if s.creationRunningPaths != nil {
		paths, err = s.creationRunningPaths(ctx)
	} else {
		output, commandErr := utils.RunCommandWithContext(ctx, "/usr/sbin/jls", "--libxo", "json", "path")
		err = commandErr
		if err == nil {
			var jails struct {
				Information struct {
					Jails []struct {
						Path string `json:"path"`
					} `json:"jail"`
				} `json:"jail-information"`
			}
			if err = json.Unmarshal([]byte(output), &jails); err == nil {
				for _, jail := range jails.Information.Jails {
					paths = append(paths, jail.Path)
				}
			}
		}
	}
	if err != nil {
		return fmt.Errorf("failed_to_check_zfs_source_runtime: %w", err)
	}
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == root || strings.HasPrefix(path, root+"/") || strings.HasPrefix(root, path+"/") {
			return fmt.Errorf("zfs_source_jail_running")
		}
	}
	return nil
}

func validateCopiedRoot(root, jailType string) error {
	confined, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("zfs_source_layout_unsupported: %w", err)
	}
	defer confined.Close()
	for _, required := range []string{"bin/sh", "etc", "usr", "var"} {
		info, err := confined.Stat(required)
		if err != nil || required == "bin/sh" && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0) || required != "bin/sh" && !info.IsDir() {
			return fmt.Errorf("zfs_source_layout_unsupported: missing_or_external_%s", required)
		}
	}
	if jailType == string(jailModels.JailTypeFreeBSD) {
		if _, err := confined.Stat("etc/rc"); err != nil {
			return fmt.Errorf("zfs_source_layout_unsupported: missing_freebsd_rc")
		}
	}
	// The existing configuration writers operate on host paths. Reject symlink
	// components they would traverse, including links pointing outside the jail.
	for _, target := range []string{"etc/resolv.conf", "etc/rc.conf", "usr/local/sylve/scripts/start.sh", "usr/local/sylve/scripts/stop.sh", ".sylve/jail.json", ".sylve/host-config"} {
		parts := strings.Split(target, "/")
		for i := range parts {
			info, err := confined.Lstat(filepath.Join(parts[:i+1]...))
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("zfs_source_unsafe_configuration_path: %s", target)
			}
		}
	}
	return nil
}

func (s *Service) copyZFSSource(ctx context.Context, req jailServiceInterfaces.CreateJailRequest, op *jailModels.JailCreation, state *creationState) (*gzfs.Dataset, string, error) {
	source := *req.ZFSSource
	tree, err := s.inspectZFSSource(ctx, source, req.Pool)
	if err != nil {
		return nil, "", err
	}
	snapshotName := "sylve_create_" + strings.ReplaceAll(op.ID, "-", "")
	for _, member := range tree.Members {
		state.Snapshots = append(state.Snapshots, creationDataset{Name: member.Dataset + "@" + snapshotName})
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "snapshotting"); err != nil {
		return nil, "", err
	}
	if err := func() error {
		s.actionMutex.Lock()
		defer s.actionMutex.Unlock()
		root, err := s.GZFS.ZFS.Get(ctx, source.Dataset, false)
		if err != nil || root == nil || root.GUID != source.GUID {
			return fmt.Errorf("zfs_source_identity_changed")
		}
		if err := s.requireSourceStopped(ctx, root.Mountpoint); err != nil {
			return err
		}
		_, _, err = s.creationZFS().RunBytes(ctx, nil, "snapshot", "-r", "-o", creationOwnerProperty+"="+op.ID, source.Dataset+"@"+snapshotName)
		return err
	}(); err != nil {
		return nil, "", err
	}
	rootSnapshot, err := s.GZFS.ZFS.Get(ctx, source.Dataset+"@"+snapshotName, false)
	if err != nil || rootSnapshot == nil {
		return nil, "", fmt.Errorf("jail_creation_snapshot_missing")
	}
	if err := s.resolveCreationSnapshots(ctx, snapshotName, rootSnapshot.GUID, &tree); err != nil {
		return nil, "", err
	}
	currentDatasets, err := s.GZFS.ZFS.List(ctx, true, source.Dataset)
	if err != nil {
		return nil, "", err
	}
	// A pool may include ordinary snapshots in its default ZFS inventory.
	filesystems := currentDatasets[:0]
	for _, dataset := range currentDatasets {
		if dataset.Type != gzfs.DatasetTypeSnapshot {
			filesystems = append(filesystems, dataset)
		}
	}
	currentDatasets = filesystems
	sort.Slice(currentDatasets, func(i, j int) bool { return currentDatasets[i].Name < currentDatasets[j].Name })
	if len(currentDatasets) != len(tree.Members) {
		return nil, "", fmt.Errorf("zfs_source_identity_changed")
	}
	for i, ds := range currentDatasets {
		expectedMount := filepath.Join(currentDatasets[0].Mountpoint, tree.Members[i].MountRel)
		if ds.Name != tree.Members[i].Dataset || ds.GUID != tree.Members[i].GUID || ds.Mountpoint != expectedMount {
			return nil, "", fmt.Errorf("zfs_source_identity_changed")
		}
	}
	for i := range state.Snapshots {
		state.Snapshots[i].GUID = tree.Members[i].SnapshotGUID
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "copying"); err != nil {
		return nil, "", err
	}
	for _, snapshot := range state.Snapshots {
		actual, err := s.GZFS.ZFS.Get(ctx, snapshot.Name, false)
		if err != nil || actual == nil || actual.GUID != snapshot.GUID {
			return nil, "", fmt.Errorf("zfs_source_identity_changed")
		}
		if _, _, err := s.creationZFS().RunBytes(ctx, nil, "hold", "sylve_create_"+op.ID, snapshot.Name); err != nil {
			return nil, "", err
		}
	}
	parent, err := s.GZFS.ZFS.Get(ctx, req.Pool+"/sylve/jails", false)
	if err != nil || parent == nil {
		return nil, "", fmt.Errorf("jail_dataset_mountpoint_not_usable")
	}
	container := parent.Name + "/create-" + op.ID
	stagingRoot := container + "/root"
	stagingMount := filepath.Join(parent.Mountpoint, ".create-"+op.ID, "root")
	stagingDir := filepath.Dir(stagingMount)
	if err := requireNewCreationMountpoint(stagingDir); err != nil {
		return nil, "", err
	}
	state.StagingDir = stagingDir
	state.Datasets = append(state.Datasets, creationDataset{Name: container})
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "copying"); err != nil {
		return nil, "", err
	}
	if err := os.Mkdir(stagingDir, 0700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(stagingDir, ".creation-operation"), []byte(op.ID), 0600); err != nil {
		return nil, "", err
	}
	containerDS, err := s.GZFS.ZFS.CreateFilesystem(ctx, container, map[string]string{creationOwnerProperty: op.ID, "canmount": "off", "mountpoint": "none"})
	if err != nil || containerDS == nil {
		return nil, "", fmt.Errorf("failed_to_create_jail_staging_dataset: %v", err)
	}
	state.Datasets[0].GUID = containerDS.GUID
	for _, member := range tree.Members {
		rel := strings.TrimPrefix(member.Dataset, source.Dataset)
		name := stagingRoot + rel
		state.Datasets = append(state.Datasets, creationDataset{Name: name, Snapshot: snapshotName, SnapshotGUID: member.SnapshotGUID})
		if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "copying"); err != nil {
			return nil, "", err
		}
		if existing, err := s.GZFS.ZFS.Get(ctx, name, false); err != nil && !isZFSDatasetMissingError(err) {
			return nil, "", err
		} else if existing != nil {
			return nil, "", fmt.Errorf("jail_base_fs_with_ctid_already_exists")
		}
		if err := s.receiveCreationSnapshot(ctx, member.Dataset+"@"+snapshotName, name, filepath.Join(stagingMount, member.MountRel), op.ID, member.Properties); err != nil {
			return nil, "", err
		}
		ds, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil || ds == nil {
			return nil, "", fmt.Errorf("failed_to_verify_jail_copy")
		}
		state.Datasets[len(state.Datasets)-1].GUID = ds.GUID
		if origin := ds.Properties["origin"].Value; origin != "" && origin != "-" {
			return nil, "", fmt.Errorf("jail_copy_has_clone_dependency")
		}
		copied, err := s.GZFS.ZFS.Get(ctx, name+"@"+snapshotName, false)
		if err != nil || copied == nil || copied.GUID != member.SnapshotGUID {
			return nil, "", fmt.Errorf("jail_copy_snapshot_identity_mismatch")
		}
		if err := copied.Destroy(ctx, false, false); err != nil {
			return nil, "", err
		}
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "verifying"); err != nil {
		return nil, "", err
	}
	if err := s.mountCreationTree(ctx, stagingRoot, stagingMount, source.Dataset, tree); err != nil {
		return nil, "", err
	}
	if err := validateCopiedRoot(stagingMount, string(req.Type)); err != nil {
		return nil, "", err
	}
	confined, err := os.OpenRoot(stagingMount)
	if err != nil {
		return nil, "", err
	}
	// Host configuration staged by an older Sylve jail is not configuration
	// for this new identity. All active hooks/metadata are generated below.
	err = confined.RemoveAll(".sylve/host-config")
	confined.Close()
	if err != nil {
		return nil, "", err
	}
	if err := s.unmountCreationTree(ctx, stagingRoot, source.Dataset, tree); err != nil {
		return nil, "", err
	}
	finalName := fmt.Sprintf("%s/sylve/jails/%d", req.Pool, op.CTID)
	finalMount := filepath.Join(parent.Mountpoint, fmt.Sprint(op.CTID))
	if err := requireNewCreationMountpoint(finalMount); err != nil {
		return nil, "", err
	}
	if existing, err := s.GZFS.ZFS.Get(ctx, finalName, false); err != nil && !isZFSDatasetMissingError(err) {
		return nil, "", err
	} else if existing != nil {
		return nil, "", fmt.Errorf("jail_base_fs_with_ctid_already_exists")
	}
	for i, member := range tree.Members {
		state.Datasets = append(state.Datasets, creationDataset{Name: finalName + strings.TrimPrefix(member.Dataset, source.Dataset), GUID: state.Datasets[i+1].GUID, Snapshot: snapshotName, SnapshotGUID: member.SnapshotGUID})
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "publishing"); err != nil {
		return nil, "", err
	}
	if _, _, err := s.creationZFS().RunBytes(ctx, nil, "rename", "-u", stagingRoot, finalName); err != nil {
		return nil, "", err
	}
	if err := s.mountCreationTree(ctx, finalName, finalMount, source.Dataset, tree); err != nil {
		return nil, "", err
	}
	if err := containerDS.Destroy(ctx, false, false); err != nil {
		return nil, "", err
	}
	if err := removeCreationStagingDir(op.ID, state.StagingDir); err != nil {
		return nil, "", err
	}
	for _, member := range tree.Members {
		ds, err := s.GZFS.ZFS.Get(ctx, finalName+strings.TrimPrefix(member.Dataset, source.Dataset), false)
		if err != nil || ds == nil {
			return nil, "", fmt.Errorf("failed_to_get_jail_copy_dataset")
		}
		if err := ds.SetProperties(ctx, "canmount", "on"); err != nil {
			return nil, "", err
		}
	}
	if err := s.cleanupCreationSnapshots(ctx, op, state); err != nil {
		return nil, "", err
	}
	ds, err := s.GZFS.ZFS.Get(ctx, finalName, false)
	return ds, finalMount, err
}

func requireNewCreationMountpoint(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("jail_create_stale_artifacts_detected: mountpoint=%s", path)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || resolved != filepath.Dir(path) {
		return fmt.Errorf("jail_dataset_mountpoint_not_usable: unsafe_parent_path")
	}
	return nil
}

func (s *Service) receiveCreationSnapshot(ctx context.Context, snapshot, destination, mountpoint, owner string, properties map[string]string) error {
	transferCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() {
		var stderr bytes.Buffer
		err := s.creationZFS().RunStream(transferCtx, nil, writer, &stderr, "send", snapshot)
		writer.CloseWithError(err)
		done <- err
	}()
	args := []string{"receive", "-u", "-o", creationOwnerProperty + "=" + owner, "-o", "mountpoint=" + mountpoint, "-o", "canmount=noauto", "-o", "readonly=off", "-o", "sharenfs=off", "-o", "sharesmb=off", "-o", "quota=none", "-o", "refquota=none", "-o", "reservation=none", "-o", "refreservation=none"}
	for _, key := range []string{"compression", "recordsize", "aclmode", "aclinherit", "atime"} {
		if value := properties[key]; value != "" {
			args = append(args, "-o", key+"="+value)
		}
	}
	args = append(args, destination)
	var stderr bytes.Buffer
	receiveErr := s.creationZFS().RunStream(transferCtx, reader, io.Discard, &stderr, args...)
	reader.Close()
	if receiveErr != nil {
		cancel()
	}
	sendErr := <-done
	if err := errors.Join(sendErr, receiveErr); err != nil {
		return fmt.Errorf("failed_to_copy_zfs_source: %w", err)
	}
	return nil
}

func orderedMountMembers(tree sourceTree) []sourceMember {
	members := append([]sourceMember(nil), tree.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].MountRel < members[j].MountRel })
	return members
}

func (s *Service) mountCreationTree(ctx context.Context, destination, rootMount, source string, tree sourceTree) error {
	for _, member := range orderedMountMembers(tree) {
		name := destination + strings.TrimPrefix(member.Dataset, source)
		mountpoint := filepath.Join(rootMount, member.MountRel)
		ds, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil || ds == nil {
			return fmt.Errorf("failed_to_get_jail_copy_dataset")
		}
		if err := ds.SetProperties(ctx, "mountpoint", mountpoint, "canmount", "noauto"); err != nil {
			return err
		}
		if member.MountRel != "" {
			confined, err := os.OpenRoot(rootMount)
			if err != nil {
				return err
			}
			path, pathErr := confined.Stat(member.MountRel)
			confined.Close()
			if pathErr != nil || !path.IsDir() {
				return fmt.Errorf("zfs_source_layout_unsupported: missing_child_mount_directory")
			}
			resolved, err := filepath.EvalSymlinks(mountpoint)
			if err != nil || resolved != mountpoint {
				return fmt.Errorf("zfs_source_layout_unsupported: symlink_child_mountpoint")
			}
		}
		if err := ds.Mount(ctx, false); err != nil {
			return err
		}
		if err := s.checkCreationMount(ctx, name, mountpoint); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) checkCreationMount(ctx context.Context, dataset, mountpoint string) error {
	if s.creationMountCheck != nil {
		return s.creationMountCheck(ctx, dataset, mountpoint)
	}
	return verifyCreationMount(ctx, dataset, mountpoint)
}

func verifyCreationMount(ctx context.Context, dataset, mountpoint string) error {
	output, err := utils.RunCommandWithContext(ctx, "mount", "-p")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == dataset && fields[1] == mountpoint && fields[2] == "zfs" {
			return nil
		}
	}
	return fmt.Errorf("jail_copy_mount_ownership_unverified: %s", dataset)
}

func (s *Service) unmountCreationTree(ctx context.Context, destination, source string, tree sourceTree) error {
	members := orderedMountMembers(tree)
	for i := len(members) - 1; i >= 0; i-- {
		ds, err := s.GZFS.ZFS.Get(ctx, destination+strings.TrimPrefix(members[i].Dataset, source), false)
		if err != nil || ds == nil {
			return fmt.Errorf("failed_to_get_jail_copy_dataset")
		}
		if err := ds.Unmount(ctx, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) cleanupCreationSnapshots(ctx context.Context, op *jailModels.JailCreation, state *creationState) error {
	var result error
	if len(state.Snapshots) > 0 {
		rootSnapshot := state.Snapshots[0].Name
		root, name, ok := strings.Cut(rootSnapshot, "@")
		if !ok {
			return fmt.Errorf("jail_creation_snapshot_journal_invalid")
		}
		// A descendant can appear between enumeration and recursive snapshot.
		// Discover only snapshots bearing this operation's local ownership tag.
		snapshots, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, true, root)
		if err != nil && !isZFSDatasetMissingError(err) {
			result = errors.Join(result, err)
		}
		known := map[string]bool{}
		for _, snapshot := range state.Snapshots {
			known[snapshot.Name] = true
		}
		for _, snapshot := range snapshots {
			if !strings.HasSuffix(snapshot.Name, "@"+name) || known[snapshot.Name] {
				continue
			}
			owner, err := s.GZFS.ZFS.GetProperty(ctx, snapshot.Name, creationOwnerProperty)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			if owner.Value == op.ID && strings.EqualFold(owner.Source.Type, "local") {
				state.Snapshots = append(state.Snapshots, creationDataset{Name: snapshot.Name, GUID: snapshot.GUID})
			}
		}
	}
	for _, resource := range state.Snapshots {
		snapshot, err := s.GZFS.ZFS.Get(ctx, resource.Name, false)
		if isZFSDatasetMissingError(err) || err == nil && snapshot == nil {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if resource.GUID != "" && snapshot.GUID != resource.GUID {
			result = errors.Join(result, fmt.Errorf("jail_creation_snapshot_identity_changed: %s", resource.Name))
			continue
		}
		owner, err := s.GZFS.ZFS.GetProperty(ctx, snapshot.Name, creationOwnerProperty)
		if err != nil || owner.Value != op.ID || !strings.EqualFold(owner.Source.Type, "local") {
			result = errors.Join(result, fmt.Errorf("jail_creation_snapshot_ownership_unverified: %s", resource.Name))
			continue
		}
		holds, _, err := s.creationZFS().RunBytes(ctx, nil, "holds", "-H", resource.Name)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		for _, line := range strings.Split(string(holds), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == "sylve_create_"+op.ID {
				_, _, err = s.creationZFS().RunBytes(ctx, nil, "release", "sylve_create_"+op.ID, resource.Name)
				result = errors.Join(result, err)
			}
		}
		result = errors.Join(result, snapshot.Destroy(ctx, false, false))
	}
	return result
}
