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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

type vmTemplateCreateTarget struct {
	RID  uint
	Name string
}

type vmTemplateCreatePlan struct {
	Template     vmModels.VMTemplate
	Targets      []vmTemplateCreateTarget
	StoragePools map[uint]string
}

type vmTemplateStorageClone struct {
	TemplateStorage vmModels.VMTemplateStorage
	CreatedStorage  vmModels.Storage
}

func vmTemplateStoragePrefix(storageType vmModels.VMStorageType) (string, error) {
	switch storageType {
	case vmModels.VMStorageTypeRaw:
		return "raw", nil
	case vmModels.VMStorageTypeZVol:
		return "zvol", nil
	default:
		return "", fmt.Errorf("storage_type_not_cloneable: %s", storageType)
	}
}

func vmTemplateStorageDatasetPath(pool string, templateID uint, storageType vmModels.VMStorageType, sourceStorageID uint) (string, error) {
	prefix, err := vmTemplateStoragePrefix(storageType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/sylve/virtual-machines/templates/%d/%s-%d", pool, templateID, prefix, sourceStorageID), nil
}

func vmTargetStorageDatasetPath(pool string, rid uint, storageType vmModels.VMStorageType, storageID uint) (string, error) {
	prefix, err := vmTemplateStoragePrefix(storageType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/sylve/virtual-machines/%d/%s-%d", pool, rid, prefix, storageID), nil
}

func datasetEstimatedUsed(used, referenced uint64) uint64 {
	if used > 0 {
		return used
	}
	return referenced
}

func (s *Service) ensureDatasetPath(ctx context.Context, dataset string) error {
	dataset = strings.TrimSpace(strings.Trim(dataset, "/"))
	if dataset == "" {
		return fmt.Errorf("dataset_required")
	}

	parts := strings.Split(dataset, "/")
	if len(parts) == 0 {
		return fmt.Errorf("dataset_required")
	}

	current := strings.TrimSpace(parts[0])
	if current == "" {
		return fmt.Errorf("dataset_pool_required")
	}

	for idx := 1; idx < len(parts); idx++ {
		seg := strings.TrimSpace(parts[idx])
		if seg == "" {
			continue
		}
		current = current + "/" + seg

		existing, err := s.GZFS.ZFS.Get(ctx, current, false)
		if err == nil && existing != nil {
			continue
		}

		if _, err := s.GZFS.ZFS.CreateFilesystem(ctx, current, map[string]string{}); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "dataset already exists") || strings.Contains(msg, "exists") {
				continue
			}
			return fmt.Errorf("failed_to_create_dataset_%s: %w", current, err)
		}
	}

	return nil
}

func (s *Service) isClusterEnabled() (bool, error) {
	var cluster clusterModels.Cluster
	if err := s.DB.Select("enabled").First(&cluster).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed_to_get_cluster_state: %w", err)
	}
	return cluster.Enabled, nil
}

func (s *Service) checkPoolCapacity(ctx context.Context, pool string, requiredBytes uint64) error {
	zpool, err := s.GZFS.Zpool.Get(ctx, pool)
	if err != nil {
		return fmt.Errorf("failed_to_get_pool: %w", err)
	}
	if zpool == nil {
		return fmt.Errorf("pool_not_found")
	}
	if requiredBytes > zpool.Free {
		return fmt.Errorf("insufficient_pool_space")
	}
	return nil
}

func (s *Service) getUsablePoolSet(ctx context.Context) (map[string]struct{}, error) {
	usable, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	set := make(map[string]struct{}, len(usable))
	for _, pool := range usable {
		if pool == nil {
			continue
		}
		name := strings.TrimSpace(pool.Name)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	return set, nil
}

func (s *Service) resolveSwitchName(switchID uint, switchType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(switchType)) {
	case "standard":
		var sw networkModels.StandardSwitch
		if err := s.DB.Select("id", "name").First(&sw, "id = ?", switchID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("switch_not_found")
			}
			return "", fmt.Errorf("failed_to_get_standard_switch: %w", err)
		}
		return sw.Name, nil
	case "manual":
		var sw networkModels.ManualSwitch
		if err := s.DB.Select("id", "name").First(&sw, "id = ?", switchID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("switch_not_found")
			}
			return "", fmt.Errorf("failed_to_get_manual_switch: %w", err)
		}
		return sw.Name, nil
	default:
		return "", fmt.Errorf("invalid_switch_type")
	}
}

func (s *Service) resolveSwitchID(switchName, switchType string) (uint, error) {
	switchName = strings.TrimSpace(switchName)
	switchType = strings.ToLower(strings.TrimSpace(switchType))
	if switchName == "" {
		return 0, fmt.Errorf("switch_name_required")
	}

	switch switchType {
	case "standard":
		var sw networkModels.StandardSwitch
		if err := s.DB.Select("id", "name").First(&sw, "name = ?", switchName).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, fmt.Errorf("switch_not_found")
			}
			return 0, fmt.Errorf("failed_to_get_standard_switch: %w", err)
		}
		return sw.ID, nil
	case "manual":
		var sw networkModels.ManualSwitch
		if err := s.DB.Select("id", "name").First(&sw, "name = ?", switchName).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, fmt.Errorf("switch_not_found")
			}
			return 0, fmt.Errorf("failed_to_get_manual_switch: %w", err)
		}
		return sw.ID, nil
	default:
		return 0, fmt.Errorf("invalid_switch_type")
	}
}

