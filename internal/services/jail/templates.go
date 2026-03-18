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
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
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
}

type createTarget struct {
	CTID uint
	Name string
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
			SourceCTID:     t.SourceCTID,
			SourceJailName: t.SourceJailName,
		})
	}

	return out, nil
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

func (s *Service) ConvertJailToTemplate(ctx context.Context, ctID uint) error {
	if ctID == 0 {
		return fmt.Errorf("invalid_ct_id")
	}

	jail, err := s.GetJailByCTID(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}

	allowed, leaseErr := s.canMutateProtectedJail(ctID)
	if leaseErr != nil {
		return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
	}
	if !allowed {
		return fmt.Errorf("replication_lease_not_owned")
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
	templateDataset := fmt.Sprintf("%s/sylve/jails/clones/%d", pool, ctID)

	state, err := s.GetStateByCtId(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_state: %w", err)
	}

	wasRunning := state.State == "ACTIVE"
	if wasRunning {
		if err := s.JailAction(int(ctID), "stop"); err != nil {
			return fmt.Errorf("failed_to_stop_jail_before_template_conversion: %w", err)
		}
	}
	defer func() {
		if wasRunning {
			_ = s.JailAction(int(ctID), "start")
		}
	}()

	srcDS, err := s.GZFS.ZFS.Get(ctx, sourceDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_source_jail_dataset: %w", err)
	}
	if srcDS == nil {
		return fmt.Errorf("source_jail_dataset_not_found")
	}

	if existing, getErr := s.GZFS.ZFS.Get(ctx, templateDataset, false); getErr == nil && existing != nil {
		if err := existing.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_destroy_existing_template_dataset: %w", err)
		}
	}

	snapshotName := fmt.Sprintf("sylve_template_%d_%d", ctID, time.Now().UTC().UnixMilli())
	snapshot, err := srcDS.Snapshot(ctx, snapshotName, true)
	if err != nil {
		return fmt.Errorf("failed_to_create_template_snapshot: %w", err)
	}
	defer func() {
		_ = snapshot.Destroy(ctx, true, false)
	}()

	if _, err := snapshot.SendToDataset(ctx, templateDataset, true); err != nil {
		return fmt.Errorf("failed_to_copy_jail_dataset_to_template: %w", err)
	}

	templateName := strings.TrimSpace(jail.Name)
	if templateName == "" {
		templateName = fmt.Sprintf("Jail-%d", jail.CTID)
	}
	templateName = fmt.Sprintf("%s Template", templateName)

	template := jailModels.JailTemplate{
		Name:              templateName,
		SourceCTID:        jail.CTID,
		SourceJailName:    jail.Name,
		Pool:              pool,
		RootDataset:       templateDataset,
		Type:              jail.Type,
		ResourceLimits:    jail.ResourceLimits,
		Cores:             jail.Cores,
		Memory:            jail.Memory,
		InheritIPv4:       jail.InheritIPv4,
		InheritIPv6:       jail.InheritIPv6,
		Fstab:             jail.Fstab,
		ResolvConf:        jail.ResolvConf,
		DevFSRuleset:      jail.DevFSRuleset,
		CleanEnvironment:  jail.CleanEnvironment,
		AdditionalOptions: jail.AdditionalOptions,
		AllowedOptions:    append([]string{}, jail.AllowedOptions...),
		MetadataMeta:      jail.MetadataMeta,
		MetadataEnv:       jail.MetadataEnv,
		Networks:          s.buildTemplateNetworks(jail.Networks),
		Hooks:             s.buildTemplateHooks(jail.JailHooks),
	}

	var existing jailModels.JailTemplate
	if err := s.DB.Where("source_ct_id = ?", ctID).First(&existing).Error; err == nil {
		template.ID = existing.ID
		if err := s.DB.Model(&existing).Updates(&template).Error; err != nil {
			return fmt.Errorf("failed_to_update_jail_template: %w", err)
		}
	} else if err == gorm.ErrRecordNotFound {
		if err := s.DB.Create(&template).Error; err != nil {
			return fmt.Errorf("failed_to_create_jail_template: %w", err)
		}
	} else {
		return fmt.Errorf("failed_to_query_existing_jail_template: %w", err)
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_template_convert_%d", ctID))
	return nil
}

func (s *Service) buildCreateTargets(template jailModels.JailTemplate, req CreateFromTemplateRequest) ([]createTarget, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "single"
	}

	if mode == "single" {
		if req.CTID == 0 {
			return nil, fmt.Errorf("ctid_required")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = strings.TrimSpace(template.SourceJailName)
			if name == "" {
				name = fmt.Sprintf("jail-%d", req.CTID)
			}
		}
		return []createTarget{{CTID: req.CTID, Name: name}}, nil
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

	namePrefix := strings.TrimSpace(req.NamePrefix)
	if namePrefix == "" {
		namePrefix = strings.TrimSpace(template.Name)
	}
	if namePrefix == "" {
		namePrefix = "jail"
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
		})
	}

	return targets, nil
}

