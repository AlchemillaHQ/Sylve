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
	"strconv"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/beevik/etree"
	"gorm.io/gorm"
)

const (
	MaxRequestBodyBytes int64 = 1 * 1024 * 1024

	maximumShutdownWaitTimeSeconds = 3600
	maximumExtraBhyveOptionCount   = 128
	maximumExtraBhyveOptionBytes   = 4096
	maximumExtraBhyveOptionsBytes  = 64 * 1024
)

type vmOptionDataMutationHooks struct {
	writeVMJSON   func(*gorm.DB, uint) error
	restoreVMJSON func(uint) error
}

func (s *Service) normalizeVMOptionDataMutationHooks(
	hooks vmOptionDataMutationHooks,
) vmOptionDataMutationHooks {
	if hooks.writeVMJSON == nil {
		hooks.writeVMJSON = s.writeVMJsonWithDB
	}
	if hooks.restoreVMJSON == nil {
		hooks.restoreVMJSON = s.WriteVMJson
	}
	return hooks
}

func (s *Service) applyVMOptionDataMutation(
	rid uint,
	updateDB func(*gorm.DB) error,
	hooks vmOptionDataMutationHooks,
) error {
	hooks = s.normalizeVMOptionDataMutationHooks(hooks)
	mutationStarted := false
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := updateDB(tx); err != nil {
			return err
		}
		mutationStarted = true
		if err := hooks.writeVMJSON(tx, rid); err != nil {
			return fmt.Errorf("failed_to_write_vm_json_after_option_update: %w", err)
		}
		return nil
	})
	if err == nil || !mutationStarted {
		return err
	}

	if restoreErr := hooks.restoreVMJSON(rid); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("vm_option_json_reconciliation_failed: %w", restoreErr))
	}
	return err
}

func (s *Service) prepareVMOptionMutation(rid uint, requireShutoff bool) (vmModels.VM, error) {
	if s == nil || s.DB == nil {
		return vmModels.VM{}, fmt.Errorf("db_not_initialized")
	}
	if rid == 0 {
		return vmModels.VM{}, fmt.Errorf("invalid_vm_rid")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vmModels.VM{}, fmt.Errorf("vm_not_found: %w", err)
		}
		return vmModels.VM{}, fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return vmModels.VM{}, err
	}
	if !requireShutoff {
		return vm, nil
	}

	shutoff, err := s.IsDomainShutOff(rid)
	if err != nil {
		return vmModels.VM{}, fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !shutoff {
		return vmModels.VM{}, fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}
	return vm, nil
}

func (s *Service) lockVMOptionMutation() func() {
	s.crudMutex.Lock()
	s.actionMutex.Lock()
	return func() {
		s.actionMutex.Unlock()
		s.crudMutex.Unlock()
	}
}

func validateExtraBhyveOptions(options []string) ([]string, error) {
	normalized := normalizeExtraBhyveOptions(options)
	if len(normalized) > maximumExtraBhyveOptionCount {
		return nil, fmt.Errorf("too_many_extra_bhyve_options")
	}

	totalBytes := 0
	for _, option := range normalized {
		if strings.ContainsRune(option, '\x00') {
			return nil, fmt.Errorf("invalid_extra_bhyve_option")
		}
		if len(option) > maximumExtraBhyveOptionBytes {
			return nil, fmt.Errorf("extra_bhyve_option_too_long")
		}
		totalBytes += len(option)
	}
	if totalBytes > maximumExtraBhyveOptionsBytes {
		return nil, fmt.Errorf("extra_bhyve_options_too_large")
	}
	return normalized, nil
}

func validateCloudInitConfiguration(data, metadata, networkConfig string) error {
	if (data == "") != (metadata == "") {
		return fmt.Errorf("both_data_and_metadata_must_be_provided")
	}
	if data != "" && (!utils.IsValidYAML(data) || !utils.IsValidYAML(metadata)) {
		return fmt.Errorf("invalid_yaml_in_cloud_init_data_or_metadata")
	}
	if networkConfig != "" && !utils.IsValidYAML(networkConfig) {
		return fmt.Errorf("invalid_yaml_in_cloud_init_network_config")
	}
	return nil
}

func vmHasCloudInitConfiguration(vm vmModels.VM) bool {
	return vm.CloudInitData != "" || vm.CloudInitMetaData != "" || vm.CloudInitNetworkConfig != ""
}

func updateClockOptionXML(xml, timeOffset string) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed_to_parse_xml: %w", err)
	}
	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("invalid_domain_xml: root_missing")
	}

	clock := doc.FindElement("//clock")
	if clock == nil {
		clock = root.CreateElement("clock")
	}
	if offset := clock.SelectAttr("offset"); offset == nil {
		clock.CreateAttr("offset", timeOffset)
	} else {
		offset.Value = timeOffset
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed_to_serialize_xml: %w", err)
	}
	return out, nil
}