func vmStorageSourceDatasetName(storage vmModels.Storage, rid uint) (string, error) {
	if storage.Type != vmModels.VMStorageTypeRaw && storage.Type != vmModels.VMStorageTypeZVol {
		return "", fmt.Errorf("storage_type_not_cloneable: %s", storage.Type)
	}

	if strings.TrimSpace(storage.Dataset.Name) != "" {
		return strings.TrimSpace(storage.Dataset.Name), nil
	}

	pool := strings.TrimSpace(storage.Pool)
	if pool == "" {
		pool = strings.TrimSpace(storage.Dataset.Pool)
	}
	if pool == "" {
		return "", fmt.Errorf("storage_pool_missing")
	}

	prefix, err := vmTemplateStoragePrefix(storage.Type)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/sylve/virtual-machines/%d/%s-%d", pool, rid, prefix, storage.ID), nil
}

func templateHasCloudInit(template vmModels.VMTemplate) bool {
	return strings.TrimSpace(template.CloudInitData) != "" ||
		strings.TrimSpace(template.CloudInitMetaData) != "" ||
		strings.TrimSpace(template.CloudInitNetworkConfig) != ""
}

func normalizeVMTemplateName(name string) string {
	return strings.TrimSpace(name)
}

func (s *Service) ensureUniqueVMTemplateName(name string) error {
	normalized := normalizeVMTemplateName(name)
	if normalized == "" {
		return fmt.Errorf("template_name_required")
	}
	if len(normalized) > 120 {
		return fmt.Errorf("template_name_too_long")
	}

	var count int64
	if err := s.DB.Model(&vmModels.VMTemplate{}).
		Where("LOWER(name) = ?", strings.ToLower(normalized)).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_template_name_uniqueness: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("template_name_already_in_use")
	}

	return nil
}

func rewriteCloudInitMetadataIdentity(metadata, prefix, vmName string, rid uint) (string, error) {
	identityPrefix := strings.TrimSpace(prefix)
	if identityPrefix == "" {
		identityPrefix = strings.TrimSpace(vmName)
	}
	if identityPrefix == "" {
		identityPrefix = "vm"
	}

	value := fmt.Sprintf("%s-%d", identityPrefix, rid)

	meta := map[string]any{}
	trimmed := strings.TrimSpace(metadata)
	if trimmed != "" {
		if err := yaml.Unmarshal([]byte(metadata), &meta); err != nil {
			return "", fmt.Errorf("invalid_cloud_init_metadata_yaml: %w", err)
		}
	}

	meta["local-hostname"] = value
	meta["instance-id"] = value

	out, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("failed_to_marshal_cloud_init_metadata: %w", err)
	}

	return string(out), nil
}

func (s *Service) getNextFreeVNCPort() (int, error) {
	var usedPorts []int
	if err := s.DB.Model(&vmModels.VM{}).Where("vnc_port > 0").Pluck("vnc_port", &usedPorts).Error; err != nil {
		return 0, fmt.Errorf("failed_to_list_used_vnc_ports: %w", err)
	}

	used := make(map[int]struct{}, len(usedPorts))
	for _, port := range usedPorts {
		if port > 0 {
			used[port] = struct{}{}
		}
	}

	for port := 5900; port <= 65535; port++ {
		if _, exists := used[port]; exists {
			continue
		}
		if utils.IsPortInUse(port) {
			continue
		}
		return port, nil
	}

	return 0, fmt.Errorf("no_available_vnc_port")
}

func (s *Service) createUniqueMACObject(tx *gorm.DB, baseName string) (uint, error) {
	base := strings.TrimSpace(baseName)
	if base == "" {
		base = "vm-template-mac"
	}

	resolved := base
	for i := 0; ; i++ {
		if i > 0 {
			resolved = fmt.Sprintf("%s-%d", base, i)
		}

		var count int64
		if err := tx.Model(&networkModels.Object{}).Where("name = ?", resolved).Count(&count).Error; err != nil {
			return 0, fmt.Errorf("failed_to_check_mac_object_name: %w", err)
		}
		if count == 0 {
			break
		}
	}

	macAddress := utils.GenerateRandomMAC()
	obj := networkModels.Object{
		Type: "Mac",
		Name: resolved,
	}
	if err := tx.Create(&obj).Error; err != nil {
		return 0, fmt.Errorf("failed_to_create_mac_object: %w", err)
	}

	entry := networkModels.ObjectEntry{
		ObjectID: obj.ID,
		Value:    macAddress,
	}
	if err := tx.Create(&entry).Error; err != nil {
		return 0, fmt.Errorf("failed_to_create_mac_entry: %w", err)
	}

	return obj.ID, nil
}

