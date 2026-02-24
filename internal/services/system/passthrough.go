// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/system/pciconf"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

const (
	loaderConfPath = "/boot/loader.conf"
	loaderConfKey  = "pptdevs"
)

var validPPTID = regexp.MustCompile(`^\d+/\d+/\d+$`)

func parseLoaderConfAssignment(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}

	index := strings.Index(trimmed, "=")
	if index < 0 {
		return "", "", false
	}

	key := strings.TrimSpace(trimmed[:index])
	value := strings.TrimSpace(trimmed[index+1:])
	if key == "" {
		return "", "", false
	}

	return key, value, true
}

func parseLoaderPPTValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if value[0] == '"' || value[0] == '\'' {
		quote := value[0]
		value = value[1:]
		if end := strings.IndexByte(value, quote); end >= 0 {
			return value[:end]
		}
		return value
	}

	if comment := strings.Index(value, "#"); comment >= 0 {
		value = value[:comment]
	}

	return strings.TrimSpace(value)
}

func dedupePPTIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || !validPPTID.MatchString(id) {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}
		out = append(out, id)
	}

	return out
}

func parsePPTIDsFromLoader(lines []string) []string {
	ids := []string{}

	for _, line := range lines {
		key, value, ok := parseLoaderConfAssignment(line)
		if !ok || key != loaderConfKey {
			continue
		}

		value = parseLoaderPPTValue(value)
		ids = append(ids, strings.Fields(value)...)
	}

	return dedupePPTIDs(ids)
}

func rewriteLoaderPPTIDs(lines []string, ids []string) []string {
	ids = dedupePPTIDs(ids)

	filtered := make([]string, 0, len(lines)+1)
	replaced := false

	for _, line := range lines {
		key, _, ok := parseLoaderConfAssignment(line)
		if ok && key == loaderConfKey {
			if len(ids) > 0 && !replaced {
				filtered = append(filtered, fmt.Sprintf(`%s="%s"`, loaderConfKey, strings.Join(ids, " ")))
				replaced = true
			}
			continue
		}

		filtered = append(filtered, line)
	}

	if !replaced && len(ids) > 0 {
		filtered = append(filtered, fmt.Sprintf(`%s="%s"`, loaderConfKey, strings.Join(ids, " ")))
	}

	return filtered
}

func readLoaderConf() ([]string, os.FileMode, error) {
	data, err := os.ReadFile(loaderConfPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, 0, fmt.Errorf("reading %s: %w", loaderConfPath, err)
	}

	lines := []string{}
	if len(data) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}

	perm := os.FileMode(0644)
	if fi, err := os.Stat(loaderConfPath); err == nil {
		perm = fi.Mode().Perm()
	}

	return lines, perm, nil
}

func (s *Service) writeLoaderConf(lines []string, perm os.FileMode) error {
	settings, err := s.GetBasicSettings()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to fetch basic settings for loader config: %w", err)
	}

	hasVirtualization := slices.Contains(settings.Services, models.Virtualization)

	vmmFound := false
	pptFound := false

	for i, line := range lines {
		key, _, ok := parseLoaderConfAssignment(line)
		if !ok {
			continue
		}

		switch key {
		case "vmm_load":
			vmmFound = true
			if hasVirtualization {
				lines[i] = `vmm_load="YES"`
			}
		case "ppt_load":
			pptFound = true
			lines[i] = `ppt_load="YES"`
		}
	}

	needsVmm := hasVirtualization && !vmmFound
	needsPpt := !pptFound

	if needsVmm || needsPpt {
		var newLines []string
		for _, line := range lines {
			key, _, ok := parseLoaderConfAssignment(line)
			if ok && key == loaderConfKey {
				if needsVmm {
					newLines = append(newLines, `vmm_load="YES"`)
					needsVmm = false
				}
				if needsPpt {
					newLines = append(newLines, `ppt_load="YES"`)
					needsPpt = false
				}
			}
			newLines = append(newLines, line)
		}

		if needsVmm {
			newLines = append(newLines, `vmm_load="YES"`)
		}
		if needsPpt {
			newLines = append(newLines, `ppt_load="YES"`)
		}

		lines = newLines
	}

	out := ""
	if len(lines) > 0 {
		out = strings.Join(lines, "\n") + "\n"
	}

	if err := os.WriteFile(loaderConfPath, []byte(out), perm); err != nil {
		return fmt.Errorf("writing %s: %w", loaderConfPath, err)
	}

	return nil
}

