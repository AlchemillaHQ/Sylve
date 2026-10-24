// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

type creationRename struct {
	Source                string                                 `json:"source"`
	Destination           string                                 `json:"destination"`
	Mountpoint            string                                 `json:"mountpoint"`
	DestinationMountpoint string                                 `json:"destinationMountpoint"`
	Snapshot              string                                 `json:"snapshot"`
	Tree                  sourceTree                             `json:"tree"`
	Properties            map[string]map[string]gzfs.ZFSProperty `json:"properties"`
	RestoreData           bool                                   `json:"restoreData"`
	Restored              bool                                   `json:"restored"`
}

var renameSourceProperties = []string{"mountpoint", "canmount", "readonly", "sharenfs", "sharesmb", creationOwnerProperty}

func (s *Service) requireRenameSourceUnreferenced(ctx context.Context, tree sourceTree) error {
	guids, names := make([]string, 0, len(tree.Members)), make([]string, 0, len(tree.Members))
	for _, member := range tree.Members {
		guids = append(guids, member.GUID)
		names = append(names, member.Dataset)
	}
	for _, reference := range []struct {
		model any
		query string
		args  []any
	}{
		{&vmModels.VMStorageDataset{}, "guid IN ?", []any{guids}},
		{&sambaModels.SambaShare{}, "dataset IN ?", []any{guids}},
		{&utilitiesModels.Downloads{}, "storage_dataset_guid IN ?", []any{guids}},
		{&clusterModels.BackupJob{}, "source_dataset IN ? OR jail_root_dataset IN ?", []any{names, names}},
	} {
		if !s.DB.Migrator().HasTable(reference.model) {
			continue
		}
		var count int64
		if err := s.DB.WithContext(ctx).Model(reference.model).Where(reference.query, reference.args...).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("zfs_source_protected: dataset_is_referenced")
		}
	}
	return nil
}

