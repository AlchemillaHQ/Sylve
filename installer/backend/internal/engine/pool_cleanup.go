// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/alchemillahq/gzfs"
)

type installPools interface {
	GetPoolNames(context.Context) ([]string, error)
	GetPoolStatus(context.Context, string) (*gzfs.ZPoolStatusPool, error)
	Destroy(context.Context, string, bool) error
}

var poolProviderRe = regexp.MustCompile(`^[a-z][a-z0-9_]*[0-9]+(?:[ps][0-9]+)*$`)

func resolvePoolProvider(path string) string {
	provider := strings.TrimSuffix(strings.TrimPrefix(path, "/dev/"), ".eli")
	if strings.Contains(provider, "/") {
		provider = strings.TrimSuffix(strings.TrimPrefix(glabelProvider(provider), "/dev/"), ".eli")
	}
	if poolProviderRe.MatchString(provider) {
		return provider
	}
	return ""
}

func poolTargetScope(pool *gzfs.ZPoolStatusPool, storage ZFSStorage, resolve func(string) string) (allSelected, anySelected bool, err error) {
	if pool == nil || pool.PoolGUID == "" {
		return false, false, fmt.Errorf("pool identity is unavailable")
	}
	whole, partitions := map[string]bool{}, map[string]bool{}
	for _, vdev := range storage.Vdevs {
		for _, member := range vdev.Disks {
			if zfsMemberIsPartition(member) {
				partitions[member] = true
			} else {
				whole[member] = true
			}
		}
	}
	allSelected = true
	leaves := 0
	var walk func(map[string]*gzfs.ZPoolStatusVDEV) error
	walk = func(vdevs map[string]*gzfs.ZPoolStatusVDEV) error {
		for _, vdev := range vdevs {
			if vdev == nil {
				return fmt.Errorf("pool has an unknown device")
			}
			if len(vdev.Vdevs) > 0 {
				if err := walk(vdev.Vdevs); err != nil {
					return err
				}
				continue
			}
			if vdev.VdevType != "disk" {
				return fmt.Errorf("cannot verify pool device %q (%s)", vdev.Name, vdev.VdevType)
			}
			path := vdev.Path
			if path == "" {
				path = vdev.Name
			}
			provider := resolve(path)
			if provider == "" {
				return fmt.Errorf("cannot resolve pool device %q", path)
			}
			selected := partitions[provider] || whole[provider] || whole[partitionParent(provider)]
			allSelected = allSelected && selected
			anySelected = anySelected || selected
			leaves++
		}
		return nil
	}
	if err := walk(pool.Vdevs); err != nil {
		return false, false, err
	}
	if leaves == 0 {
		return false, false, fmt.Errorf("pool has no verifiable data devices")
	}
	for _, group := range []map[string]*gzfs.ZPoolStatusVDEV{
		pool.Logs, pool.Spares, pool.L2Cache, pool.Special, pool.Dedup,
	} {
		if err := walk(group); err != nil {
			return false, false, err
		}
	}
	return allSelected, anySelected, nil
}

func cleanupTargetPools(ctx context.Context, pools installPools, storage ZFSStorage, resolve func(string) string) error {
	root := poolNameOf(storage)
	if root == "bootpool" {
		return fmt.Errorf("pool name %q is reserved for the installer bootpool", root)
	}
	names, err := pools.GetPoolNames(ctx)
	if err != nil {
		return fmt.Errorf("inspect imported pools before partitioning: %w", err)
	}
	var cleanup []*gzfs.ZPoolStatusPool
	for _, name := range []string{root, "bootpool"} {
		if !slices.Contains(names, name) {
			continue
		}
		pool, err := pools.GetPoolStatus(ctx, name)
		if err != nil {
			return fmt.Errorf("inspect pool %s before partitioning: %w", name, err)
		}
		allSelected, anySelected, err := poolTargetScope(pool, storage, resolve)
		if err != nil {
			return fmt.Errorf("refusing to destroy pool %s: %w", name, err)
		}
		if !allSelected {
			if name == "bootpool" && !storage.Encrypt && !anySelected {
				continue
			}
			return fmt.Errorf("pool %s uses devices outside the selected targets — export it or choose another pool name", name)
		}
		if pool.Name != name {
			return fmt.Errorf("pool %s identity does not match its status", name)
		}
		cleanup = append(cleanup, pool)
	}
	for _, previous := range cleanup {
		current, err := pools.GetPoolStatus(ctx, previous.Name)
		if err != nil {
			return fmt.Errorf("recheck pool %s before cleanup: %w", previous.Name, err)
		}
		allSelected, _, scopeErr := poolTargetScope(current, storage, resolve)
		if scopeErr != nil || !allSelected || current.PoolGUID != previous.PoolGUID || current.Name != previous.Name {
			return fmt.Errorf("pool %s changed during validation — refusing cleanup", previous.Name)
		}
		if err := pools.Destroy(ctx, previous.Name, true); err != nil {
			return fmt.Errorf("clean up pool %s: %w", previous.Name, err)
		}
	}
	return nil
}