func parsePPTAddress(id string) ([3]int, error) {
	var parts [3]int

	if !validPPTID.MatchString(id) {
		return parts, fmt.Errorf("invalid device ID format: must be 'number/number/number'")
	}

	p := strings.Split(id, "/")
	if len(p) != 3 {
		return parts, fmt.Errorf("invalid format: expected 'num/num/num'")
	}

	for i, part := range p {
		n, err := strconv.Atoi(part)
		if err != nil {
			return parts, fmt.Errorf("invalid number in device ID: %v", err)
		}
		parts[i] = n
	}

	return parts, nil
}

func parseDomain(domain string) (int, error) {
	intDomain, err := strconv.Atoi(domain)
	if err != nil {
		return 0, fmt.Errorf("invalid domain number: %v", err)
	}

	if intDomain < 0 || intDomain > 255 {
		return 0, fmt.Errorf("domain number must be between 0 and 255")
	}

	return intDomain, nil
}

func findPCIDeviceByDomainAndAddress(pciDevices []pciconf.PCIDevice, domain int, parts [3]int) (pciconf.PCIDevice, bool) {
	for _, device := range pciDevices {
		if device.Domain == domain && device.Bus == parts[0] && device.Device == parts[1] && device.Function == parts[2] {
			return device, true
		}
	}

	return pciconf.PCIDevice{}, false
}

func pciAddress(domain int, parts [3]int) string {
	return fmt.Sprintf("pci%d:%d:%d:%d", domain, parts[0], parts[1], parts[2])
}

func (s *Service) addLoaderPPTDevice(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	lines, perm, err := readLoaderConf()
	if err != nil {
		return err
	}

	ids := parsePPTIDsFromLoader(lines)
	if slices.Contains(ids, id) {
		return nil
	}

	ids = append(ids, id)
	lines = rewriteLoaderPPTIDs(lines, ids)
	return s.writeLoaderConf(lines, perm)
}

func (s *Service) removeLoaderPPTDevice(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	lines, perm, err := readLoaderConf()
	if err != nil {
		return err
	}

	ids := parsePPTIDsFromLoader(lines)
	filtered := make([]string, 0, len(ids))
	for _, loaderID := range ids {
		if loaderID != id {
			filtered = append(filtered, loaderID)
		}
	}

	lines = rewriteLoaderPPTIDs(lines, filtered)
	return s.writeLoaderConf(lines, perm)
}

func (s *Service) getLoaderPPTDevices() ([]string, error) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	lines, _, err := readLoaderConf()
	if err != nil {
		return nil, err
	}

	return parsePPTIDsFromLoader(lines), nil
}

func (s *Service) SyncPPTDevices() error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var ids []models.PassedThroughIDs
	if err := s.DB.Find(&ids).Error; err != nil {
		return fmt.Errorf("loading PassedThroughIDs: %w", err)
	}

	lines, perm, err := readLoaderConf()
	if err != nil {
		return err
	}

	parts := parsePPTIDsFromLoader(lines)
	known := make(map[string]struct{}, len(parts))
	for _, id := range parts {
		known[id] = struct{}{}
	}

	for _, rec := range ids {
		if rec.Domain == 0 {
			if _, ok := known[rec.DeviceID]; ok {
				continue
			}
			parts = append(parts, rec.DeviceID)
			known[rec.DeviceID] = struct{}{}
		} else {
			// Todo: Please do MANUAL SYNC!
			fmt.Printf("Warning: Device %s is on domain %d. Skipping loader.conf sync.\n", rec.DeviceID, rec.Domain)
		}
	}

	lines = rewriteLoaderPPTIDs(lines, parts)
	return s.writeLoaderConf(lines, perm)
}

