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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type CreateFromTemplateRequest struct {
	Mode       string `json:"mode"`
	CTID       uint   `json:"ctid"`
	Name       string `json:"name"`
	StartCTID  uint   `json:"startCtid"`
	Count      int    `json:"count"`
	NamePrefix string `json:"namePrefix"`
	Pool       string `json:"pool"`
}

type ConvertToTemplateRequest struct {
	Name string `json:"name"`
}

type createTarget struct {
	CTID uint
	Name string
	Pool string
}

func jailTemplateCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
}

func isMissingJailTemplateDatasetError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "dataset does not exist") || strings.Contains(message, "does not exist")
}

func validateJailTemplateDatasetPath(pool, dataset string) error {
	pool = strings.TrimSpace(strings.Trim(pool, "/"))
	dataset = strings.TrimSpace(strings.Trim(dataset, "/"))
	if pool == "" || strings.Contains(pool, "/") || dataset == "" {
		return fmt.Errorf("invalid_template_dataset_path")
	}

	prefix := fmt.Sprintf("%s/sylve/jails/templates/", pool)
	if !strings.HasPrefix(dataset, prefix) || strings.TrimPrefix(dataset, prefix) == "" {
		return fmt.Errorf("invalid_template_dataset_path")
	}

	return nil
}

func (s *Service) destroyJailTemplateDatasetIfPresent(
	ctx context.Context,
	pool string,
	dataset string,
	recursive bool,
) error {
	if err := validateJailTemplateDatasetPath(pool, dataset); err != nil {
		return err
	}

	ds, err := s.GZFS.ZFS.Get(ctx, strings.TrimSpace(strings.Trim(dataset, "/")), false)
	if err != nil {
		if isMissingJailTemplateDatasetError(err) {
			return nil
		}
		return err
	}
	if ds == nil {
		return nil
	}
	return ds.Destroy(ctx, recursive, false)
}

func (s *Service) validateJailTemplateNetworks(jailType jailModels.JailType, networks []jailModels.JailTemplateNetwork) error {
	defaultGatewayCount := 0
	for _, network := range networks {
		if jailType == jailModels.JailTypeLinux && (network.DHCP || network.SLAAC) {
			return fmt.Errorf("cannot_set_dhcp_or_slaac_when_linux_jail")
		}
		if network.DefaultGateway {
			defaultGatewayCount++
			if defaultGatewayCount > 1 {
				return fmt.Errorf("jail_default_gateway_exists")
			}
		}
	}

	for _, network := range networks {
		if network.SwitchID == 0 {
			continue
		}
		if s.NetworkService == nil {
			return fmt.Errorf("template_network_service_unavailable")
		}

		bridge, err := s.NetworkService.GetBridgeNameByIDType(
			network.SwitchID,
			strings.ToLower(strings.TrimSpace(network.SwitchType)),
		)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				return fmt.Errorf("template_network_switch_not_found")
			}
			return fmt.Errorf("failed_to_validate_template_network_switch: %w", err)
		}
		if strings.TrimSpace(bridge) == "" {
			return fmt.Errorf("template_network_switch_not_found")
		}
	}

	return nil
}

func (s *Service) ensureNoActiveJailLifecycleTask(ctID uint) error {
	var count int64
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).
		Where("guest_type = ? AND guest_id = ? AND status IN ?", taskModels.GuestTypeJail, ctID, []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
		}).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_jail_lifecycle_tasks: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("jail_has_active_lifecycle_task")
	}
	return nil
}

func (s *Service) ensureNoActiveJailTemplateCreateTask(templateID uint) error {
	var count int64
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).
		Where(
			"guest_type = ? AND guest_id = ? AND action = ? AND status IN ?",
			taskModels.GuestTypeJailTemplate,
			templateID,
			"create",
			[]string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning},
		).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_jail_template_usage: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("jail_template_in_use")
	}
	return nil
}