func (s *Service) renameZFSSource(ctx context.Context, req jailServiceInterfaces.CreateJailRequest, op *jailModels.JailCreation, state *creationState) (*gzfs.Dataset, string, error) {
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()
	source := *req.ZFSSource
	tree, err := s.inspectZFSSource(ctx, source, req.Pool, op.ID)
	if err != nil {
		return nil, "", err
	}
	root, err := s.GZFS.ZFS.Get(ctx, source.Dataset, false)
	if err != nil || root == nil || root.GUID != source.GUID {
		return nil, "", fmt.Errorf("zfs_source_identity_changed")
	}
	if err := validateCopiedRoot(root.Mountpoint, string(req.Type)); err != nil {
		return nil, "", err
	}
	parent, err := s.GZFS.ZFS.Get(ctx, req.Pool+"/sylve/jails", false)
	if err != nil || parent == nil {
		return nil, "", fmt.Errorf("jail_dataset_mountpoint_not_usable")
	}
	rename := &creationRename{
		Source: source.Dataset, Destination: fmt.Sprintf("%s/%d", parent.Name, op.CTID),
		Mountpoint: root.Mountpoint, DestinationMountpoint: filepath.Join(parent.Mountpoint, fmt.Sprint(op.CTID)),
		Snapshot: "sylve_create_" + strings.ReplaceAll(op.ID, "-", ""), Tree: tree,
		Properties: make(map[string]map[string]gzfs.ZFSProperty, len(tree.Members)),
	}
	if err := requireNewCreationMountpoint(rename.DestinationMountpoint); err != nil {
		return nil, "", err
	}
	if existing, err := s.GZFS.ZFS.Get(ctx, rename.Destination, false); err != nil && !isZFSDatasetMissingError(err) {
		return nil, "", err
	} else if existing != nil {
		return nil, "", fmt.Errorf("jail_base_fs_with_ctid_already_exists")
	}
	for _, member := range tree.Members {
		props := make(map[string]gzfs.ZFSProperty, len(renameSourceProperties))
		for _, key := range renameSourceProperties {
			property, err := s.GZFS.ZFS.GetProperty(ctx, member.Dataset, key)
			if err != nil {
				return nil, "", err
			}
			props[key] = property
		}
		expectedMount := filepath.Join(rename.Mountpoint, member.MountRel)
		if props["mountpoint"].Value != expectedMount {
			return nil, "", fmt.Errorf("zfs_source_identity_changed")
		}
		if err := s.checkCreationMount(ctx, member.Dataset, expectedMount); err != nil {
			return nil, "", err
		}
		rename.Properties[member.Dataset] = props
	}
	if err := s.requireSourceStopped(ctx, rename.Mountpoint); err != nil {
		return nil, "", err
	}
	state.Rename = rename
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "snapshotting"); err != nil {
		return nil, "", err
	}
	if _, _, err := s.creationZFS().RunBytes(ctx, nil, "snapshot", "-r", "-o", creationOwnerProperty+"="+op.ID, source.Dataset+"@"+rename.Snapshot); err != nil {
		return nil, "", err
	}
	snapshot, err := s.GZFS.ZFS.Get(ctx, source.Dataset+"@"+rename.Snapshot, false)
	if err != nil || snapshot == nil {
		return nil, "", fmt.Errorf("jail_creation_snapshot_missing")
	}
	if err := s.resolveCreationSnapshots(ctx, rename.Snapshot, snapshot.GUID, &rename.Tree); err != nil {
		return nil, "", err
	}
	if err := s.verifyRenameTree(ctx, rename, source.Dataset); err != nil {
		return nil, "", err
	}
	rename.RestoreData = true
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "renaming"); err != nil {
		return nil, "", err
	}
	if err := s.unmountCreationTree(ctx, source.Dataset, source.Dataset, tree); err != nil {
		return nil, "", err
	}
	if _, _, err := s.creationZFS().RunBytes(ctx, nil, "rename", "-u", source.Dataset, rename.Destination); err != nil {
		return nil, "", err
	}
	if err := s.verifyRenameTree(ctx, rename, rename.Destination); err != nil {
		return nil, "", err
	}
	for _, member := range tree.Members {
		name := rename.Destination + strings.TrimPrefix(member.Dataset, rename.Source)
		ds, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil || ds == nil || ds.GUID != member.GUID {
			return nil, "", fmt.Errorf("zfs_source_identity_changed")
		}
		if err := ds.SetProperties(ctx, creationOwnerProperty, op.ID, "readonly", "off", "sharenfs", "off", "sharesmb", "off", "canmount", "noauto"); err != nil {
			return nil, "", err
		}
	}
	if err := s.mountCreationTree(ctx, rename.Destination, rename.DestinationMountpoint, source.Dataset, tree); err != nil {
		return nil, "", err
	}
	if err := validateCopiedRoot(rename.DestinationMountpoint, string(req.Type)); err != nil {
		return nil, "", err
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "verifying"); err != nil {
		return nil, "", err
	}
	confined, err := os.OpenRoot(rename.DestinationMountpoint)
	if err != nil {
		return nil, "", err
	}
	err = confined.RemoveAll(".sylve/host-config")
	confined.Close()
	if err != nil {
		return nil, "", err
	}
	for _, member := range tree.Members {
		ds, err := s.GZFS.ZFS.Get(ctx, rename.Destination+strings.TrimPrefix(member.Dataset, rename.Source), false)
		if err != nil || ds == nil {
			return nil, "", fmt.Errorf("zfs_source_identity_changed")
		}
		if err := ds.SetProperties(ctx, "canmount", "on"); err != nil {
			return nil, "", err
		}
	}
	ds, err := s.GZFS.ZFS.Get(ctx, rename.Destination, false)
	return ds, rename.DestinationMountpoint, err
}

func (s *Service) verifyRenameTree(ctx context.Context, rename *creationRename, root string) error {
	expected := make(map[string]string, len(rename.Tree.Members))
	for _, member := range rename.Tree.Members {
		expected[root+strings.TrimPrefix(member.Dataset, rename.Source)] = member.GUID
	}
	datasets, err := s.GZFS.ZFS.List(ctx, true, root)
	if err != nil {
		return err
	}
	for _, ds := range datasets {
		if ds.Type == gzfs.DatasetTypeSnapshot {
			continue
		}
		if guid, ok := expected[ds.Name]; !ok || guid == "" || ds.GUID != guid || ds.Type != gzfs.DatasetTypeFilesystem {
			return fmt.Errorf("zfs_source_identity_changed: %s", ds.Name)
		}
		delete(expected, ds.Name)
	}
	if len(expected) != 0 {
		return fmt.Errorf("zfs_source_identity_changed: missing_descendant")
	}
	return nil
}

func renameSnapshotResources(rename *creationRename, root string) []creationDataset {
	resources := make([]creationDataset, 0, len(rename.Tree.Members))
	for _, member := range rename.Tree.Members {
		resources = append(resources, creationDataset{
			Name: root + strings.TrimPrefix(member.Dataset, rename.Source) + "@" + rename.Snapshot,
			GUID: member.SnapshotGUID,
		})
	}
	return resources
}