func (s *Service) buildVMTemplateTargets(template vmModels.VMTemplate, req libvirtServiceInterfaces.CreateFromTemplateRequest) ([]vmTemplateCreateTarget, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "single"
	}

	if mode == "single" {
		if req.RID == 0 || req.RID > 9999 {
			return nil, fmt.Errorf("invalid_rid")
		}

		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = strings.TrimSpace(template.SourceVMName)
			if name == "" {
				name = fmt.Sprintf("vm-%d", req.RID)
			}
		}
		if !utils.IsValidVMName(name) {
			return nil, fmt.Errorf("invalid_vm_name")
		}

		return []vmTemplateCreateTarget{
			{
				RID:  req.RID,
				Name: name,
			},
		}, nil
	}

	if mode != "multiple" {
		return nil, fmt.Errorf("invalid_mode")
	}

	if req.StartRID == 0 || req.StartRID > 9999 {
		return nil, fmt.Errorf("invalid_start_rid")
	}
	if req.Count <= 0 {
		return nil, fmt.Errorf("count_must_be_positive")
	}
	if req.Count > 200 {
		return nil, fmt.Errorf("count_too_large")
	}
	if uint(req.Count-1) > 9999-req.StartRID {
		return nil, fmt.Errorf("invalid_rid_range")
	}

	namePrefix := strings.TrimSpace(req.NamePrefix)
	if namePrefix == "" {
		candidate := strings.TrimSpace(template.SourceVMName)
		if candidate != "" && utils.IsValidVMName(candidate) {
			namePrefix = candidate
		} else {
			namePrefix = "vm"
		}
	}
	if !utils.IsValidVMName(namePrefix) {
		return nil, fmt.Errorf("invalid_name_prefix")
	}

	targets := make([]vmTemplateCreateTarget, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		rid := req.StartRID + uint(i)
		if rid == 0 || rid > 9999 {
			return nil, fmt.Errorf("invalid_rid_range")
		}
		targets = append(targets, vmTemplateCreateTarget{
			RID:  rid,
			Name: fmt.Sprintf("%s-%d", namePrefix, rid),
		})
	}

	return targets, nil
}

func (s *Service) resolveVMTemplateStoragePools(
	ctx context.Context,
	template vmModels.VMTemplate,
	assignments []libvirtServiceInterfaces.VMTemplateStoragePoolAssignment,
) (map[uint]string, error) {
	if len(template.Storages) == 0 {
		return nil, fmt.Errorf("template_has_no_cloneable_storage")
	}

	usablePools, err := s.getUsablePoolSet(ctx)
	if err != nil {
		return nil, err
	}

	templateStorageByID := make(map[uint]vmModels.VMTemplateStorage, len(template.Storages))
	poolByStorageID := make(map[uint]string, len(template.Storages))

	for _, storage := range template.Storages {
		templateStorageByID[storage.SourceStorageID] = storage

		pool := strings.TrimSpace(storage.Pool)
		if pool == "" {
			return nil, fmt.Errorf("storage_pool_required")
		}
		if _, ok := usablePools[pool]; !ok {
			return nil, fmt.Errorf("pool_not_found")
		}
		poolByStorageID[storage.SourceStorageID] = pool
	}

	seenAssignments := make(map[uint]struct{}, len(assignments))
	for _, assignment := range assignments {
		if assignment.SourceStorageID == 0 {
			return nil, fmt.Errorf("invalid_storage_mapping_source")
		}
		if _, exists := templateStorageByID[assignment.SourceStorageID]; !exists {
			return nil, fmt.Errorf("invalid_storage_mapping_source")
		}
		if _, duplicate := seenAssignments[assignment.SourceStorageID]; duplicate {
			return nil, fmt.Errorf("duplicate_storage_mapping_source")
		}
		seenAssignments[assignment.SourceStorageID] = struct{}{}

		pool := strings.TrimSpace(assignment.Pool)
		if pool == "" {
			return nil, fmt.Errorf("storage_pool_required")
		}
		if _, ok := usablePools[pool]; !ok {
			return nil, fmt.Errorf("pool_not_found")
		}
		poolByStorageID[assignment.SourceStorageID] = pool
	}

	return poolByStorageID, nil
}

func (s *Service) preflightVMTemplateTargets(targets []vmTemplateCreateTarget) error {
	if len(targets) == 0 {
		return fmt.Errorf("no_targets")
	}

	rids := make([]uint, 0, len(targets))
	names := make([]string, 0, len(targets))
	seenRID := make(map[uint]struct{}, len(targets))
	seenNames := make(map[string]struct{}, len(targets))

	for _, target := range targets {
		if target.RID == 0 || target.RID > 9999 {
			return fmt.Errorf("invalid_rid")
		}
		if _, exists := seenRID[target.RID]; exists {
			return fmt.Errorf("duplicate_rids_requested")
		}
		seenRID[target.RID] = struct{}{}

		name := strings.TrimSpace(target.Name)
		if name == "" || !utils.IsValidVMName(name) {
			return fmt.Errorf("invalid_vm_name")
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("duplicate_vm_names_requested")
		}
		seenNames[name] = struct{}{}

		rids = append(rids, target.RID)
		names = append(names, name)
	}

	var count int64
	if err := s.DB.Model(&vmModels.VM{}).Where("rid IN ?", rids).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_vm_rids: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("rid_range_contains_used_values")
	}

	if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id IN ?", rids).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_jail_ctids: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("rid_range_contains_used_values")
	}

	if err := s.DB.Model(&vmModels.VM{}).Where("name IN ?", names).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_vm_names: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("vm_name_already_in_use")
	}

	clusterEnabled, err := s.isClusterEnabled()
	if err != nil {
		return err
	}
	if !clusterEnabled {
		return nil
	}

	var nodes []clusterModels.ClusterNode
	if err := s.DB.Select("guest_ids").Find(&nodes).Error; err != nil {
		return fmt.Errorf("failed_to_check_cluster_guest_ids: %w", err)
	}

	usedGuestIDs := make(map[uint]struct{})
	for _, node := range nodes {
		for _, id := range node.GuestIDs {
			usedGuestIDs[id] = struct{}{}
		}
	}

	for _, rid := range rids {
		if _, exists := usedGuestIDs[rid]; exists {
			return fmt.Errorf("rid_range_contains_used_values")
		}
	}

	return nil
}