func (s *Service) ReconcilePreparedPPTDevices() error {
	loaderIDs, err := s.getLoaderPPTDevices()
	if err != nil {
		return fmt.Errorf("loading prepared passthrough IDs: %w", err)
	}

	if len(loaderIDs) == 0 {
		return nil
	}

	var existing []models.PassedThroughIDs
	if err := s.DB.Find(&existing).Error; err != nil {
		return fmt.Errorf("loading PassedThroughIDs: %w", err)
	}

	existingMap := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		existingMap[item.DeviceID] = struct{}{}
	}

	pciDevices, err := pciconf.GetPCIDevices()
	if err != nil {
		return fmt.Errorf("getting PCI devices: %w", err)
	}

	devicesByID := make(map[string]pciconf.PCIDevice, len(pciDevices))
	for _, device := range pciDevices {
		if device.Domain != 0 {
			continue
		}

		deviceID := fmt.Sprintf("%d/%d/%d", device.Bus, device.Device, device.Function)
		devicesByID[deviceID] = device
	}

	for _, deviceID := range loaderIDs {
		if _, exists := existingMap[deviceID]; exists {
			continue
		}

		device, found := devicesByID[deviceID]
		if !found {
			continue
		}

		if !strings.HasPrefix(device.Name, "ppt") {
			continue
		}

		record := models.PassedThroughIDs{
			DeviceID:  deviceID,
			Domain:    0,
			OldDriver: "",
		}

		if err := s.DB.Create(&record).Error; err != nil {
			return fmt.Errorf("creating prepared passthrough entry for %s: %w", deviceID, err)
		}

		existingMap[deviceID] = struct{}{}
	}

	return nil
}

func (s *Service) GetPPTDevices() ([]models.PassedThroughIDs, error) {
	var ids []models.PassedThroughIDs
	if err := s.DB.Find(&ids).Error; err != nil {
		return nil, fmt.Errorf("loading PassedThroughIDs: %w", err)
	}
	return ids, nil
}

func (s *Service) AddPPTDevice(domain string, id string) error {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	intDomain, err := parseDomain(domain)
	if err != nil {
		return err
	}

	parts, err := parsePPTAddress(id)
	if err != nil {
		return err
	}

	pciDevices, err := pciconf.GetPCIDevices()
	if err != nil {
		return fmt.Errorf("getting PCI devices: %w", err)
	}

	device, found := findPCIDeviceByDomainAndAddress(pciDevices, intDomain, parts)
	if !found {
		return fmt.Errorf("device ID %s not found in PCI devices", id)
	}

	driver := device.Name
	pciAddr := pciAddress(intDomain, parts)

	detach, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"detach",
		"-f",
		pciAddr,
	)

	if err != nil {
		if !strings.HasSuffix(strings.TrimSpace(detach), "Device not configured") {
			return fmt.Errorf("detaching device %s on root bus %s failed %s: %w", id, domain, detach, err)
		}
	}

	clearDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"clear",
		"driver",
		"-f",
		pciAddr,
	)

	if err != nil {
		return fmt.Errorf("clearing driver for device %s on root bus %s failed %s: %w", id, domain, clearDriver, err)
	}

	setDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"set",
		"driver",
		pciAddr,
		"ppt",
	)

	if err != nil {
		return fmt.Errorf("setting driver for device %s on root bus %s failed %s: %w", id, domain, setDriver, err)
	}

	newID := models.PassedThroughIDs{
		DeviceID:  id,
		Domain:    intDomain,
		OldDriver: driver,
	}

	if err := s.DB.Create(&newID).Error; err != nil {
		_, rollbackErr := utils.RunCommand("/usr/sbin/devctl", "set", "driver", pciAddr, driver)

		if rollbackErr != nil {
			return fmt.Errorf("CRITICAL STATE MISMATCH: failed to save to DB (%v), and failed to revert device %s back to %s (%v)", err, pciAddr, driver, rollbackErr)
		}

		return fmt.Errorf("database insert failed, hardware state reverted: %w", err)
	}

	return s.SyncPPTDevices()
}

func (s *Service) PreparePPTDevice(domain string, id string) error {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	intDomain, err := parseDomain(domain)
	if err != nil {
		return err
	}

	if intDomain != 0 {
		return fmt.Errorf("prepare passthrough supports domain 0 only")
	}

	parts, err := parsePPTAddress(id)
	if err != nil {
		return err
	}

	pciDevices, err := pciconf.GetPCIDevices()
	if err != nil {
		return fmt.Errorf("getting PCI devices: %w", err)
	}

	if _, found := findPCIDeviceByDomainAndAddress(pciDevices, intDomain, parts); !found {
		return fmt.Errorf("device ID %s not found in PCI devices", id)
	}

	return s.addLoaderPPTDevice(id)
}

