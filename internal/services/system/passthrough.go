// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/system/pciconf"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

func (s *Service) SyncPPTDevices() error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var ids []models.PassedThroughIDs
	if err := s.DB.Find(&ids).Error; err != nil {
		return fmt.Errorf("loading PassedThroughIDs: %w", err)
	}

	const (
		loaderConf = "/boot/loader.conf"
		key        = "pptdevs"
	)

	data, err := os.ReadFile(loaderConf)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", loaderConf, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")

	if len(ids) == 0 {
		var filtered []string
		removed := false
		for _, ln := range lines {
			if strings.HasPrefix(strings.TrimSpace(ln), key+"=") {
				removed = true
				continue
			}
			filtered = append(filtered, ln)
		}
		if removed {
			out := strings.Join(filtered, "\n")
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			perm := os.FileMode(0644)
			if fi, err := os.Stat(loaderConf); err == nil {
				perm = fi.Mode().Perm()
			}
			if err := os.WriteFile(loaderConf, []byte(out), perm); err != nil {
				return fmt.Errorf("writing %s: %w", loaderConf, err)
			}
		}
		return nil
	}

	var parts []string
	for _, rec := range ids {
		parts = append(parts, rec.DeviceID)
	}

	newLine := fmt.Sprintf(`%s="%s"`, key, strings.Join(parts, " "))

	replaced := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), key+"=") {
			lines[i] = newLine
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}

	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}

	perm := os.FileMode(0644)
	if fi, err := os.Stat(loaderConf); err == nil {
		perm = fi.Mode().Perm()
	}

	if err := os.WriteFile(loaderConf, []byte(out), perm); err != nil {
		return fmt.Errorf("writing %s: %w", loaderConf, err)
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

	intDomain, err := strconv.Atoi(domain)

	if err != nil {
		return fmt.Errorf("invalid domain number: %v", err)
	}

	if intDomain < 0 || intDomain > 255 {
		return fmt.Errorf("domain number must be between 0 and 255")
	}

	var validPPTID = regexp.MustCompile(`^\d+/\d+/\d+$`)
	if !validPPTID.MatchString(id) {
		return fmt.Errorf("invalid device ID format: must be 'number/number/number'")
	}

	pciDevices, err := pciconf.GetPCIDevices()
	if err != nil {
		return fmt.Errorf("getting PCI devices: %w", err)
	}

	var found bool

	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		return fmt.Errorf("invalid format: expected 'num/num/num'")
	}

	intParts := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("invalid number in device ID: %v", err)
		}
		intParts[i] = n
	}

	var driver string

	for _, device := range pciDevices {
		if device.Domain == intDomain && device.Bus == intParts[0] && device.Device == intParts[1] && device.Function == intParts[2] {
			driver = device.Name
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("device ID %s not found in PCI devices", id)
	}

	detach, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"detach",
		"-f",
		fmt.Sprintf("pci%d:%d:%d:%d", intDomain, intParts[0], intParts[1], intParts[2]),
	)

	if err != nil && detach != "" {
		if !strings.HasSuffix(strings.TrimSpace(detach), "Device not configured") {
			return fmt.Errorf("detaching device %s on root bus %s failed %s: %w", id, domain, detach, err)
		}
	}

	clearDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"clear",
		"driver",
		"-f",
		fmt.Sprintf("pci%d:%d:%d:%d", intDomain, intParts[0], intParts[1], intParts[2]),
	)

	if err != nil && clearDriver != "" {
		return fmt.Errorf("clearing driver for device %s on root bus %s failed %s: %w", id, domain, clearDriver, err)
	}

	setDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"set",
		"driver",
		fmt.Sprintf("pci%d:%d:%d:%d", intDomain, intParts[0], intParts[1], intParts[2]),
		"ppt",
	)

	if err != nil && setDriver != "" {
		return fmt.Errorf("setting driver for device %s on root bus %s failed %s: %w", id, domain, setDriver, err)
	}

	newID := models.PassedThroughIDs{DeviceID: id, OldDriver: driver}
	if err := s.DB.Create(&newID).Error; err != nil {
		return fmt.Errorf("adding PassedThroughIDs: %w", err)
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
		for _, id := range vm.PCIDevices {
			if id == existing.ID {
				result = append(result, vm)
				break
			}
		}
	}

	if len(result) > 0 {
		return fmt.Errorf("device_%d_in_use_by_vm", existing.ID)
	}

	parts := strings.Split(existing.DeviceID, "/")
	if len(parts) != 3 {
		return fmt.Errorf("invalid device ID format: expected 'num/num/num'")
	}

	detach, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"detach",
		"-f",
		fmt.Sprintf("pci%d:%s:%s:%s", existing.Domain, parts[0], parts[1], parts[2]),
	)

	if err != nil && detach != "" {
		return fmt.Errorf("detaching device %s failed %s: %w", existing.DeviceID, detach, err)
	}

	clearDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"clear",
		"driver",
		"-f",
		fmt.Sprintf("pci%d:%s:%s:%s", existing.Domain, parts[0], parts[1], parts[2]),
	)

	if err != nil && clearDriver != "" {
		return fmt.Errorf("clearing driver for device %s failed %s: %w", existing.DeviceID, clearDriver, err)
	}

	setDriver, err := utils.RunCommand(
		"/usr/sbin/devctl",
		"set",
		"driver",
		fmt.Sprintf("pci%d:%s:%s:%s", existing.Domain, parts[0], parts[1], parts[2]),
		existing.OldDriver,
	)

	if err != nil && setDriver != "" {
		return fmt.Errorf("setting driver for device %s failed %s: %w", existing.DeviceID, setDriver, err)
	}

	if err := s.DB.Delete(&existing).Error; err != nil {
		return fmt.Errorf("removing PassedThroughIDs: %w", err)
	}

	return s.SyncPPTDevices()
}