func (s *Service) preflightVMTemplateResources(
	ctx context.Context,
	template vmModels.VMTemplate,
	targets []vmTemplateCreateTarget,
	poolByStorageID map[uint]string,
) error {
	if len(template.Storages) == 0 {
		return fmt.Errorf("template_has_no_cloneable_storage")
	}

	requiredByPool := make(map[string]uint64)
	targetRootDatasets := make(map[string]struct{})

	for _, storage := range template.Storages {
		if storage.Type != vmModels.VMStorageTypeRaw && storage.Type != vmModels.VMStorageTypeZVol {
			continue
		}
		sourceID := storage.SourceStorageID
		pool := strings.TrimSpace(poolByStorageID[sourceID])
		if pool == "" {
			return fmt.Errorf("storage_pool_required")
		}
		templateDataset := strings.TrimSpace(storage.TemplateDataset)
		if templateDataset == "" {
			return fmt.Errorf("template_storage_dataset_missing")
		}

		ds, err := s.GZFS.ZFS.Get(ctx, templateDataset, false)
		if err != nil {
			return fmt.Errorf("failed_to_get_template_storage_dataset: %w", err)
		}
		if ds == nil {
			return fmt.Errorf("template_storage_dataset_not_found")
		}

		perTarget := storage.EstimatedBytes
		if perTarget == 0 {
			perTarget = datasetEstimatedUsed(ds.Used, ds.Referenced)
		}

		for _, target := range targets {
			requiredByPool[pool] += perTarget

			rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, target.RID)
			targetRootDatasets[rootDataset] = struct{}{}
		}
	}

	for dataset := range targetRootDatasets {
		existing, err := s.GZFS.ZFS.Get(ctx, dataset, false)
		if err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "dataset does not exist") || strings.Contains(msg, "does not exist") {
				continue
			}
			return fmt.Errorf("failed_to_check_target_vm_dataset: %w", err)
		}
		if existing != nil {
			return fmt.Errorf("target_vm_dataset_already_exists")
		}
	}

	for pool, required := range requiredByPool {
		if err := s.checkPoolCapacity(ctx, pool, required); err != nil {
			return err
		}
	}

	for _, network := range template.Networks {
		switchName := strings.TrimSpace(network.SwitchName)
		if switchName == "" {
			continue
		}
		if _, err := s.resolveSwitchID(switchName, network.SwitchType); err != nil {
			if strings.Contains(err.Error(), "switch_not_found") {
				return fmt.Errorf("template_network_switch_not_found")
			}
			return err
		}
	}

	return nil
}

func (s *Service) preflightCreateVMsFromTemplate(
	ctx context.Context,
	templateID uint,
	req libvirtServiceInterfaces.CreateFromTemplateRequest,
) (vmTemplateCreatePlan, error) {
	plan := vmTemplateCreatePlan{}
	if templateID == 0 {
		return plan, fmt.Errorf("invalid_template_id")
	}

	var template vmModels.VMTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return plan, fmt.Errorf("template_not_found")
		}
		return plan, fmt.Errorf("failed_to_get_template: %w", err)
	}

	targets, err := s.buildVMTemplateTargets(template, req)
	if err != nil {
		return plan, err
	}

	storagePools, err := s.resolveVMTemplateStoragePools(ctx, template, req.StoragePools)
	if err != nil {
		return plan, err
	}

	if err := s.preflightVMTemplateTargets(targets); err != nil {
		return plan, err
	}

	if err := s.preflightVMTemplateResources(ctx, template, targets, storagePools); err != nil {
		return plan, err
	}

	plan.Template = template
	plan.Targets = targets
	plan.StoragePools = storagePools
	return plan, nil
}