func (s *Service) preflightTemplateTargets(targets []createTarget) error {
	ctids := make([]uint, 0, len(targets))
	for _, t := range targets {
		ctids = append(ctids, t.CTID)
	}

	var existingCount int64
	if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id IN ?", ctids).Count(&existingCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_existing_ctids: %w", err)
	}
	if existingCount > 0 {
		return fmt.Errorf("ctid_range_contains_used_values")
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

func (s *Service) createJailFromTemplateTarget(ctx context.Context, template jailModels.JailTemplate, target createTarget) error {
	templateDS, err := s.GZFS.ZFS.Get(ctx, template.RootDataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_template_dataset: %w", err)
	}
	if templateDS == nil {
		return fmt.Errorf("template_dataset_not_found")
	}

	datasetName := fmt.Sprintf("%s/sylve/jails/%d", template.Pool, target.CTID)
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", template.Pool, target.CTID)

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
	defer func() {
		_ = snapshot.Destroy(ctx, true, false)
	}()

	createdDS, err := snapshot.SendToDataset(ctx, datasetName, false)
	if err != nil {
		return fmt.Errorf("failed_to_clone_template_dataset: %w", err)
	}

	var createdJail jailModels.Jail
	macByNetworkIndex := map[int]string{}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		createdJail = jailModels.Jail{
			Name:              target.Name,
			CTID:              target.CTID,
			Type:              template.Type,
			Description:       "",
			StartAtBoot:       nil,
			StartOrder:        0,
			InheritIPv4:       template.InheritIPv4,
			InheritIPv6:       template.InheritIPv6,
			ResourceLimits:    template.ResourceLimits,
			Cores:             template.Cores,
			Memory:            template.Memory,
			DevFSRuleset:      template.DevFSRuleset,
			Fstab:             template.Fstab,
			ResolvConf:        template.ResolvConf,
			CleanEnvironment:  template.CleanEnvironment,
			AdditionalOptions: template.AdditionalOptions,
			AllowedOptions:    append([]string{}, template.AllowedOptions...),
			MetadataMeta:      template.MetadataMeta,
			MetadataEnv:       template.MetadataEnv,
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
			Pool:   template.Pool,
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
			macID, macAddr, err := s.allocateMACObject(tx, fmt.Sprintf("%s-net-%d", target.Name, idx+1))
			if err != nil {
				return err
			}
			macByNetworkIndex[idx] = macAddr
			macIDCopy := macID

			network := jailModels.Network{
				JailID:         createdJail.ID,
				Name:           n.Name,
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
		if createdDS != nil {
			_ = createdDS.Destroy(ctx, true, false)
		}
		return err
	}

	defer func() {
		if err != nil {
			_ = s.DeleteJail(ctx, target.CTID, true, true)
		}
	}()

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

	if strings.TrimSpace(createdJail.ResolvConf) != "" && createdJail.Type == jailModels.JailTypeFreeBSD {
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

	firstMAC := ""
	if len(reloaded.Networks) > 0 {
		firstMAC = macByNetworkIndex[0]
		if firstMAC == "" && reloaded.Networks[0].MacID != nil {
			firstMAC, _ = s.NetworkService.GetObjectEntryByID(*reloaded.Networks[0].MacID)
		}
	}

	cfg, err := s.CreateJailConfig(*reloaded, mountPoint, firstMAC)
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

	if err := s.WriteJailJSON(target.CTID); err != nil {
		return fmt.Errorf("failed_to_write_jail_json_from_template: %w", err)
	}

	return nil
}

func (s *Service) CreateJailsFromTemplate(ctx context.Context, templateID uint, req CreateFromTemplateRequest) error {
	if templateID == 0 {
		return fmt.Errorf("invalid_template_id")
	}

	var template jailModels.JailTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		return fmt.Errorf("template_not_found: %w", err)
	}

	targets, err := s.buildCreateTargets(template, req)
	if err != nil {
		return err
	}
	if err := s.preflightTemplateTargets(targets); err != nil {
		return err
	}

	for _, target := range targets {
		if err := s.createJailFromTemplateTarget(ctx, template, target); err != nil {
			return err
		}
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_template_create_%d", templateID))
	return nil
}

func (s *Service) DeleteJailTemplate(ctx context.Context, templateID uint) error {
	if templateID == 0 {
		return fmt.Errorf("invalid_template_id")
	}

	var template jailModels.JailTemplate
	if err := s.DB.First(&template, "id = ?", templateID).Error; err != nil {
		return fmt.Errorf("template_not_found: %w", err)
	}

	if err := s.DB.Delete(&template).Error; err != nil {
		return fmt.Errorf("failed_to_delete_template_db_record: %w", err)
	}

	ds, err := s.GZFS.ZFS.Get(ctx, template.RootDataset, false)
	if err == nil && ds != nil {
		if err := ds.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_delete_template_dataset: %w", err)
		}
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_template_delete_%d", templateID))
	return nil
}