func updateSerialOptionXML(xml string, rid uint, enabled bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed_to_parse_xml: %w", err)
	}
	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("invalid_domain_xml: root_missing")
	}

	master := "/dev/nmdm" + strconv.Itoa(int(rid)) + "A"
	devices := doc.FindElement("//devices")
	if devices != nil {
		for _, element := range append([]*etree.Element{}, devices.ChildElements()...) {
			if element.Tag != "serial" && element.Tag != "console" {
				continue
			}
			source := element.FindElement("source")
			if source != nil && source.SelectAttrValue("master", "") == master {
				devices.RemoveChild(element)
			}
		}
	}

	if enabled {
		if devices == nil {
			devices = root.CreateElement("devices")
		}
		serial := devices.CreateElement("serial")
		serial.CreateAttr("type", "nmdm")
		source := serial.CreateElement("source")
		source.CreateAttr("master", master)
		source.CreateAttr("slave", "/dev/nmdm"+strconv.Itoa(int(rid))+"B")
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed_to_serialize_xml: %w", err)
	}
	return out, nil
}

func updateIgnoreUMSROptionXML(xml string, ignore bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed_to_parse_xml: %w", err)
	}
	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("invalid_domain_xml: root_missing")
	}

	features := doc.FindElement("//features")
	if features != nil {
		for _, element := range features.FindElements("msrs") {
			features.RemoveChild(element)
		}
	}

	if commandline := doc.FindElement("//commandline"); commandline != nil && commandline.Space == "bhyve" {
		for _, argument := range append([]*etree.Element{}, commandline.ChildElements()...) {
			if argument.SelectAttrValue("value", "") == "-w" {
				commandline.RemoveChild(argument)
			}
		}
		if len(commandline.ChildElements()) == 0 && commandline.Parent() != nil {
			commandline.Parent().RemoveChild(commandline)
		}
	}

	if ignore {
		if features == nil {
			features = root.CreateElement("features")
		}
		msrs := features.CreateElement("msrs")
		msrs.CreateAttr("unknown", "ignore")
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed_to_serialize_xml: %w", err)
	}
	return out, nil
}

func preserveDomainUUID(oldXML, rebuiltXML string) (string, error) {
	oldDocument := etree.NewDocument()
	if err := oldDocument.ReadFromString(oldXML); err != nil {
		return "", fmt.Errorf("failed_to_parse_existing_domain_xml: %w", err)
	}
	oldRoot := oldDocument.Root()
	if oldRoot == nil {
		return "", fmt.Errorf("invalid_existing_domain_xml: root_missing")
	}
	oldUUID := oldRoot.FindElement("./uuid")
	if oldUUID == nil || strings.TrimSpace(oldUUID.Text()) == "" {
		return rebuiltXML, nil
	}

	rebuiltDocument := etree.NewDocument()
	if err := rebuiltDocument.ReadFromString(rebuiltXML); err != nil {
		return "", fmt.Errorf("failed_to_parse_rebuilt_domain_xml: %w", err)
	}
	rebuiltRoot := rebuiltDocument.Root()
	if rebuiltRoot == nil {
		return "", fmt.Errorf("invalid_rebuilt_domain_xml: root_missing")
	}
	value := strings.TrimSpace(oldUUID.Text())
	if rebuiltUUID := rebuiltRoot.FindElement("./uuid"); rebuiltUUID != nil {
		rebuiltUUID.SetText(value)
	} else {
		rebuiltUUID := etree.NewElement("uuid")
		rebuiltUUID.SetText(value)
		if name := rebuiltRoot.FindElement("./name"); name != nil {
			rebuiltRoot.InsertChildAt(name.Index()+1, rebuiltUUID)
		} else {
			rebuiltRoot.InsertChildAt(0, rebuiltUUID)
		}
	}
	out, err := rebuiltDocument.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed_to_serialize_rebuilt_domain_xml: %w", err)
	}
	return out, nil
}