func (s *Service) restoreRenamedSource(ctx context.Context, op *jailModels.JailCreation, state *creationState) error {
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()
	rename := state.Rename
	if len(rename.Tree.Members) == 0 || rename.Tree.Members[0].Dataset != rename.Source {
		return fmt.Errorf("jail_creation_rename_journal_invalid")
	}
	location := ""
	for _, name := range []string{rename.Source, rename.Destination} {
		ds, err := s.GZFS.ZFS.Get(ctx, name, false)
		if isZFSDatasetMissingError(err) || err == nil && ds == nil {
			continue
		}
		if err != nil {
			return err
		}
		if ds.GUID != rename.Tree.Members[0].GUID || location != "" {
			return fmt.Errorf("jail_creation_rename_identity_changed: %s", name)
		}
		location = name
	}
	if location == "" {
		return fmt.Errorf("jail_creation_rename_source_missing")
	}
	if err := s.verifyRenameTree(ctx, rename, location); err != nil {
		return err
	}
	snapshots := renameSnapshotResources(rename, location)
	if rename.Restored || !rename.RestoreData {
		if location != rename.Source {
			return fmt.Errorf("jail_creation_rename_identity_changed")
		}
		rename.Restored = true
		if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "rolling_back"); err != nil {
			return err
		}
		return s.cleanupCreationSnapshots(ctx, op, &snapshots)
	}
	for _, root := range []string{rename.Mountpoint, rename.DestinationMountpoint} {
		if err := s.requireSourceStopped(ctx, root); err != nil {
			return err
		}
	}
	for _, resource := range snapshots {
		snapshot, err := s.ownedCreationDataset(ctx, op.ID, resource)
		if err != nil {
			return err
		}
		if snapshot == nil || resource.GUID == "" {
			return fmt.Errorf("jail_creation_snapshot_missing: %s", resource.Name)
		}
	}
	members := orderedMountMembers(rename.Tree)
	for i := len(members) - 1; i >= 0; i-- {
		name := location + strings.TrimPrefix(members[i].Dataset, rename.Source)
		ds, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil || ds == nil {
			return fmt.Errorf("zfs_source_identity_changed: %s", name)
		}
		if ds.Properties["mounted"].Value == "yes" {
			if err := ds.Unmount(ctx, false); err != nil {
				return err
			}
		}
	}
	for _, resource := range snapshots {
		if _, _, err := s.creationZFS().RunBytes(ctx, nil, "rollback", resource.Name); err != nil {
			return err
		}
	}
	if location != rename.Source {
		if _, _, err := s.creationZFS().RunBytes(ctx, nil, "rename", "-u", location, rename.Source); err != nil {
			return err
		}
	}
	for _, member := range members {
		for _, key := range renameSourceProperties {
			property, ok := rename.Properties[member.Dataset][key]
			if !ok {
				return fmt.Errorf("jail_creation_rename_journal_invalid: %s", key)
			}
			if err := s.restoreRenameProperty(ctx, member.Dataset, key, property); err != nil {
				return err
			}
		}
		ds, err := s.GZFS.ZFS.Get(ctx, member.Dataset, false)
		if err != nil || ds == nil || ds.Mountpoint != filepath.Join(rename.Mountpoint, member.MountRel) {
			return fmt.Errorf("jail_creation_rename_mountpoint_changed: %s", member.Dataset)
		}
		path := ds.Mountpoint
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			path = filepath.Dir(path)
		} else if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
			return fmt.Errorf("jail_creation_rename_mountpoint_changed: unsafe_path")
		}
		if ds.Properties["mounted"].Value != "yes" {
			if err := ds.Mount(ctx, false); err != nil {
				return err
			}
		}
		if err := s.checkCreationMount(ctx, ds.Name, ds.Mountpoint); err != nil {
			return err
		}
	}
	if err := os.Remove(rename.DestinationMountpoint); err != nil && !os.IsNotExist(err) {
		return err
	}
	rename.Restored = true
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "rolling_back"); err != nil {
		return err
	}
	snapshots = renameSnapshotResources(rename, rename.Source)
	return s.cleanupCreationSnapshots(ctx, op, &snapshots)
}

func (s *Service) restoreRenameProperty(ctx context.Context, dataset, key string, property gzfs.ZFSProperty) error {
	var args []string
	if strings.EqualFold(property.Source.Type, "local") {
		args = []string{"set", key + "=" + property.Value, dataset}
	} else {
		args = []string{"inherit"}
		if key == "canmount" || strings.EqualFold(property.Source.Type, "received") {
			args = append(args, "-S")
		}
		args = append(args, key, dataset)
	}
	_, _, err := s.creationZFS().RunBytes(ctx, nil, args...)
	return err
}