func (s *Service) ensureFilesystemPath(ctx context.Context, dataset string) error {
	dataset = strings.TrimSpace(strings.Trim(dataset, "/"))
	if dataset == "" {
		return fmt.Errorf("dataset_required")
	}

	parts := strings.Split(dataset, "/")
	if len(parts) < 1 {
		return fmt.Errorf("dataset_required")
	}

	current := strings.TrimSpace(parts[0])
	if current == "" {
		return fmt.Errorf("dataset_pool_required")
	}

	for idx := 1; idx < len(parts); idx++ {
		current = current + "/" + strings.TrimSpace(parts[idx])
		if current == "" {
			continue
		}

		ds, err := s.GZFS.ZFS.Get(ctx, current, false)
		if err == nil && ds != nil {
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

func (s *Service) GetJailTemplatesSimple() ([]jailServiceInterfaces.SimpleTemplateList, error) {
	var templates []jailModels.JailTemplate
	if err := s.DB.Model(&jailModels.JailTemplate{}).Order("id asc").Find(&templates).Error; err != nil {
		return nil, fmt.Errorf("failed_to_fetch_jail_templates: %w", err)
	}

	out := make([]jailServiceInterfaces.SimpleTemplateList, 0, len(templates))
	for _, t := range templates {
		out = append(out, jailServiceInterfaces.SimpleTemplateList{
			ID:             t.ID,
			Name:           t.Name,
			SourceJailName: t.SourceJailName,
		})
	}

	return out, nil
}

func (s *Service) GetJailTemplate(templateID uint) (*jailModels.JailTemplate, error) {
	if templateID == 0 {
		return nil, fmt.Errorf("invalid_template_id")
	}

	var template jailModels.JailTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("template_not_found")
		}
		return nil, fmt.Errorf("failed_to_get_template: %w", err)
	}

	return &template, nil
}

func datasetEstimatedUsed(used, referenced uint64) uint64 {
	if used > 0 {
		return used
	}
	return referenced
}

func (s *Service) checkPoolCapacity(ctx context.Context, pool string, requiredBytes uint64) error {
	pool = strings.TrimSpace(pool)
	if pool == "" {
		return fmt.Errorf("pool_required")
	}

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

func (s *Service) validateCreateTargetPool(ctx context.Context, pool string) error {
	pool = strings.TrimSpace(pool)
	if pool == "" {
		return fmt.Errorf("pool_required")
	}

	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	for _, p := range pools {
		if p != nil && p.Name == pool {
			return nil
		}
	}

	return fmt.Errorf("pool_not_found")
}

func (s *Service) buildTemplateNetworks(networks []jailModels.Network) []jailModels.JailTemplateNetwork {
	out := make([]jailModels.JailTemplateNetwork, 0, len(networks))
	for _, n := range networks {
		if n.SwitchID == 0 {
			continue
		}
		out = append(out, jailModels.JailTemplateNetwork{
			Name:           n.Name,
			SwitchID:       n.SwitchID,
			SwitchType:     n.SwitchType,
			DHCP:           n.DHCP,
			SLAAC:          n.SLAAC,
			DefaultGateway: n.DefaultGateway,
		})
	}
	return out
}

func (s *Service) buildTemplateHooks(hooks []jailModels.JailHooks) []jailModels.JailTemplateHook {
	out := make([]jailModels.JailTemplateHook, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, jailModels.JailTemplateHook{Phase: h.Phase, Enabled: h.Enabled, Script: h.Script})
	}
	return out
}

func (s *Service) selectJailTemplateCPUSet(template jailModels.JailTemplate, ctID uint) ([]int, error) {
	if template.Cores <= 0 || (template.ResourceLimits != nil && !*template.ResourceLimits) {
		return []int{}, nil
	}

	logicalCores := s.jailHardwareOps().HostLogicalCores()
	if logicalCores < 1 {
		return nil, fmt.Errorf("host_cpu_unavailable")
	}
	return s.selectJailHardwareCPUSet(ctID, template.Cores, logicalCores)
}

func normalizeTemplateName(name string) string {
	return strings.TrimSpace(name)
}

func sanitizeTemplateDatasetToken(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "template"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range name {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}

		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}

	token := strings.Trim(b.String(), "-")
	if token == "" {
		token = "template"
	}
	return token
}

func (s *Service) ensureUniqueJailTemplateName(name string) error {
	normalized := normalizeTemplateName(name)
	if normalized == "" {
		return fmt.Errorf("template_name_required")
	}
	if len(normalized) > 120 {
		return fmt.Errorf("template_name_too_long")
	}

	var count int64
	if err := s.DB.Model(&jailModels.JailTemplate{}).
		Where("LOWER(name) = ?", strings.ToLower(normalized)).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_template_name_uniqueness: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("template_name_already_in_use")
	}

	return nil
}