func updateTPMOptionXML(xml string, rid uint, vmPath string, enabled bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed_to_parse_xml: %w", err)
	}
	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("invalid_domain_xml: root_missing")
	}

	commandline := doc.FindElement("//bhyve:commandline")
	if commandline == nil {
		commandline = doc.FindElement("//commandline")
		if commandline != nil && commandline.Space != "bhyve" {
			commandline = nil
		}
	}
	if commandline != nil {
		for _, argument := range append([]*etree.Element{}, commandline.ChildElements()...) {
			if strings.HasPrefix(argument.SelectAttrValue("value", ""), "-ltpm") {
				commandline.RemoveChild(argument)
			}
		}
	}

	if enabled {
		if strings.TrimSpace(vmPath) == "" {
			return "", fmt.Errorf("invalid_vm_path")
		}
		if commandline == nil {
			if root.SelectAttr("xmlns:bhyve") == nil {
				root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
			}
			commandline = root.CreateElement("bhyve:commandline")
		}
		argument := commandline.CreateElement("bhyve:arg")
		argument.CreateAttr(
			"value",
			fmt.Sprintf("-ltpm,swtpm,%s", filepath.Join(vmPath, fmt.Sprintf("%d_tpm.socket", rid))),
		)
	} else if commandline != nil && len(commandline.ChildElements()) == 0 && commandline.Parent() != nil {
		commandline.Parent().RemoveChild(commandline)
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed_to_serialize_xml: %w", err)
	}
	return out, nil
}

func (s *Service) ModifyWakeOnLan(rid uint, enabled bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, false)
	if err != nil {
		return err
	}
	if vm.WoL == enabled {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	return s.applyVMOptionDataMutation(rid, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("wo_l", enabled).Error; err != nil {
			return fmt.Errorf("failed_to_update_wol: %w", err)
		}
		return nil
	}, vmOptionDataMutationHooks{})
}

func (s *Service) ModifyBootOrder(rid uint, startAtBoot bool, bootOrder int) error {
	if bootOrder < 0 {
		return fmt.Errorf("start_order_must_be_greater_than_or_equal_to_0")
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, false)
	if err != nil {
		return err
	}
	if vm.StartAtBoot == startAtBoot && vm.StartOrder == bootOrder {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	return s.applyVMOptionDataMutation(rid, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Updates(map[string]any{
			"start_order":   bootOrder,
			"start_at_boot": startAtBoot,
		}).Error; err != nil {
			return fmt.Errorf("failed_to_update_boot_order: %w", err)
		}
		return nil
	}, vmOptionDataMutationHooks{})
}

