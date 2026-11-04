// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/logger"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
)

func (s *Service) PrepareStack(ctx context.Context, req containerServiceInterfaces.StackRequest) (containerServiceInterfaces.StackRequest, error) {
	if !nameRegex.MatchString(req.Stack) || len(req.Containers) == 0 {
		return req, fmt.Errorf("invalid_stack")
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).Where("stack = ?", req.Stack).Count(&count).Error; err != nil {
		return req, err
	}
	if count > 0 {
		return req, fmt.Errorf("catalog_stack_exists: %s", req.Stack)
	}
	next := req.OCID
	for i := range req.Containers {
		for ; !s.ocidFree(ctx, next); next++ {
			if next >= clusterModels.GuestIdentityMaxID {
				return req, fmt.Errorf("no_free_container_ids")
			}
		}
		create := &req.Containers[i]
		create.OCID, create.Pool, create.StartAtBoot = next, req.Pool, req.StartAtBoot
		create.Stack, create.Network = req.Stack, req.Stack
		next++
	}

	var network containerModels.Network
	if err := s.DB.WithContext(ctx).Where("name = ?", req.Stack).Limit(1).Find(&network).Error; err != nil {
		return req, err
	}
	if network.ID == 0 {
		if _, err := s.CreateNetwork(ctx, containerServiceInterfaces.NetworkRequest{Name: req.Stack}); err != nil {
			return req, err
		}
	}
	return req, nil
}

// stackFolders gives the folders a stack names relative to itself, like
// "./data/postgres", datasets under the stack's own, which its containers
// share as a compose project's share its folder. Deleting the stack keeps them,
// as it keeps any dataset picked for a mount.
func (s *Service) stackFolders(ctx context.Context, req containerServiceInterfaces.StackRequest) error {
	for _, create := range req.Containers {
		for i, mount := range create.Mounts {
			folder, relative := strings.CutPrefix(mount.Source, "./")
			if mount.Type != containerModels.MountDataset || !relative {
				continue
			}
			if folder = path.Clean(folder); folder == "." || strings.HasPrefix(folder, "..") {
				return fmt.Errorf("invalid_mount_dataset: %s", mount.Source)
			}
			name := req.Pool + "/sylve"
			parts := strings.Split("stacks/"+req.Stack+"/"+folder, "/")
			for j, part := range parts {
				name += "/" + part
				dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
				if err != nil && !storagezfs.DatasetDoesNotExist(err) {
					return err
				}
				if dataset != nil {
					continue
				}
				if dataset, err = s.GZFS.ZFS.CreateFilesystem(ctx, name, nil); err != nil {
					return fmt.Errorf("failed_to_create_stack_folder_%s: %w", folder, err)
				}
				if j == len(parts)-1 {
					mountpoint, err := storagezfs.FilesystemMountpoint(dataset)
					if err == nil {
						err = setPermissions(mountpoint, mount.Owner, "")
					}
					if err != nil {
						return fmt.Errorf("failed_to_set_stack_folder_permissions_%s: %w", folder, err)
					}
				}
			}
			create.Mounts[i].Source = name
		}
	}
	return nil
}

func (s *Service) createStack(ctx context.Context, req containerServiceInterfaces.StackRequest) error {
	if err := s.stackFolders(ctx, req); err != nil {
		return err
	}
	created := make([]bool, len(req.Containers))
	kept := make([]bool, len(req.Containers))
	errs := make([]error, len(req.Containers))
	report, _ := ctx.Value(progressKey{}).(func(string))
	var wg sync.WaitGroup
	for i, create := range req.Containers {
		dataset, err := s.GZFS.ZFS.Get(ctx, containerModels.VolumesDataset(create.Pool, create.OCID), false)
		kept[i] = err == nil && dataset != nil
		create.StartAfterCreate = false
		member := context.WithValue(ctx, progressKey{}, func(message string) {
			if report != nil {
				report(create.Name + ": " + message)
			}
		})
		wg.Go(func() {
			errs[i] = s.Create(member, create)
			created[i] = errs[i] == nil
		})
	}
	wg.Wait()

	failed := slices.IndexFunc(errs, func(err error) bool { return err != nil })
	for i, create := range req.Containers {
		if failed >= 0 || !create.StartAfterCreate {
			continue
		}
		if errs[i] = s.Action(ctx, create.OCID, "start"); errs[i] != nil {
			failed = i
		}
	}
	if failed < 0 {
		return nil
	}

	cleanup := context.WithoutCancel(ctx)
	for i, create := range req.Containers {
		if !created[i] {
			continue
		}
		if err := s.Delete(cleanup, create.OCID, !kept[i]); err != nil {
			logger.L.Warn().Err(err).Uint("ocid", create.OCID).Msg("container_stack_rollback_failed")
		}
	}
	if err := s.RemoveEmptyStack(cleanup, req.Stack); err != nil {
		logger.L.Warn().Err(err).Str("stack", req.Stack).Msg("container_stack_network_not_removed")
	}
	return fmt.Errorf("container_stack_failed: %s: %w", req.Containers[failed].Name, errs[failed])
}

func (s *Service) ocidFree(ctx context.Context, ocid uint) bool {
	var count int64
	if ocid < 1 || s.DB.WithContext(ctx).Model(&containerModels.Container{}).Where("oc_id = ?", ocid).Count(&count).Error != nil || count > 0 {
		return false
	}
	return s.identity == nil || s.identity.RequireGuestIDAvailable(ctx, ocid) == nil
}

func (s *Service) RemoveEmptyStack(ctx context.Context, stack string) error {
	if stack == "" {
		return nil
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).Where("stack = ?", stack).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	var network containerModels.Network
	if err := s.DB.WithContext(ctx).Where("name = ?", stack).Limit(1).Find(&network).Error; err != nil || network.ID == 0 {
		return err
	}
	return s.DeleteNetwork(ctx, network.ID)
}