func (s *Service) PreflightConvertJailToTemplate(ctx context.Context, ctID uint, req ConvertToTemplateRequest) error {
	if ctID == 0 {
		return fmt.Errorf("invalid_ct_id")
	}

	jail, err := s.GetJailByCTID(ctID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("jail_not_found")
		}
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}
	if jail == nil {
		return fmt.Errorf("jail_not_found")
	}

	if err := s.ensureUniqueJailTemplateName(req.Name); err != nil {
		return err
	}

	if replicationguard.PolicySchemaReady(s.DB) || replicationguard.GuestOperationSchemaReady(s.DB) {
		allowed, leaseErr := s.canMutateProtectedJail(ctID)
		if leaseErr != nil {
			return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
		}
		if !allowed {
			return fmt.Errorf("replication_lease_not_owned")
		}
	}

	state, err := s.GetStateByCtId(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_state: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(state.State), "INACTIVE") {
		return fmt.Errorf("jail_must_be_stopped")
	}

	if err := s.ensureNoActiveJailLifecycleTask(ctID); err != nil {
		return err
	}

	templateNetworks := s.buildTemplateNetworks(jail.Networks)
	if err := s.validateJailTemplateNetworks(jail.Type, templateNetworks); err != nil {
		return err
	}

	pool := ""
	for _, st := range jail.Storages {
		if st.IsBase {
			pool = st.Pool
			break
		}
	}
	if pool == "" {
		return fmt.Errorf("jail_base_pool_not_found")
	}

	sourceDataset := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	srcDS, err := s.GZFS.ZFS.Get(ctx, sourceDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_source_jail_dataset: %w", err)
	}
	if srcDS == nil {
		return fmt.Errorf("source_jail_dataset_not_found")
	}

	return s.checkPoolCapacity(ctx, pool, datasetEstimatedUsed(srcDS.Used, srcDS.Referenced))
}