func (s *Service) ImportPPTDevice(domain string, id string) error {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	intDomain, err := parseDomain(domain)
	if err != nil {
		return err
	}

	parts, err := parsePPTAddress(id)
	if err != nil {
		return err
	}

	pciDevices, err := pciconf.GetPCIDevices()
	if err != nil {
		return fmt.Errorf("getting PCI devices: %w", err)
	}

	device, found := findPCIDeviceByDomainAndAddress(pciDevices, intDomain, parts)
	if !found {
		return fmt.Errorf("device ID %s not found in PCI devices", id)
	}

	if !strings.HasPrefix(device.Name, "ppt") {
		return fmt.Errorf("device ID %s is not currently attached to ppt", id)
	}

	var existing models.PassedThroughIDs
	if err := s.DB.Where("device_id = ?", id).First(&existing).Error; err == nil {
		return nil
	} else if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("checking PassedThroughIDs: %w", err)
	}

	record := models.PassedThroughIDs{
		DeviceID:  id,
		Domain:    intDomain,
		OldDriver: "",
	}

	if err := s.DB.Create(&record).Error; err != nil {
		return fmt.Errorf("creating PassedThroughIDs: %w", err)
	}

	if intDomain == 0 {
		if err := s.addLoaderPPTDevice(id); err != nil {
			if rollbackErr := s.DB.Delete(&record).Error; rollbackErr != nil {
				return fmt.Errorf("CRITICAL STATE MISMATCH: failed to update loader.conf (%v), and failed to revert DB insert for %s (%v)", err, id, rollbackErr)
			}

			return err
		}
	}

	return s.SyncPPTDevices()
}

func (s *Service) RemovePPTDevice(id string) error {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	if id == "" {
		return fmt.Errorf("device ID cannot be empty")
	}

	var existing models.PassedThroughIDs
	if err := s.DB.Where("id = ?", id).First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("device ID %s not found", id)
		}
		return fmt.Errorf("checking PassedThroughIDs: %w", err)
	}

	var vms []vmModels.VM
	_ = s.DB.Find(&vms)

	var result []vmModels.VM
	for _, vm := range vms {
		if slices.Contains(vm.PCIDevices, existing.ID) {
			result = append(result, vm)
		}
	}

	if len(result) > 0 {
		return fmt.Errorf("device_%d_in_use_by_vm", existing.ID)
	}

	parts, err := parsePPTAddress(existing.DeviceID)
	if err != nil {
		return err
	}

	pciAddr := pciAddress(existing.Domain, parts)

	detach, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"detach",
		"-f",
		pciAddr,
	)

	if err != nil {
		return fmt.Errorf("detaching device %s failed %s: %w", existing.DeviceID, detach, err)
	}

	clearDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"clear",
		"driver",
		"-f",
		pciAddr,
	)

	if err != nil {
		return fmt.Errorf("clearing driver for device %s failed %s: %w", existing.DeviceID, clearDriver, err)
	}

	if strings.TrimSpace(existing.OldDriver) == "" {
		rescanOutput, err := utils.RunCommand(
			"/usr/sbin/devctl",
			"rescan",
			pciAddr,
		)

		if err != nil {
			return fmt.Errorf("rescanning device %s failed %s: %w", existing.DeviceID, rescanOutput, err)
		}
	} else {
		setDriver, err := utils.RunCommand(
			"/usr/sbin/devctl",
			"set",
			"driver",
			pciAddr,
			existing.OldDriver,
		)

		if err != nil {
			return fmt.Errorf("setting driver for device %s failed %s: %w", existing.DeviceID, setDriver, err)
		}
	}

	if err := s.DB.Delete(&existing).Error; err != nil {
		_, rollbackErr := utils.RunCommand("/usr/sbin/devctl", "set", "driver", pciAddr, "ppt")
		if rollbackErr != nil {
			return fmt.Errorf("CRITICAL STATE MISMATCH: failed to delete from DB (%v), and failed to revert device %s back to ppt (%v)", err, existing.DeviceID, rollbackErr)
		}

		return fmt.Errorf("database delete failed, hardware state reverted to ppt: %w", err)
	}

	if err := s.removeLoaderPPTDevice(existing.DeviceID); err != nil {
		return err
	}

	return s.SyncPPTDevices()
}