func (s *Service) cloneStorageDatasetFromTemplate(
	ctx context.Context,
	target vmTemplateCreateTarget,
	clone vmTemplateStorageClone,
) error {
	templateDataset := strings.TrimSpace(clone.TemplateStorage.TemplateDataset)
	if templateDataset == "" {
		return fmt.Errorf("template_storage_dataset_missing")
	}

	sourceDS, err := s.GZFS.ZFS.Get(ctx, templateDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_template_storage_dataset: %w", err)
	}
	if sourceDS == nil {
		return fmt.Errorf("template_storage_dataset_not_found")
	}

	pool := strings.TrimSpace(clone.CreatedStorage.Pool)
	if pool == "" {
		return fmt.Errorf("storage_pool_required")
	}

	if err := s.CreateStorageParent(target.RID, pool, ctx); err != nil {
		return fmt.Errorf("failed_to_prepare_target_storage_parent: %w", err)
	}

	targetDataset, err := vmTargetStorageDatasetPath(pool, target.RID, clone.CreatedStorage.Type, clone.CreatedStorage.ID)
	if err != nil {
		return err
	}

	existing, err := s.GZFS.ZFS.Get(ctx, targetDataset, false)
	if err == nil && existing != nil {
		return fmt.Errorf("target_storage_dataset_already_exists")
	}

	snapshotName := fmt.Sprintf("sylve_vm_template_restore_%d_%d", target.RID, time.Now().UTC().UnixMilli())
	snapshot, err := sourceDS.Snapshot(ctx, snapshotName, true)
	if err != nil {
		return fmt.Errorf("failed_to_snapshot_template_storage_dataset: %w", err)
	}
	defer func() {
		_ = snapshot.Destroy(ctx, true, false)
	}()

	clonedDS, err := snapshot.SendToDataset(ctx, targetDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_clone_template_storage_dataset: %w", err)
	}

	if clone.CreatedStorage.Type == vmModels.VMStorageTypeRaw {
		oldPath := filepath.Join(clonedDS.Mountpoint, fmt.Sprintf("%d.img", clone.TemplateStorage.SourceStorageID))
		newPath := filepath.Join(clonedDS.Mountpoint, fmt.Sprintf("%d.img", clone.CreatedStorage.ID))

		if oldPath != newPath {
			if _, statErr := os.Stat(oldPath); statErr == nil {
				if err := os.Rename(oldPath, newPath); err != nil {
					_ = clonedDS.Destroy(ctx, true, false)
					return fmt.Errorf("failed_to_rename_cloned_raw_disk: %w", err)
				}
			}
		}
	}

	return nil
}

func (s *Service) cleanupTemplateCreatedVM(ctx context.Context, rid uint) {
	if rid == 0 {
		return
	}

	if err := s.RemoveLvVm(rid); err != nil {
		logger.L.Warn().Err(err).Uint("rid", rid).Msg("vm_template_cleanup_remove_lv_failed")
	}

	warnings := make([]string, 0)
	s.forceRemoveVMRuntimeArtifacts(rid, &warnings)
	s.forceRemoveVMZFSDatasets(ctx, rid, &warnings)
	s.forceRemoveVMDBRecords(rid, true, &warnings)

	if len(warnings) > 0 {
		logger.L.Warn().
			Uint("rid", rid).
			Strs("warnings", warnings).
			Msg("vm_template_cleanup_warnings")
	}
}