func (s *Service) ConvertJailToTemplate(ctx context.Context, ctID uint, req ConvertToTemplateRequest) (retErr error) {
	if ctID == 0 {
		return fmt.Errorf("invalid_ct_id")
	}

	if err := s.PreflightConvertJailToTemplate(ctx, ctID, req); err != nil {
		return err
	}

	jail, err := s.GetJailByCTID(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}

	pool := ""
	for _, st := range jail.Storages {
		if st.IsBase {
			pool = st.Pool
			break
		}
	}
	if pool == "" {
		return fmt.Errorf("jail_base_pool_not_found")
	}

	sourceDataset := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	templateParentDataset := fmt.Sprintf("%s/sylve/jails/templates", pool)
	templateToken := sanitizeTemplateDatasetToken(req.Name)
	templateDataset := fmt.Sprintf(
		"%s/%s-%d",
		templateParentDataset,
		templateToken,
		time.Now().UTC().UnixMilli(),
	)
	if err := validateJailTemplateDatasetPath(pool, templateDataset); err != nil {
		return err
	}

	srcDS, err := s.GZFS.ZFS.Get(ctx, sourceDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_source_jail_dataset: %w", err)
	}
	if srcDS == nil {
		return fmt.Errorf("source_jail_dataset_not_found")
	}

	if err := s.ensureFilesystemPath(ctx, templateParentDataset); err != nil {
		return fmt.Errorf("failed_to_prepare_template_parent_dataset: %w", err)
	}

	templateDatasetCreated := false
	templateRowCreated := false
	var template jailModels.JailTemplate
	defer func() {
		if retErr == nil || !templateDatasetCreated {
			return
		}

		cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
		defer cancel()

		if err := s.destroyJailTemplateDatasetIfPresent(cleanupCtx, pool, templateDataset, true); err != nil {
			logger.L.Warn().Err(err).Str("dataset", templateDataset).Msg("jail_template_capture_dataset_cleanup_failed")
			retErr = errors.Join(retErr, fmt.Errorf("failed_to_cleanup_jail_template_dataset: %w", err))
			return
		}
		if templateRowCreated {
			if err := s.DB.WithContext(cleanupCtx).Delete(&jailModels.JailTemplate{}, template.ID).Error; err != nil {
				logger.L.Warn().Err(err).Uint("template_id", template.ID).Msg("jail_template_capture_row_cleanup_failed")
				retErr = errors.Join(retErr, fmt.Errorf("failed_to_cleanup_jail_template_record: %w", err))
			}
		}
	}()

	snapshotName := fmt.Sprintf("sylve_template_%d_%d", ctID, time.Now().UTC().UnixMilli())
	s.actionMutex.Lock()
	state, stateErr := s.GetStateByCtId(ctID)
	if stateErr != nil {
		s.actionMutex.Unlock()
		return fmt.Errorf("failed_to_get_jail_state: %w", stateErr)
	}
	if !strings.EqualFold(strings.TrimSpace(state.State), "INACTIVE") {
		s.actionMutex.Unlock()
		return fmt.Errorf("jail_must_be_stopped")
	}
	if taskErr := s.ensureNoActiveJailLifecycleTask(ctID); taskErr != nil {
		s.actionMutex.Unlock()
		return taskErr
	}
	snapshot, err := srcDS.Snapshot(ctx, snapshotName, true)
	s.actionMutex.Unlock()
	if err != nil {
		return fmt.Errorf("failed_to_create_template_snapshot: %w", err)
	}
	snapshotNeedsCleanup := true
	defer func() {
		if !snapshotNeedsCleanup {
			return
		}
		cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
		defer cancel()
		if err := snapshot.Destroy(cleanupCtx, true, false); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed_to_delete_temporary_template_snapshot: %w", err))
		}
	}()

	templateDatasetCreated = true
	if _, err := snapshot.SendToDataset(ctx, templateDataset, false); err != nil {
		return fmt.Errorf("failed_to_copy_jail_dataset_to_template: %w", err)
	}
	cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
	if err := snapshot.Destroy(cleanupCtx, true, false); err != nil {
		cancel()
		return fmt.Errorf("failed_to_delete_temporary_template_snapshot: %w", err)
	}
	cancel()
	snapshotNeedsCleanup = false

	templateName := normalizeTemplateName(req.Name)

	template = jailModels.JailTemplate{
		Name:              templateName,
		SourceJailName:    jail.Name,
		SourceJailCTID:    jail.CTID,
		Pool:              pool,
		RootDataset:       templateDataset,
		Type:              jail.Type,
		WoL:               jail.WoL,
		ResourceLimits:    jail.ResourceLimits,
		Cores:             jail.Cores,
		Memory:            jail.Memory,
		InheritIPv4:       jail.InheritIPv4,
		InheritIPv6:       jail.InheritIPv6,
		Fstab:             jail.Fstab,
		ResolvConf:        jail.ResolvConf,
		DevFSRuleset:      jail.DevFSRuleset,
		CleanEnvironment:  jail.CleanEnvironment,
		ExecTimeout:       jail.ExecTimeout,
		AdditionalOptions: jail.AdditionalOptions,
		AllowedOptions:    append([]string{}, jail.AllowedOptions...),
		MetadataMeta:      jail.MetadataMeta,
		MetadataEnv:       jail.MetadataEnv,
		Networks:          s.buildTemplateNetworks(jail.Networks),
		Hooks:             s.buildTemplateHooks(jail.JailHooks),
	}
	if template.ExecTimeout == 0 {
		template.ExecTimeout = jailModels.DefaultExecTimeoutSeconds
	}

	if err := s.DB.Create(&template).Error; err != nil {
		return fmt.Errorf("failed_to_create_jail_template: %w", err)
	}
	templateRowCreated = true

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_template_convert_%d", ctID))
	return nil
}

func (s *Service) buildCreateTargets(ctx context.Context, template jailModels.JailTemplate, req CreateFromTemplateRequest) ([]createTarget, error) {
	targetPool := strings.TrimSpace(req.Pool)
	if targetPool == "" {
		targetPool = strings.TrimSpace(template.Pool)
	}
	if err := s.validateCreateTargetPool(ctx, targetPool); err != nil {
		return nil, err
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "single"
	}

	if mode == "single" {
		if req.CTID == 0 {
			return nil, fmt.Errorf("ctid_required")
		}
		if req.CTID > 9999 {
			return nil, fmt.Errorf("invalid_ctid")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = strings.TrimSpace(template.SourceJailName)
			if name == "" {
				name = fmt.Sprintf("jail-%d", req.CTID)
			}
		}
		if !utils.IsValidVMName(name) {
			return nil, fmt.Errorf("invalid_jail_name")
		}
		return []createTarget{{
			CTID: req.CTID,
			Name: name,
			Pool: targetPool,
		}}, nil
	}

	if mode != "multiple" {
		return nil, fmt.Errorf("invalid_mode")
	}

	if req.StartCTID == 0 {
		return nil, fmt.Errorf("start_ctid_required")
	}
	if req.Count <= 0 {
		return nil, fmt.Errorf("count_must_be_positive")
	}
	if req.Count > 200 {
		return nil, fmt.Errorf("count_too_large")
	}
	if req.StartCTID > 9999 || uint(req.Count-1) > 9999-req.StartCTID {
		return nil, fmt.Errorf("invalid_ctid_range")
	}

	namePrefix := strings.TrimSpace(req.NamePrefix)
	if namePrefix == "" {
		candidate := strings.TrimSpace(template.SourceJailName)
		if len(candidate) > 0 && len(candidate) <= 15 && utils.IsValidVMName(candidate) {
			namePrefix = candidate
		} else {
			namePrefix = "jail"
		}
	} else if len(namePrefix) > 15 || !utils.IsValidVMName(namePrefix) {
		return nil, fmt.Errorf("invalid_name_prefix")
	}

	targets := make([]createTarget, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		ctid := req.StartCTID + uint(i)
		if ctid == 0 || ctid > 9999 {
			return nil, fmt.Errorf("invalid_ctid_range")
		}
		targets = append(targets, createTarget{
			CTID: ctid,
			Name: fmt.Sprintf("%s-%d", namePrefix, ctid),
			Pool: targetPool,
		})
	}

	return targets, nil
}