func (s *Service) ModifyClock(rid uint, timeOffset string) error {
	timeOffset = strings.ToLower(strings.TrimSpace(timeOffset))
	if timeOffset != "utc" && timeOffset != "localtime" {
		return fmt.Errorf("invalid_time_offset: %s", timeOffset)
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if string(vm.TimeOffset) == timeOffset {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := updateClockOptionXML(oldXML, timeOffset)
	if err != nil {
		return err
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("time_offset", timeOffset).Error; err != nil {
			return fmt.Errorf("failed_to_update_time_offset_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifySerial(rid uint, enabled bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if vm.Serial == enabled {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := updateSerialOptionXML(oldXML, rid, enabled)
	if err != nil {
		return err
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("serial", enabled).Error; err != nil {
			return fmt.Errorf("failed_to_update_serial_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyShutdownWaitTime(rid uint, waitTime int) error {
	if waitTime < 1 || waitTime > maximumShutdownWaitTimeSeconds {
		return fmt.Errorf("shutdown_wait_time_out_of_range: must be between 1 and %d", maximumShutdownWaitTimeSeconds)
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, false)
	if err != nil {
		return err
	}
	if vm.ShutdownWaitTime == waitTime {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	return s.applyVMOptionDataMutation(rid, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("shutdown_wait_time", waitTime).Error; err != nil {
			return fmt.Errorf("failed_to_update_shutdown_wait_time: %w", err)
		}
		return nil
	}, vmOptionDataMutationHooks{})
}

func (s *Service) ModifyCloudInitData(rid uint, data string, metadata string, networkConfig string) error {
	if err := validateCloudInitConfiguration(data, metadata, networkConfig); err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if vm.CloudInitData == data && vm.CloudInitMetaData == metadata &&
		vm.CloudInitNetworkConfig == networkConfig {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}

	mutationStarted := false
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Updates(map[string]any{
			"cloud_init_data":           data,
			"cloud_init_meta_data":      metadata,
			"cloud_init_network_config": networkConfig,
		}).Error; err != nil {
			return fmt.Errorf("failed_to_update_cloud_init_data_in_db: %w", err)
		}
		mutationStarted = true
		if err := s.syncVMDisksWithDB(context.Background(), tx, rid); err != nil {
			return fmt.Errorf("failed_to_sync_cloud_init_configuration: %w", err)
		}
		return nil
	})
	if err == nil || !mutationStarted {
		return err
	}

	var restoreErr error
	if artifactErr := s.CreateCloudInitISO(vm); artifactErr != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_cloud_init_artifacts: %w", artifactErr))
	}
	if _, xmlErr := s.conn().DomainDefineXML(oldXML); xmlErr != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_domain_xml: %w", xmlErr))
	}
	if jsonErr := s.WriteVMJson(rid); jsonErr != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_vm_json: %w", jsonErr))
	}
	if restoreErr != nil {
		return errors.Join(err, fmt.Errorf("cloud_init_reconciliation_failed: %w", restoreErr))
	}
	return err
}

func (s *Service) ModifyBootROM(rid uint, bootROM string) error {
	normalizedBootROM, err := parseBootROMValue(bootROM)
	if err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if normalizeBootROMValue(vm.BootROM) == normalizedBootROM {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}

	vm.BootROM = normalizedBootROM
	vmPath, err := s.GetVMConfigDirectory(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_path: %w", err)
	}
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("failed_to_ensure_vm_path: %w", err)
	}
	if err := s.ensureVMBootROMArtifacts(vm.RID, vm.BootROM, vmPath); err != nil {
		return fmt.Errorf("failed_to_prepare_boot_rom_artifacts: %w", err)
	}
	newXML, err := s.CreateVmXML(vm, vmPath)
	if err != nil {
		return fmt.Errorf("failed_to_rebuild_domain_xml: %w", err)
	}
	newXML, err = preserveDomainUUID(oldXML, newXML)
	if err != nil {
		return fmt.Errorf("failed_to_preserve_domain_identity: %w", err)
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vmModels.VM{}).Where("id = ?", vm.ID).
			Update("boot_rom", normalizedBootROM).Error; err != nil {
			return fmt.Errorf("failed_to_update_boot_rom_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyExtraBhyveOptions(rid uint, options []string) error {
	normalized, err := validateExtraBhyveOptions(options)
	if err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if slices.Equal(normalizeExtraBhyveOptions(vm.ExtraBhyveOptions), normalized) {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}

	vm.ExtraBhyveOptions = normalized
	vmPath, err := s.GetVMConfigDirectory(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_path: %w", err)
	}
	vm.BootROM = normalizeBootROMValue(vm.BootROM)
	if err := s.ensureVMBootROMArtifacts(vm.RID, vm.BootROM, vmPath); err != nil {
		return fmt.Errorf("failed_to_prepare_boot_rom_artifacts: %w", err)
	}
	newXML, err := s.CreateVmXML(vm, vmPath)
	if err != nil {
		return fmt.Errorf("failed_to_rebuild_domain_xml: %w", err)
	}
	newXML, err = preserveDomainUUID(oldXML, newXML)
	if err != nil {
		return fmt.Errorf("failed_to_preserve_domain_identity: %w", err)
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vmModels.VM{}).Where("id = ?", vm.ID).
			Select("ExtraBhyveOptions").
			Updates(vmModels.VM{ExtraBhyveOptions: normalized}).Error; err != nil {
			return fmt.Errorf("failed_to_update_extra_bhyve_options_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyIgnoreUMSRs(rid uint, ignore bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if vm.IgnoreUMSR == ignore {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := updateIgnoreUMSROptionXML(oldXML, ignore)
	if err != nil {
		return err
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("ignore_umsr", ignore).Error; err != nil {
			return fmt.Errorf("failed_to_update_ignore_umsr_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyQemuGuestAgent(rid uint, enabled bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if vm.QemuGuestAgent == enabled {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}

	socketPath := ""
	if enabled {
		vmPath, err := s.GetVMConfigDirectory(vm.RID)
		if err != nil {
			return fmt.Errorf("failed_to_get_vm_data_path: %w", err)
		}
		socketPath = filepath.Join(vmPath, "qga.sock")
	}
	newXML, err := updateQemuGuestAgentXML(oldXML, socketPath, enabled)
	if err != nil {
		return err
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("qemu_guest_agent", enabled).Error; err != nil {
			return fmt.Errorf("failed_to_update_qemu_guest_agent_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyTPMEmulation(rid uint, enabled bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	unlock := s.lockVMOptionMutation()
	defer unlock()

	vm, err := s.prepareVMOptionMutation(rid, true)
	if err != nil {
		return err
	}
	if vm.TPMEmulation == enabled {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}
	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	vmPath, err := s.GetVMConfigDirectory(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_data_path: %w", err)
	}
	newXML, err := updateTPMOptionXML(oldXML, rid, vmPath, enabled)
	if err != nil {
		return err
	}

	if !enabled {
		if err := s.StopTPM(vm.RID); err != nil && !strings.Contains(err.Error(), "tpm_socket_not_found") {
			return fmt.Errorf("failed_to_stop_tpm_before_disable: %w", err)
		}
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("tpm_emulation", enabled).Error; err != nil {
			return fmt.Errorf("failed_to_update_tpm_emulation_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}