func (s *Service) createVMFromTemplateTarget(
	ctx context.Context,
	template vmModels.VMTemplate,
	target vmTemplateCreateTarget,
	poolByStorageID map[uint]string,
	req libvirtServiceInterfaces.CreateFromTemplateRequest,
) error {
	vncPort, err := s.getNextFreeVNCPort()
	if err != nil {
		return err
	}

	cloudInitData := template.CloudInitData
	cloudInitMetaData := template.CloudInitMetaData
	cloudInitNetworkConfig := template.CloudInitNetworkConfig

	if req.RewriteCloudInitIdentity && templateHasCloudInit(template) {
		rewrittenMeta, err := rewriteCloudInitMetadataIdentity(
			cloudInitMetaData,
			req.CloudInitPrefix,
			target.Name,
			target.RID,
		)
		if err != nil {
			return err
		}
		cloudInitMetaData = rewrittenMeta
	}

	vm := vmModels.VM{
		Name:                   target.Name,
		Description:            template.Description,
		RID:                    target.RID,
		CPUSockets:             template.CPUSockets,
		CPUCores:               template.CPUCores,
		CPUThreads:             template.CPUThreads,
		RAM:                    template.RAM,
		TPMEmulation:           template.TPMEmulation,
		ShutdownWaitTime:       template.ShutdownWaitTime,
		Serial:                 template.Serial,
		VNCEnabled:             template.VNCEnabled,
		VNCBind:                NormalizeVNCBindAddress(template.VNCBind),
		VNCPort:                vncPort,
		VNCPassword:            utils.GenerateRandomString(16),
		VNCResolution:          template.VNCResolution,
		VNCWait:                template.VNCWait,
		StartAtBoot:            false,
		StartOrder:             0,
		WoL:                    false,
		TimeOffset:             template.TimeOffset,
		PCIDevices:             []int{},
		APIC:                   template.APIC,
		ACPI:                   template.ACPI,
		CloudInitData:          cloudInitData,
		CloudInitMetaData:      cloudInitMetaData,
		CloudInitNetworkConfig: cloudInitNetworkConfig,
		IgnoreUMSR:             template.IgnoreUMSR,
		QemuGuestAgent:         template.QemuGuestAgent,
		CPUPinning:             []vmModels.VMCPUPinning{},
		Storages:               []vmModels.Storage{},
		Networks:               []vmModels.Network{},
	}

	networkSwitchIDs := make([]uint, len(template.Networks))
	for idx, network := range template.Networks {
		switchName := strings.TrimSpace(network.SwitchName)
		if switchName == "" {
			continue
		}

		switchID, err := s.resolveSwitchID(switchName, network.SwitchType)
		if err != nil {
			if strings.Contains(err.Error(), "switch_not_found") {
				return fmt.Errorf("template_network_switch_not_found")
			}
			return err
		}
		networkSwitchIDs[idx] = switchID
	}

	clonePlan := make([]vmTemplateStorageClone, 0, len(template.Storages))

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&vm).Error; err != nil {
			return fmt.Errorf("failed_to_create_vm_from_template: %w", err)
		}

		templateStorages := append([]vmModels.VMTemplateStorage{}, template.Storages...)
		slices.SortFunc(templateStorages, func(a, b vmModels.VMTemplateStorage) int {
			return a.BootOrder - b.BootOrder
		})

		for _, storage := range templateStorages {
			if storage.Type != vmModels.VMStorageTypeRaw && storage.Type != vmModels.VMStorageTypeZVol {
				continue
			}

			pool := strings.TrimSpace(poolByStorageID[storage.SourceStorageID])
			if pool == "" {
				return fmt.Errorf("storage_pool_required")
			}

			createdStorage := vmModels.Storage{
				VMID:         vm.ID,
				Type:         storage.Type,
				Pool:         pool,
				Size:         storage.Size,
				Emulation:    storage.Emulation,
				Enable:       storage.Enable,
				BootOrder:    storage.BootOrder,
				RecordSize:   storage.RecordSize,
				VolBlockSize: storage.VolBlockSize,
			}
			if err := tx.Create(&createdStorage).Error; err != nil {
				return fmt.Errorf("failed_to_create_vm_storage_from_template: %w", err)
			}
			vm.Storages = append(vm.Storages, createdStorage)
			clonePlan = append(clonePlan, vmTemplateStorageClone{
				TemplateStorage: storage,
				CreatedStorage:  createdStorage,
			})
		}

		for idx, network := range template.Networks {
			switchID := networkSwitchIDs[idx]
			if switchID == 0 {
				continue
			}

			macID, err := s.createUniqueMACObject(tx, fmt.Sprintf("%s-net-%d", target.Name, idx+1))
			if err != nil {
				return err
			}
			macIDCopy := macID

			createdNetwork := vmModels.Network{
				VMID:       vm.ID,
				MacID:      &macIDCopy,
				SwitchID:   switchID,
				SwitchType: strings.ToLower(strings.TrimSpace(network.SwitchType)),
				Emulation:  network.Emulation,
			}
			if err := tx.Create(&createdNetwork).Error; err != nil {
				return fmt.Errorf("failed_to_create_vm_network_from_template: %w", err)
			}
			vm.Networks = append(vm.Networks, createdNetwork)
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, clone := range clonePlan {
		if err := s.cloneStorageDatasetFromTemplate(ctx, target, clone); err != nil {
			s.cleanupTemplateCreatedVM(ctx, target.RID)
			return err
		}
	}

	if err := s.CreateLvVm(int(vm.ID), ctx); err != nil {
		s.cleanupTemplateCreatedVM(ctx, target.RID)
		return fmt.Errorf("failed_to_create_lv_vm_from_template: %w", err)
	}

	return nil
}

func (s *Service) GetVMTemplatesSimple() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
	var templates []vmModels.VMTemplate
	if err := s.DB.Model(&vmModels.VMTemplate{}).Order("id asc").Find(&templates).Error; err != nil {
		return nil, fmt.Errorf("failed_to_fetch_vm_templates: %w", err)
	}

	out := make([]libvirtServiceInterfaces.SimpleTemplateList, 0, len(templates))
	for _, template := range templates {
		out = append(out, libvirtServiceInterfaces.SimpleTemplateList{
			ID:           template.ID,
			Name:         template.Name,
			SourceVMName: template.SourceVMName,
		})
	}

	return out, nil
}

func (s *Service) GetVMTemplate(templateID uint) (*vmModels.VMTemplate, error) {
	if templateID == 0 {
		return nil, fmt.Errorf("invalid_template_id")
	}

	var template vmModels.VMTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("template_not_found")
		}
		return nil, fmt.Errorf("failed_to_get_template: %w", err)
	}

	return &template, nil
}