func (s *Service) preflightTemplateTargets(ctx context.Context, template jailModels.JailTemplate, targets []createTarget) error {
	if len(targets) == 0 {
		return fmt.Errorf("no_targets")
	}
	if err := s.validateJailTemplateNetworks(template.Type, template.Networks); err != nil {
		return err
	}

	ctids := make([]uint, 0, len(targets))
	names := make([]string, 0, len(targets))
	seenCTIDs := make(map[uint]struct{}, len(targets))
	seenNames := make(map[string]struct{}, len(targets))

	for _, t := range targets {
		if _, exists := seenCTIDs[t.CTID]; exists {
			return fmt.Errorf("duplicate_ctids_requested")
		}
		seenCTIDs[t.CTID] = struct{}{}

		name := strings.TrimSpace(t.Name)
		if name == "" || !utils.IsValidVMName(name) {
			return fmt.Errorf("invalid_jail_name")
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("duplicate_jail_names_requested")
		}
		seenNames[name] = struct{}{}

		ctids = append(ctids, t.CTID)
		names = append(names, name)
	}
	if replicationguard.GuestOperationSchemaReady(s.DB) {
		for _, ctID := range ctids {
			allowed, leaseErr := s.canMutateProtectedJail(ctID)
			if leaseErr != nil {
				return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
			}
			if !allowed {
				return fmt.Errorf("replication_lease_not_owned")
			}
		}
	}

	var existingCount int64
	if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id IN ?", ctids).Count(&existingCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_ctids: %w", err)
	}
	if existingCount > 0 {
		return fmt.Errorf("ctid_range_contains_used_values")
	}

	if err := s.DB.Model(&vmModels.VM{}).Where("rid IN ?", ctids).Count(&existingCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_vm_ids: %w", err)
	}
	if existingCount > 0 {
		return fmt.Errorf("ctid_range_contains_used_values")
	}

	if s.guestIdentityChecker != nil {
		if err := s.guestIdentityChecker.RequireGuestIDsAvailable(ctx, ctids); err != nil {
			return err
		}
	}

	if err := s.DB.Model(&jailModels.Jail{}).Where("name IN ?", names).Count(&existingCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_names: %w", err)
	}
	if existingCount > 0 {
		return fmt.Errorf("jail_name_already_in_use")
	}

	templateDS, err := s.GZFS.ZFS.Get(ctx, template.RootDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_template_dataset: %w", err)
	}
	if templateDS == nil {
		return fmt.Errorf("template_dataset_not_found")
	}

	perTargetBytes := datasetEstimatedUsed(templateDS.Used, templateDS.Referenced)
	requiredByPool := make(map[string]uint64)

	for _, target := range targets {
		datasetName := fmt.Sprintf("%s/sylve/jails/%d", target.Pool, target.CTID)
		if existing, getErr := s.GZFS.ZFS.Get(ctx, datasetName, false); getErr != nil {
			if !strings.Contains(strings.ToLower(getErr.Error()), "does not exist") {
				return fmt.Errorf("failed_to_check_target_dataset: %w", getErr)
			}
		} else if existing != nil {
			return fmt.Errorf("target_dataset_already_exists")
		}

		requiredByPool[target.Pool] += perTargetBytes
	}

	for pool, required := range requiredByPool {
		if err := s.checkPoolCapacity(ctx, pool, required); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) allocateMACObject(tx *gorm.DB, baseName string) (uint, string, error) {
	name := strings.TrimSpace(baseName)
	if name == "" {
		name = "jail-template-mac"
	}

	resolved := name
	for i := 0; ; i++ {
		if i > 0 {
			resolved = fmt.Sprintf("%s-%d", name, i)
		}
		var exists int64
		if err := tx.Model(&networkModels.Object{}).Where("name = ?", resolved).Count(&exists).Error; err != nil {
			return 0, "", fmt.Errorf("failed_to_check_mac_name: %w", err)
		}
		if exists == 0 {
			break
		}
	}

	macAddress := utils.GenerateRandomMAC()
	obj := networkModels.Object{Type: "Mac", Name: resolved}
	if err := tx.Create(&obj).Error; err != nil {
		return 0, "", fmt.Errorf("failed_to_create_mac_object: %w", err)
	}

	entry := networkModels.ObjectEntry{ObjectID: obj.ID, Value: macAddress}
	if err := tx.Create(&entry).Error; err != nil {
		return 0, "", fmt.Errorf("failed_to_create_mac_entry: %w", err)
	}

	return obj.ID, macAddress, nil
}

