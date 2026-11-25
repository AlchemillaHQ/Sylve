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
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
)

var poolDevicePathPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.:/-]*$`)
var poolDeviceParentPattern = regexp.MustCompile(`^(root|mirror|raidz[0-9]*|draid[0-9]*)$`)
var poolOpticalDiskPattern = regexp.MustCompile(`^a?cd[0-9]+$`)

type poolDiskIndex map[string]*diskServiceInterfaces.PoolDevice

type poolDevicePreflight struct {
	pool           *gzfs.ZPool
	imported       []*gzfs.ZPool
	disks          poolDiskIndex
	source, parent *gzfs.ZPoolStatusVDEV
	role           string
	members, used  []string
	checks         zfsServiceInterfaces.PoolDeviceMutationOptions
}

func validPoolDeviceGUID(guid string) bool {
	value, err := strconv.ParseUint(guid, 10, 64)
	return err == nil && value > 0 && strconv.FormatUint(value, 10) == guid
}

func normalizePoolDevicePath(value string) (string, error) {
	name := strings.TrimPrefix(value, "/dev/")
	if len(value) == 0 || len(value) > 1024 || !poolDevicePathPattern.MatchString(name) {
		return "", classifyError(ErrInvalidRequest, "invalid_pool_device_path")
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", classifyError(ErrInvalidRequest, "invalid_pool_device_path")
		}
	}
	return "/dev/" + name, nil
}

func poolDeviceMutationOptions(options []zfsServiceInterfaces.PoolDeviceMutationOptions) (zfsServiceInterfaces.PoolDeviceMutationOptions, error) {
	var checks zfsServiceInterfaces.PoolDeviceMutationOptions
	if len(options) > 1 {
		return checks, classifyError(ErrInvalidRequest, "invalid_pool_device_preconditions")
	}
	if len(options) == 1 {
		checks = options[0]
	}
	if len(checks.ExpectedNewIdentity) > 1024 {
		return checks, classifyError(ErrInvalidRequest, "invalid_replacement_identity")
	}
	if checks.ExpectedParentGUID != "" || checks.ExpectedMemberGUIDs != nil {
		if !validPoolDeviceGUID(checks.ExpectedParentGUID) || len(checks.ExpectedMemberGUIDs) == 0 || len(checks.ExpectedMemberGUIDs) > 4096 {
			return checks, classifyError(ErrInvalidRequest, "invalid_pool_device_preconditions")
		}
		checks.ExpectedMemberGUIDs = slices.Clone(checks.ExpectedMemberGUIDs)
		slices.Sort(checks.ExpectedMemberGUIDs)
		for i, guid := range checks.ExpectedMemberGUIDs {
			if !validPoolDeviceGUID(guid) || (i > 0 && checks.ExpectedMemberGUIDs[i-1] == guid) {
				return checks, classifyError(ErrInvalidRequest, "invalid_pool_device_preconditions")
			}
		}
	}
	return checks, nil
}

func indexPoolDisks(devices []diskServiceInterfaces.PoolDevice) poolDiskIndex {
	index := make(poolDiskIndex)
	for i := range devices {
		disk := &devices[i]
		for _, path := range append([]string{disk.Path}, disk.Aliases...) {
			if previous, exists := index[path]; exists && previous != disk {
				index[path] = nil
			} else {
				index[path] = disk
			}
		}
	}
	return index
}

func (index poolDiskIndex) find(value string) (*diskServiceInterfaces.PoolDevice, error) {
	path, err := normalizePoolDevicePath(value)
	if err != nil {
		return nil, nil
	}
	disk, exists := index[path]
	if exists && disk == nil {
		return nil, classifyError(ErrConflict, "pool_device_path_is_ambiguous: %s", path)
	}
	return disk, nil
}

func (p *poolDevicePreflight) findSource(status *gzfs.ZPoolStatusPool, source string) error {
	requested, err := p.disks.find(source)
	if err != nil {
		return err
	}
	aliases := map[string]bool{source: true}
	if requested != nil {
		for _, path := range append([]string{requested.Path}, requested.Aliases...) {
			aliases[path] = true
		}
	}
	var ancestors []*gzfs.ZPoolStatusVDEV
	var sourcePaths []string
	var walk func(map[string]*gzfs.ZPoolStatusVDEV, string, []*gzfs.ZPoolStatusVDEV) error
	walk = func(nodes map[string]*gzfs.ZPoolStatusVDEV, role string, parents []*gzfs.ZPoolStatusVDEV) error {
		for key, node := range nodes {
			if node == nil {
				return fmt.Errorf("%w: incomplete device topology", ErrPoolDeviceStatusUnavailable)
			}
			if node.VdevType == "replacing" {
				return classifyError(ErrConflict, "pool_device_replacement_active")
			}
			matched := node.GUID == source
			paths := []string{node.Path, node.Name, key}
			for _, name := range paths {
				matched = matched || aliases["/dev/"+strings.TrimPrefix(name, "/dev/")]
			}
			if len(node.Vdevs) == 0 {
				p.used = append(p.used, paths...)
			}
			if matched {
				if p.source != nil {
					return classifyError(ErrConflict, "pool_device_identity_is_ambiguous")
				}
				p.source, p.role, sourcePaths = node, role, paths
				ancestors = slices.Clone(parents)
			}
			if err := walk(node.Vdevs, role, append(parents, node)); err != nil {
				return err
			}
		}
		return nil
	}
	for role, nodes := range map[string]map[string]*gzfs.ZPoolStatusVDEV{
		"vdevs": status.Vdevs, "logs": status.Logs, "special": status.Special,
		"dedup": status.Dedup, "spares": status.Spares, "l2cache": status.L2Cache,
	} {
		if err := walk(nodes, role, nil); err != nil {
			return err
		}
	}
	if p.source == nil {
		return classifyError(ErrConflict, "pool_device_not_found_or_changed")
	}
	node := p.source
	if !slices.Contains([]string{"vdevs", "logs", "special", "dedup"}, p.role) || node.VdevType != "disk" || len(node.Vdevs) != 0 || slices.Contains([]string{"spare", "spares", "cache", "l2cache"}, strings.ToLower(node.Class)) {
		return classifyError(ErrConflict, "pool_device_action_requires_a_member_disk")
	}
	if !validPoolDeviceGUID(node.GUID) || !slices.Contains([]string{"ONLINE", "DEGRADED", "FAULTED", "UNAVAIL", "UNAVAILABLE", "OFFLINE", "REMOVED"}, strings.ToUpper(node.State)) {
		return fmt.Errorf("%w: source identity or health unavailable", ErrPoolDeviceStatusUnavailable)
	}
	for _, parent := range ancestors {
		if !validPoolDeviceGUID(parent.GUID) || !poolDeviceParentPattern.MatchString(parent.VdevType) {
			return classifyError(ErrConflict, "pool_device_topology_not_supported")
		}
		if !slices.Contains([]string{"ONLINE", "DEGRADED"}, strings.ToUpper(parent.State)) {
			return fmt.Errorf("%w: parent VDEV health unavailable", ErrPoolDeviceStatusUnavailable)
		}
		p.parent = parent
	}
	identified := false
	for _, path := range sourcePaths {
		disk, err := p.disks.find(path)
		if err != nil {
			return err
		}
		if disk != nil {
			identified = true
			if disk.Boot {
				return classifyError(ErrConflict, "boot_pool_device_changes_not_supported")
			}
		}
	}
	if !identified && slices.Contains([]string{"ONLINE", "DEGRADED"}, strings.ToUpper(node.State)) {
		return fmt.Errorf("%w: source physical disk unavailable", ErrPoolDeviceStatusUnavailable)
	}
	if p.parent != nil {
		for _, child := range p.parent.Vdevs {
			if !validPoolDeviceGUID(child.GUID) {
				return fmt.Errorf("%w: member identity unavailable", ErrPoolDeviceStatusUnavailable)
			}
			p.members = append(p.members, child.GUID)
		}
		slices.Sort(p.members)
		for i := 1; i < len(p.members); i++ {
			if p.members[i-1] == p.members[i] {
				return fmt.Errorf("%w: ambiguous member topology", ErrPoolDeviceStatusUnavailable)
			}
		}
	}
	if p.checks.ExpectedParentGUID != "" && (p.parent == nil || p.parent.GUID != p.checks.ExpectedParentGUID || !slices.Equal(p.members, p.checks.ExpectedMemberGUIDs)) {
		return classifyError(ErrConflict, "pool_device_membership_changed")
	}
	return nil
}

func (s *Service) readPoolDevicePreflight(ctx context.Context, guid, source string, checks zfsServiceInterfaces.PoolDeviceMutationOptions) (poolDevicePreflight, error) {
	p := poolDevicePreflight{checks: checks}
	disks, err := s.PoolDeviceDisks.ReadPoolDeviceInventory(ctx)
	if err != nil {
		return p, fmt.Errorf("%w: %w", ErrPoolDeviceStatusUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	p.disks = indexPoolDisks(disks)
	scope, err := s.loadManagedPoolScope(ctx)
	if err != nil {
		return p, fmt.Errorf("%w: %w", ErrPoolDeviceStatusUnavailable, err)
	}
	p.imported = scope.importedPools
	for _, pool := range scope.pools {
		if pool.PoolGUID == guid {
			p.pool = pool
			break
		}
	}
	if p.pool == nil {
		return p, classifyError(ErrPoolNotFound, "pool_unavailable_or_unmanaged")
	}
	bootfs := p.pool.Properties["bootfs"].Value
	if bootfs == "" {
		return p, fmt.Errorf("%w: boot-pool status unavailable", ErrPoolDeviceStatusUnavailable)
	}
	if !slices.Contains([]string{"-", "none"}, strings.ToLower(bootfs)) {
		return p, classifyError(ErrConflict, "boot_pool_device_changes_not_supported")
	}
	if p.pool.Properties["readonly"].Value == "on" {
		return p, classifyError(ErrConflict, "pool_is_read_only")
	}
	status, err := p.pool.Status(ctx)
	if err != nil {
		return p, fmt.Errorf("%w: %w", ErrPoolDeviceStatusUnavailable, err)
	}
	if status == nil || status.PoolGUID != guid || status.Name != p.pool.Name {
		return p, classifyError(ErrConflict, "pool_identity_changed")
	}
	for _, node := range status.Vdevs {
		if node != nil && node.VdevType == "root" && node.GUID != guid {
			return p, classifyError(ErrConflict, "pool_identity_changed")
		}
	}
	if _, err := poolScrubStartBaseline(status, guid); err != nil {
		return p, err
	}
	if !slices.Contains([]string{"ONLINE", "DEGRADED"}, strings.ToUpper(status.State)) {
		return p, classifyError(ErrConflict, "pool_unavailable_for_device_changes")
	}
	err = p.findSource(status, source)
	return p, err
}

func (p poolDevicePreflight) replacement(path string) (*diskServiceInterfaces.PoolDevice, error) {
	disk, err := p.disks.find(path)
	if err != nil {
		return nil, err
	}
	if disk == nil || !disk.IdentityStable || disk.Identity == "" || disk.Size == 0 {
		return nil, classifyError(ErrConflict, "replacement_device_identity_unavailable_or_changed")
	}
	canonical, err := normalizePoolDevicePath(disk.Path)
	if err != nil || canonical != disk.Path || poolOpticalDiskPattern.MatchString(disk.Disk) {
		return nil, classifyError(ErrConflict, "replacement_device_not_supported")
	}
	if p.checks.ExpectedNewIdentity != "" && p.checks.ExpectedNewIdentity != disk.Identity {
		return nil, classifyError(ErrConflict, "replacement_device_identity_changed")
	}
	if disk.Boot || disk.Swap || disk.Busy || disk.Partitioned || (disk.Partition && !slices.Contains([]string{"", "unused", "unused entry", "zfs", "freebsd-zfs"}, strings.ToLower(disk.PartitionType))) {
		return nil, classifyError(ErrConflict, "replacement_device_is_protected_or_in_use")
	}
	return disk, nil
}

func (p poolDevicePreflight) checkReplacement(disk *diskServiceInterfaces.PoolDevice) error {
	paths := p.used
	var sizes []uint64
	var walk func(map[string]*gzfs.ZPoolVDEV, bool) error
	walk = func(nodes map[string]*gzfs.ZPoolVDEV, sourcePool bool) error {
		for key, node := range nodes {
			if node == nil {
				return fmt.Errorf("%w: incomplete assigned-device inventory", ErrPoolDeviceStatusUnavailable)
			}
			if sourcePool && node.GUID == p.source.GUID {
				sizes = append(sizes, node.Size)
			}
			paths = append(paths, node.Path, node.Name, key)
			if err := walk(node.Vdevs, sourcePool); err != nil {
				return err
			}
		}
		return nil
	}
	for _, pool := range p.imported {
		if pool == nil {
			return fmt.Errorf("%w: incomplete pool inventory", ErrPoolDeviceStatusUnavailable)
		}
		for _, group := range []map[string]*gzfs.ZPoolVDEV{pool.Vdevs, pool.Logs, pool.Special, pool.Dedup, pool.Spares, pool.L2Cache} {
			if err := walk(group, pool.PoolGUID == p.pool.PoolGUID); err != nil {
				return err
			}
		}
	}
	for _, path := range paths {
		assigned, err := p.disks.find(path)
		if err != nil {
			return err
		}
		if assigned != nil && assigned.Disk == disk.Disk {
			return classifyError(ErrConflict, "replacement_physical_disk_is_already_assigned")
		}
	}
	var minimum uint64
	for _, raw := range []string{p.source.RepDevSize, p.source.TotalSpace} {
		if raw != "" {
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("%w: replacement size invalid", ErrPoolDeviceStatusUnavailable)
			}
			if value > 0 {
				minimum = value
				break
			}
		}
	}
	if minimum == 0 && len(sizes) == 1 {
		minimum = sizes[0]
	}
	if minimum == 0 {
		return fmt.Errorf("%w: replacement size unavailable", ErrPoolDeviceStatusUnavailable)
	}
	if disk.Size < minimum {
		return classifyError(ErrConflict, "replacement_device_too_small: minimum %d bytes", minimum)
	}
	return nil
}

func (p poolDevicePreflight) newDevice(path string, before poolDevicePreflight) (*diskServiceInterfaces.PoolDevice, error) {
	previous, err := before.replacement(path)
	if err != nil {
		return nil, err
	}
	disk, err := p.replacement(path)
	if err != nil {
		return nil, err
	}
	if disk.Path != previous.Path || disk.Identity != previous.Identity || disk.Size != previous.Size {
		return nil, classifyError(ErrConflict, "replacement_device_identity_changed")
	}
	if err := p.checkReplacement(disk); err != nil {
		return nil, err
	}
	return disk, nil
}

func (s *Service) ReplaceDevice(ctx context.Context, guid, old, latest string, options ...zfsServiceInterfaces.PoolDeviceMutationOptions) error {
	path, err := normalizePoolDevicePath(latest)
	if err != nil {
		return err
	}
	return s.withPoolDeviceMutation(ctx, guid, old, options, func(ctx context.Context, before, current poolDevicePreflight) error {
		disk, err := current.newDevice(path, before)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := current.pool.ReplaceDevice(ctx, current.source.GUID, disk.Path, false); err != nil {
			return classifyError(ErrConflict, "failed_to_replace_device %s: %v", current.source.GUID, err)
		}
		return nil
	})
}

func (s *Service) AttachDevice(ctx context.Context, guid, device, latest string, options ...zfsServiceInterfaces.PoolDeviceMutationOptions) error {
	path, err := normalizePoolDevicePath(latest)
	if err != nil {
		return err
	}
	return s.withPoolDeviceMutation(ctx, guid, device, options, func(ctx context.Context, before, current poolDevicePreflight) error {
		for _, sample := range []poolDevicePreflight{before, current} {
			if sample.parent != nil && sample.parent.VdevType != "root" && sample.parent.VdevType != "mirror" {
				return classifyError(ErrConflict, "attach_requires_a_mirror_or_single_disk_vdev")
			}
			if sample.source.State != "ONLINE" {
				return classifyError(ErrConflict, "attach_requires_a_healthy_existing_member")
			}
			for _, raw := range []string{sample.source.ReadErrors, sample.source.WriteErrors, sample.source.ChkErrors} {
				count, err := strconv.ParseUint(raw, 10, 64)
				if err != nil || count != 0 {
					return classifyError(ErrConflict, "attach_requires_a_verified_error_free_existing_member")
				}
			}
		}
		disk, err := current.newDevice(path, before)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		command := gzfs.Cmd{Bin: "zpool", Runner: s.poolDeviceRunner}
		if _, _, err := command.RunBytes(ctx, nil, "attach", current.pool.Name, current.source.GUID, disk.Path); err != nil {
			return classifyError(ErrConflict, "attach_failed: %v", err)
		}
		return nil
	})
}

func (s *Service) DetachDevice(ctx context.Context, guid, device string, options ...zfsServiceInterfaces.PoolDeviceMutationOptions) error {
	if len(options) == 1 && options[0].ExpectedNewIdentity != "" {
		return classifyError(ErrInvalidRequest, "invalid_replacement_identity")
	}
	return s.withPoolDeviceMutation(ctx, guid, device, options, func(ctx context.Context, _, current poolDevicePreflight) error {
		parent := current.parent
		if parent == nil || parent.VdevType != "mirror" || len(parent.Vdevs) < 2 {
			return classifyError(ErrConflict, "detach_requires_an_individual_mirror_member")
		}
		for _, sibling := range parent.Vdevs {
			if sibling.GUID == current.source.GUID {
				continue
			}
			if sibling.VdevType != "disk" || len(sibling.Vdevs) != 0 || sibling.State != "ONLINE" {
				return classifyError(ErrConflict, "detach_requires_healthy_remaining_mirror_members")
			}
			for _, raw := range []string{sibling.ReadErrors, sibling.WriteErrors, sibling.ChkErrors} {
				count, err := strconv.ParseUint(raw, 10, 64)
				if err != nil || count != 0 {
					return classifyError(ErrConflict, "detach_requires_verified_error_free_remaining_members")
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := current.pool.Detach(ctx, current.source.GUID); err != nil {
			return classifyError(ErrConflict, "detach_failed: %v", err)
		}
		return nil
	})
}

func (s *Service) withPoolDeviceMutation(ctx context.Context, guid, source string, options []zfsServiceInterfaces.PoolDeviceMutationOptions, apply func(context.Context, poolDevicePreflight, poolDevicePreflight) error) error {
	if err := validateScrubGUID(guid); err != nil {
		return err
	}
	if !validPoolDeviceGUID(source) {
		path, err := normalizePoolDevicePath(source)
		if err != nil {
			return err
		}
		source = path
	}
	checks, err := poolDeviceMutationOptions(options)
	if err != nil {
		return err
	}
	return s.withPoolScrubMutation(ctx, guid, func(ctx context.Context) error {
		if s.syncMutex == nil || s.PoolDeviceDisks == nil {
			return fmt.Errorf("%w: disk mutation coordinator unavailable", ErrPoolDeviceStatusUnavailable)
		}
		s.syncMutex.Lock()
		defer s.syncMutex.Unlock()
		release := s.PoolDeviceDisks.LockPoolDeviceMutation()
		defer release()
		var queued int64
		if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubSchedule{}).
			Where("pool_guid = ? AND enabled = ? AND last_status = ?", guid, true, "queued").Count(&queued).Error; err != nil {
			return err
		}
		if queued > 0 {
			return classifyError(ErrConflict, "pool_scrub_schedule_queued")
		}
		before, err := s.readPoolDevicePreflight(ctx, guid, source, checks)
		if err != nil {
			return err
		}
		if before.parent != nil {
			checks.ExpectedParentGUID, checks.ExpectedMemberGUIDs = before.parent.GUID, before.members
		}
		current, err := s.readPoolDevicePreflight(ctx, guid, source, checks)
		if err != nil {
			return err
		}
		if before.pool.Name != current.pool.Name || before.source.GUID != current.source.GUID || before.role != current.role || (before.parent == nil) != (current.parent == nil) {
			return classifyError(ErrConflict, "pool_device_topology_changed")
		}
		if before.parent != nil && before.parent.VdevType != current.parent.VdevType {
			return classifyError(ErrConflict, "pool_device_topology_changed")
		}
		return apply(ctx, before, current)
	})
}