func (s *Service) PreflightConvertVMToTemplate(
	ctx context.Context,
	rid uint,
	req libvirtServiceInterfaces.ConvertToTemplateRequest,
) error {
	if rid == 0 {
		return fmt.Errorf("invalid_rid")
	}
	if err := s.ensureUniqueVMTemplateName(req.Name); err != nil {
		return err
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return err
	}

	off, err := s.IsDomainShutOff(rid)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !off {
		return fmt.Errorf("vm_must_be_shut_off")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("vm_not_found")
		}
		return fmt.Errorf("failed_to_get_vm: %w", err)
	}

	cloneable := make([]vmModels.Storage, 0)
	requiredByPool := make(map[string]uint64)

	for _, storage := range vm.Storages {
		if storage.Type != vmModels.VMStorageTypeRaw && storage.Type != vmModels.VMStorageTypeZVol {
			continue
		}

		sourceDataset, err := vmStorageSourceDatasetName(storage, vm.RID)
		if err != nil {
			return err
		}

		ds, err := s.GZFS.ZFS.Get(ctx, sourceDataset, false)
		if err != nil {
			return fmt.Errorf("failed_to_get_source_storage_dataset: %w", err)
		}
		if ds == nil {
			return fmt.Errorf("source_storage_dataset_not_found")
		}

		cloneable = append(cloneable, storage)
		requiredByPool[strings.TrimSpace(storage.Pool)] += datasetEstimatedUsed(ds.Used, ds.Referenced)
	}

	if len(cloneable) == 0 {
		return fmt.Errorf("no_cloneable_storage")
	}

	for pool, required := range requiredByPool {
		if pool == "" {
			return fmt.Errorf("storage_pool_required")
		}
		zpool, err := s.GZFS.Zpool.Get(ctx, pool)
		if err != nil {
			return fmt.Errorf("failed_to_get_pool: %w", err)
		}
		if zpool == nil {
			return fmt.Errorf("pool_not_found")
		}

		if required > zpool.Free {
			return fmt.Errorf("insufficient_pool_space")
		}
	}

	for _, network := range vm.Networks {
		if network.SwitchID == 0 {
			continue
		}
		if _, err := s.resolveSwitchName(network.SwitchID, network.SwitchType); err != nil {
			if strings.Contains(err.Error(), "switch_not_found") {
				return fmt.Errorf("template_network_switch_not_found")
			}
			return err
		}
	}

	return nil
}

func (s *Service) ConvertVMToTemplate(
	ctx context.Context,
	rid uint,
	req libvirtServiceInterfaces.ConvertToTemplateRequest,
) (retErr error) {
	if err := s.PreflightConvertVMToTemplate(ctx, rid, req); err != nil {
		return err
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm: %w", err)
	}

	templateNetworks := make([]vmModels.VMTemplateNetwork, 0, len(vm.Networks))
	for _, network := range vm.Networks {
		if network.SwitchID == 0 {
			continue
		}
		switchName, err := s.resolveSwitchName(network.SwitchID, network.SwitchType)
		if err != nil {
			if strings.Contains(err.Error(), "switch_not_found") {
				return fmt.Errorf("template_network_switch_not_found")
			}
			return err
		}

		templateNetworks = append(templateNetworks, vmModels.VMTemplateNetwork{
			Name:       fmt.Sprintf("net-%d", network.ID),
			SwitchName: switchName,
			SwitchType: strings.ToLower(strings.TrimSpace(network.SwitchType)),
			Emulation:  network.Emulation,
		})
	}

	template := vmModels.VMTemplate{
		Name:                   normalizeVMTemplateName(req.Name),
		SourceVMName:           vm.Name,
		SourceVMRID:            vm.RID,
		Description:            vm.Description,
		CPUSockets:             vm.CPUSockets,
		CPUCores:               vm.CPUCores,
		CPUThreads:             vm.CPUThreads,
		RAM:                    vm.RAM,
		TPMEmulation:           vm.TPMEmulation,
		ShutdownWaitTime:       vm.ShutdownWaitTime,
		Serial:                 vm.Serial,
		VNCEnabled:             vm.VNCEnabled,
		VNCBind:                NormalizeVNCBindAddress(vm.VNCBind),
		VNCResolution:          vm.VNCResolution,
		VNCWait:                vm.VNCWait,
		StartAtBoot:            vm.StartAtBoot,
		StartOrder:             vm.StartOrder,
		WoL:                    vm.WoL,
		TimeOffset:             vm.TimeOffset,
		APIC:                   vm.APIC,
		ACPI:                   vm.ACPI,
		CloudInitData:          vm.CloudInitData,
		CloudInitMetaData:      vm.CloudInitMetaData,
		CloudInitNetworkConfig: vm.CloudInitNetworkConfig,
		IgnoreUMSR:             vm.IgnoreUMSR,
		QemuGuestAgent:         vm.QemuGuestAgent,
		Storages:               []vmModels.VMTemplateStorage{},
		Networks:               templateNetworks,
	}

	if err := s.DB.Create(&template).Error; err != nil {
		return fmt.Errorf("failed_to_create_vm_template: %w", err)
	}

	createdDatasetNames := make([]string, 0)
	defer func() {
		if retErr == nil {
			return
		}

		for _, dataset := range createdDatasetNames {
			ds, err := s.GZFS.ZFS.Get(ctx, dataset, false)
			if err == nil && ds != nil {
				_ = ds.Destroy(ctx, true, false)
			}
		}
		_ = s.DB.Delete(&vmModels.VMTemplate{}, template.ID).Error
	}()

	templateStorages := make([]vmModels.VMTemplateStorage, 0)
	for _, storage := range vm.Storages {
		if storage.Type != vmModels.VMStorageTypeRaw && storage.Type != vmModels.VMStorageTypeZVol {
			continue
		}

		sourceDataset, err := vmStorageSourceDatasetName(storage, vm.RID)
		if err != nil {
			return err
		}
		sourceDS, err := s.GZFS.ZFS.Get(ctx, sourceDataset, false)
		if err != nil {
			return fmt.Errorf("failed_to_get_source_storage_dataset: %w", err)
		}
		if sourceDS == nil {
			return fmt.Errorf("source_storage_dataset_not_found")
		}

		templateDataset, err := vmTemplateStorageDatasetPath(storage.Pool, template.ID, storage.Type, storage.ID)
		if err != nil {
			return err
		}

		parentDataset := fmt.Sprintf("%s/sylve/virtual-machines/templates/%d", storage.Pool, template.ID)
		if err := s.ensureDatasetPath(ctx, parentDataset); err != nil {
			return fmt.Errorf("failed_to_prepare_template_parent_dataset: %w", err)
		}

		if existing, getErr := s.GZFS.ZFS.Get(ctx, templateDataset, false); getErr == nil && existing != nil {
			return fmt.Errorf("template_storage_dataset_already_exists")
		}

		snapshotName := fmt.Sprintf("sylve_vm_template_%d_%d", vm.RID, time.Now().UTC().UnixMilli())
		snapshot, err := sourceDS.Snapshot(ctx, snapshotName, true)
		if err != nil {
			return fmt.Errorf("failed_to_snapshot_source_storage_dataset: %w", err)
		}
		if _, err := snapshot.SendToDataset(ctx, templateDataset, false); err != nil {
			_ = snapshot.Destroy(ctx, true, false)
			return fmt.Errorf("failed_to_copy_storage_dataset_to_template: %w", err)
		}
		_ = snapshot.Destroy(ctx, true, false)
		createdDatasetNames = append(createdDatasetNames, templateDataset)

		templateStorages = append(templateStorages, vmModels.VMTemplateStorage{
			SourceStorageID: storage.ID,
			Type:            storage.Type,
			Emulation:       storage.Emulation,
			Pool:            storage.Pool,
			Size:            storage.Size,
			Enable:          storage.Enable,
			BootOrder:       storage.BootOrder,
			RecordSize:      storage.RecordSize,
			VolBlockSize:    storage.VolBlockSize,
			TemplateDataset: templateDataset,
			EstimatedBytes:  datasetEstimatedUsed(sourceDS.Used, sourceDS.Referenced),
		})
	}

	if len(templateStorages) == 0 {
		return fmt.Errorf("no_cloneable_storage")
	}

	if err := s.DB.Model(&template).
		Select("storages").
		Updates(vmModels.VMTemplate{Storages: templateStorages}).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_template_storages: %w", err)
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("vm_template_convert_%d", vm.RID))
	return nil
}