func (s *Service) createJailFromTemplateTarget(
	ctx context.Context,
	template jailModels.JailTemplate,
	target createTarget,
) (retErr error) {
	templateDS, err := s.GZFS.ZFS.Get(ctx, template.RootDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_template_dataset: %w", err)
	}
	if templateDS == nil {
		return fmt.Errorf("template_dataset_not_found")
	}

	datasetName := fmt.Sprintf("%s/sylve/jails/%d", target.Pool, target.CTID)

	if existing, getErr := s.GZFS.ZFS.Get(ctx, datasetName, false); getErr != nil {
		if !strings.Contains(strings.ToLower(getErr.Error()), "does not exist") {
			return fmt.Errorf("failed_to_check_target_dataset: %w", getErr)
		}
	} else if existing != nil {
		return fmt.Errorf("target_dataset_already_exists")
	}

	snapshotName := fmt.Sprintf("sylve_template_restore_%d_%d", target.CTID, time.Now().UTC().UnixMilli())
	snapshot, err := templateDS.Snapshot(ctx, snapshotName, true)
	if err != nil {
		return fmt.Errorf("failed_to_snapshot_template_dataset: %w", err)
	}

	createdDS, err := snapshot.SendToDataset(ctx, datasetName, false)
	if err != nil {
		resultErr := fmt.Errorf("failed_to_clone_template_dataset: %w", err)
		cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
		defer cancel()
		if cleanupErr := snapshot.Destroy(cleanupCtx, true, false); cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("failed_to_delete_temporary_template_snapshot: %w", cleanupErr))
		}
		partialDS, getErr := s.GZFS.ZFS.Get(cleanupCtx, datasetName, false)
		switch {
		case getErr != nil && !isMissingJailTemplateDatasetError(getErr):
			resultErr = errors.Join(resultErr, fmt.Errorf("failed_to_check_partial_jail_dataset: %w", getErr))
		case getErr == nil && partialDS != nil:
			if cleanupErr := partialDS.Destroy(cleanupCtx, true, false); cleanupErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("failed_to_cleanup_partial_jail_dataset: %w", cleanupErr))
			}
		}
		return resultErr
	}

	var createdJail jailModels.Jail
	cleanupCreatedJail := false

	defer func() {
		if retErr == nil {
			return
		}

		cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
		defer cancel()

		if cleanupCreatedJail {
			if err := s.DeleteJail(cleanupCtx, target.CTID, true, true); err != nil {
				logger.L.Warn().Err(err).Uint("ctid", target.CTID).Msg("jail_template_target_cleanup_failed")
				retErr = errors.Join(retErr, fmt.Errorf("failed_to_cleanup_template_created_jail: %w", err))
			}
			return
		}

		if createdDS != nil {
			if err := createdDS.Destroy(cleanupCtx, true, false); err != nil {
				logger.L.Warn().Err(err).Str("dataset", datasetName).Msg("jail_template_target_dataset_cleanup_failed")
				retErr = errors.Join(retErr, fmt.Errorf("failed_to_cleanup_template_created_dataset: %w", err))
			}
		}
	}()
	mountPoint, err := validateFilesystemDatasetMountpoint(createdDS, datasetName, "")
	if err != nil {
		return fmt.Errorf("jail_dataset_mountpoint_not_usable: %w", err)
	}
	snapshotNeedsCleanup := true
	defer func() {
		if !snapshotNeedsCleanup {
			return
		}
		cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
		defer cancel()
		if err := snapshot.Destroy(cleanupCtx, true, false); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed_to_delete_temporary_template_snapshot: %w", err))
		}
	}()
	cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
	if err := snapshot.Destroy(cleanupCtx, true, false); err != nil {
		cancel()
		return fmt.Errorf("failed_to_delete_temporary_template_snapshot: %w", err)
	}
	cancel()
	snapshotNeedsCleanup = false

	cpuSet, err := s.selectJailTemplateCPUSet(template, target.CTID)
	if err != nil {
		return fmt.Errorf("failed_to_select_jail_template_cpu_set: %w", err)
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		createdJail = jailModels.Jail{
			Name:              target.Name,
			CTID:              target.CTID,
			Type:              template.Type,
			Description:       "",
			StartAtBoot:       nil,
			StartOrder:        0,
			WoL:               template.WoL,
			InheritIPv4:       template.InheritIPv4,
			InheritIPv6:       template.InheritIPv6,
			ResourceLimits:    template.ResourceLimits,
			Cores:             template.Cores,
			CPUSet:            append([]int{}, cpuSet...),
			Memory:            template.Memory,
			DevFSRuleset:      template.DevFSRuleset,
			Fstab:             template.Fstab,
			ResolvConf:        template.ResolvConf,
			CleanEnvironment:  template.CleanEnvironment,
			ExecTimeout:       template.ExecTimeout,
			AdditionalOptions: template.AdditionalOptions,
			AllowedOptions:    append([]string{}, template.AllowedOptions...),
			MetadataMeta:      template.MetadataMeta,
			MetadataEnv:       template.MetadataEnv,
		}
		if createdJail.ExecTimeout == 0 {
			createdJail.ExecTimeout = jailModels.DefaultExecTimeoutSeconds
		}
		if createdJail.ResourceLimits != nil && !*createdJail.ResourceLimits {
			createdJail.Cores = 0
			createdJail.Memory = 0
		}

		if err := tx.Create(&createdJail).Error; err != nil {
			return fmt.Errorf("failed_to_create_jail_from_template: %w", err)
		}

		storage := jailModels.Storage{
			JailID: createdJail.ID,
			Pool:   target.Pool,
			GUID:   createdDS.GUID,
			Name:   "Base Filesystem",
			IsBase: true,
		}
		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_template_storage: %w", err)
		}

		for _, h := range template.Hooks {
			hook := jailModels.JailHooks{
				JailID:  createdJail.ID,
				Phase:   h.Phase,
				Enabled: h.Enabled,
				Script:  h.Script,
			}
			if err := tx.Create(&hook).Error; err != nil {
				return fmt.Errorf("failed_to_create_template_hook: %w", err)
			}
		}

		for idx, n := range template.Networks {
			macID, _, err := s.allocateMACObject(tx, fmt.Sprintf("%s-net-%d", target.Name, idx+1))
			if err != nil {
				return err
			}
			macIDCopy := macID

			network := jailModels.Network{
				JailID:         createdJail.ID,
				Name:           fmt.Sprintf("%s-net-%d", target.Name, idx+1),
				SwitchID:       n.SwitchID,
				SwitchType:     n.SwitchType,
				MacID:          &macIDCopy,
				IPv4ID:         nil,
				IPv4GwID:       nil,
				IPv6ID:         nil,
				IPv6GwID:       nil,
				DHCP:           n.DHCP,
				SLAAC:          n.SLAAC,
				DefaultGateway: n.DefaultGateway,
			}
			if err := tx.Create(&network).Error; err != nil {
				return fmt.Errorf("failed_to_create_template_network: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}
	cleanupCreatedJail = true

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", target.CTID))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_directory: %w", err)
	}

	logsPath := filepath.Join(jailDir, fmt.Sprintf("%d.log", target.CTID))
	if err := os.WriteFile(logsPath, []byte(""), 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_logs_file: %w", err)
	}

	fstabPath := filepath.Join(jailDir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(createdJail.Fstab), 0644); err != nil {
		return fmt.Errorf("failed_to_write_template_fstab: %w", err)
	}

	if strings.TrimSpace(createdJail.ResolvConf) != "" {
		resolvPath := filepath.Join(mountPoint, "etc", "resolv.conf")
		if err := os.MkdirAll(filepath.Dir(resolvPath), 0755); err != nil {
			return fmt.Errorf("failed_to_prepare_resolv_path: %w", err)
		}
		if err := os.WriteFile(resolvPath, []byte(createdJail.ResolvConf), 0644); err != nil {
			return fmt.Errorf("failed_to_write_template_resolv_conf: %w", err)
		}
	}

	reloaded, err := s.GetJailByCTID(target.CTID)
	if err != nil {
		return fmt.Errorf("failed_to_reload_created_jail: %w", err)
	}

	cfg, err := s.CreateJailConfig(*reloaded, mountPoint)
	if err != nil {
		return fmt.Errorf("failed_to_create_jail_config_from_template: %w", err)
	}

	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", target.CTID))
	if err := os.WriteFile(jailConfigPath, []byte(cfg), 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_config_from_template: %w", err)
	}

	sylveDir := filepath.Join(mountPoint, ".sylve")
	if err := os.MkdirAll(sylveDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_metadata_directory: %w", err)
	}

	if err := s.SyncNetwork(target.CTID, *reloaded); err != nil {
		return fmt.Errorf("failed_to_sync_template_jail_network: %w", err)
	}

	return nil
}

func (s *Service) preflightCreateJailsFromTemplate(ctx context.Context, templateID uint, req CreateFromTemplateRequest) (jailModels.JailTemplate, []createTarget, error) {
	var template jailModels.JailTemplate

	if templateID == 0 {
		return template, nil, fmt.Errorf("invalid_template_id")
	}

	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return template, nil, fmt.Errorf("template_not_found")
		}
		return template, nil, fmt.Errorf("failed_to_get_template: %w", err)
	}

	targets, err := s.buildCreateTargets(ctx, template, req)
	if err != nil {
		return template, nil, err
	}
	if err := s.preflightTemplateTargets(ctx, template, targets); err != nil {
		return template, nil, err
	}

	return template, targets, nil
}

func (s *Service) PreflightCreateJailsFromTemplate(ctx context.Context, templateID uint, req CreateFromTemplateRequest) error {
	_, _, err := s.preflightCreateJailsFromTemplate(ctx, templateID, req)
	return err
}

func runJailTemplateCreatePlan(
	ctx context.Context,
	targets []createTarget,
	createFn func(context.Context, createTarget) error,
	cleanupFn func(context.Context, createTarget) error,
) error {
	createdTargets := make([]createTarget, 0, len(targets))
	for _, target := range targets {
		if err := createFn(ctx, target); err != nil {
			resultErr := err
			cleanupCtx, cancel := jailTemplateCleanupContext(ctx)
			for idx := len(createdTargets) - 1; idx >= 0; idx-- {
				createdTarget := createdTargets[idx]
				if cleanupErr := cleanupFn(cleanupCtx, createdTarget); cleanupErr != nil {
					resultErr = errors.Join(
						resultErr,
						fmt.Errorf("failed_to_rollback_template_created_jail_%d: %w", createdTarget.CTID, cleanupErr),
					)
				}
			}
			cancel()
			return resultErr
		}
		createdTargets = append(createdTargets, target)
	}
	return nil
}