func (s *Service) PreflightCreateVMsFromTemplate(ctx context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error {
	_, err := s.preflightCreateVMsFromTemplate(ctx, templateID, req)
	return err
}

func (s *Service) CreateVMsFromTemplate(ctx context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error {
	preflightFn := s.preflightCreateVMsFromTemplate
	if s.preflightCreateVMTemplateFn != nil {
		preflightFn = s.preflightCreateVMTemplateFn
	}

	plan, err := preflightFn(ctx, templateID, req)
	if err != nil {
		return err
	}

	createTargetFn := s.createVMFromTemplateTarget
	if s.createVMTemplateTargetFn != nil {
		createTargetFn = s.createVMTemplateTargetFn
	}

	for _, target := range plan.Targets {
		if err := createTargetFn(ctx, plan.Template, target, plan.StoragePools, req); err != nil {
			return err
		}
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("vm_template_create_%d", templateID))
	return nil
}

func (s *Service) DeleteVMTemplate(ctx context.Context, templateID uint) error {
	if templateID == 0 {
		return fmt.Errorf("invalid_template_id")
	}

	var template vmModels.VMTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("template_not_found")
		}
		return fmt.Errorf("failed_to_get_template: %w", err)
	}

	for _, storage := range template.Storages {
		templateDataset := strings.TrimSpace(storage.TemplateDataset)
		if templateDataset == "" {
			continue
		}
		ds, err := s.GZFS.ZFS.Get(ctx, templateDataset, false)
		if err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "dataset does not exist") || strings.Contains(msg, "does not exist") {
				continue
			}
			return fmt.Errorf("failed_to_get_template_storage_dataset: %w", err)
		}
		if ds == nil {
			continue
		}
		if err := ds.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_delete_template_storage_dataset: %w", err)
		}
	}

	if err := s.DB.Delete(&template).Error; err != nil {
		return fmt.Errorf("failed_to_delete_vm_template: %w", err)
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("vm_template_delete_%d", templateID))
	return nil
}

func (s *Service) sourceVMStoragesForTemplate(vm vmModels.VM) []vmModels.Storage {
	out := make([]vmModels.Storage, 0, len(vm.Storages))
	for _, storage := range vm.Storages {
		if storage.Type == vmModels.VMStorageTypeRaw || storage.Type == vmModels.VMStorageTypeZVol {
			out = append(out, storage)
		}
	}
	slices.SortFunc(out, func(a, b vmModels.Storage) int {
		return a.BootOrder - b.BootOrder
	})
	return out
}