func (s *Service) CreateJailsFromTemplate(ctx context.Context, templateID uint, req CreateFromTemplateRequest) error {
	s.createMutex.Lock()
	defer s.createMutex.Unlock()

	template, targets, err := s.preflightCreateJailsFromTemplate(ctx, templateID, req)
	if err != nil {
		return err
	}

	if err := runJailTemplateCreatePlan(
		ctx,
		targets,
		func(createCtx context.Context, target createTarget) error {
			return s.createJailFromTemplateTarget(createCtx, template, target)
		},
		func(cleanupCtx context.Context, target createTarget) error {
			return s.DeleteJail(cleanupCtx, target.CTID, true, true)
		},
	); err != nil {
		return err
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_template_create_%d", templateID))
	return nil
}

func (s *Service) DeleteJailTemplate(ctx context.Context, templateID uint) error {
	if templateID == 0 {
		return fmt.Errorf("invalid_template_id")
	}

	s.createMutex.Lock()
	defer s.createMutex.Unlock()

	var template jailModels.JailTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("template_not_found")
		}
		return fmt.Errorf("failed_to_get_template: %w", err)
	}

	if err := s.ensureNoActiveJailTemplateCreateTask(templateID); err != nil {
		return err
	}

	if err := s.destroyJailTemplateDatasetIfPresent(ctx, template.Pool, template.RootDataset, true); err != nil {
		return fmt.Errorf("failed_to_delete_template_dataset: %w", err)
	}

	if err := s.DB.Delete(&template).Error; err != nil {
		return fmt.Errorf("failed_to_delete_template_db_record: %w", err)
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_template_delete_%d", templateID))
	return nil
}
