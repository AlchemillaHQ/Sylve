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
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system/pciconf"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

const (
	loaderConfKey = "pptdevs"
)

var (
	loaderConfPath         = "/boot/loader.conf"
	validPPTID             = regexp.MustCompile(`^\d+/\d+/\d+$`)
	getPCIDevicesOperation = pciconf.GetPCIDevices
	runPPTCommand          = utils.RunCommand
)

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

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".sylve-*")
	if err != nil {
		return err
	}

	temporaryPath := temporary.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(perm); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return err
	}
	closed = true

	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true

	return nil
}

func ensureVMMLoadForPPTDevices(lines []string) []string {
	if len(parsePPTIDsFromLoader(lines)) == 0 {
		return lines
	}

	lines = append([]string(nil), lines...)
	vmmFound := false
	for i, line := range lines {
		key, _, ok := parseLoaderConfAssignment(line)
		if ok && key == "vmm_load" {
			lines[i] = `vmm_load="YES"`
			vmmFound = true
		}
	}

	if vmmFound {
		return lines
	}

	updated := make([]string, 0, len(lines)+1)
	inserted := false
	for _, line := range lines {
		key, _, ok := parseLoaderConfAssignment(line)
		if ok && key == loaderConfKey && !inserted {
			updated = append(updated, `vmm_load="YES"`)
			inserted = true
		}
		updated = append(updated, line)
	}

	return updated
}

func (s *Service) writeLoaderConf(lines []string, perm os.FileMode) error {
	lines = ensureVMMLoadForPPTDevices(lines)

	out := ""
	if len(lines) > 0 {
		out = strings.Join(lines, "\n") + "\n"
	}

	if err := writeFileAtomically(loaderConfPath, []byte(out), perm); err != nil {
		return fmt.Errorf("writing %s: %w", loaderConfPath, err)
	}

	return nil
}

func parsePPTAddress(id string) ([3]int, error) {
	var parts [3]int
	id = strings.TrimSpace(id)

	if !validPPTID.MatchString(id) {
		return parts, fmt.Errorf("%w: device ID must use bus/device/function", ErrInvalidPassthroughDevice)
	}

	p := strings.Split(id, "/")
	if len(p) != 3 {
		return parts, fmt.Errorf("%w: device ID must use bus/device/function", ErrInvalidPassthroughDevice)
	}

	for i, part := range p {
		n, err := strconv.Atoi(part)
		if err != nil {
			return parts, fmt.Errorf("%w: invalid device ID component: %v", ErrInvalidPassthroughDevice, err)
		}
		parts[i] = n
	}

	if parts[0] > 255 || parts[1] > 31 || parts[2] > 7 {
		return parts, fmt.Errorf("%w: PCI address is outside bus/device/function bounds", ErrInvalidPassthroughDevice)
	}

	return parts, nil
}

func parseDomain(domain string) (int, error) {
	intDomain, err := strconv.Atoi(strings.TrimSpace(domain))
	if err != nil {
		return 0, fmt.Errorf("%w: invalid domain number", ErrInvalidPassthroughDevice)
	}

	if intDomain < 0 || intDomain > 255 {
		return 0, fmt.Errorf("%w: domain number must be between 0 and 255", ErrInvalidPassthroughDevice)
	}

	if intDomain != 0 {
		return 0, fmt.Errorf("%w: only PCI domain 0 is supported", ErrUnsupportedPassthroughDomain)
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

func normalizedHostDriver(driver string) string {
	driver = strings.TrimSpace(driver)
	if driver == "" || strings.EqualFold(driver, "none") || strings.EqualFold(driver, "ppt") {
		return ""
	}
	return driver
}

func detachPCIDevice(pciAddr string) error {
	output, err := runPPTCommand("/usr/sbin/devctl", "detach", "-f", pciAddr)
	if err == nil {
		return nil
	}

	detail := strings.TrimSpace(output + " " + err.Error())
	if strings.Contains(detail, "Device not configured") {
		return nil
	}

	return fmt.Errorf("detaching %s failed (%s): %w", pciAddr, strings.TrimSpace(output), err)
}

func clearPCIDeviceDriver(pciAddr string) error {
	output, err := runPPTCommand("/usr/sbin/devctl", "clear", "driver", "-f", pciAddr)
	if err != nil {
		return fmt.Errorf("clearing driver for %s failed (%s): %w", pciAddr, strings.TrimSpace(output), err)
	}
	return nil
}

func setPCIDeviceDriver(pciAddr, driver string) error {
	output, err := runPPTCommand("/usr/sbin/devctl", "set", "driver", pciAddr, driver)
	if err != nil {
		return fmt.Errorf("setting driver %s for %s failed (%s): %w", driver, pciAddr, strings.TrimSpace(output), err)
	}
	return nil
}

func restorePCIDeviceDriver(pciAddr, oldDriver string) error {
	var restoreErrors []error
	if err := detachPCIDevice(pciAddr); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	if err := clearPCIDeviceDriver(pciAddr); err != nil {
		restoreErrors = append(restoreErrors, err)
	}

	oldDriver = normalizedHostDriver(oldDriver)
	if oldDriver == "" {
		output, err := runPPTCommand("/usr/sbin/devctl", "rescan", pciAddr)
		if err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("rescanning %s failed (%s): %w", pciAddr, strings.TrimSpace(output), err))
		}
	} else if err := setPCIDeviceDriver(pciAddr, oldDriver); err != nil {
		restoreErrors = append(restoreErrors, err)
	}

	return errors.Join(restoreErrors...)
}

func (s *Service) getPPTDeviceByAddress(id string) (*models.PassedThroughIDs, error) {
	var existing models.PassedThroughIDs
	err := s.DB.Where("device_id = ?", id).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking passthrough mapping for %s: %w", id, err)
	}
	return &existing, nil
}

func (s *Service) addLoaderPPTDevice(id string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	lines, perm, err := readLoaderConf()
	if err != nil {
		return err
	}

	ids := parsePPTIDsFromLoader(lines)
	if !slices.Contains(ids, id) {
		ids = append(ids, id)
		lines = rewriteLoaderPPTIDs(lines, ids)
	}

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
			logger.L.Warn().
				Int("domain", rec.Domain).
				Str("device_id", rec.DeviceID).
				Msg("Skipping loader.conf sync for unsupported PCI domain")
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

	pciDevices, err := getPCIDevicesOperation()
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
	if err := s.DB.Order("id ASC").Find(&ids).Error; err != nil {
		return nil, fmt.Errorf("loading PassedThroughIDs: %w", err)
	}
	return ids, nil
}

func (s *Service) AddPPTDevice(domain string, id string) (*models.PassedThroughIDs, error) {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	intDomain, err := parseDomain(domain)
	if err != nil {
		return nil, err
	}

	parts, err := parsePPTAddress(id)
	if err != nil {
		return nil, err
	}
	id = fmt.Sprintf("%d/%d/%d", parts[0], parts[1], parts[2])

	existing, err := s.getPPTDeviceByAddress(id)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: %s", ErrPassthroughDeviceAlreadyAdded, id)
	}

	pciDevices, err := getPCIDevicesOperation()
	if err != nil {
		return nil, fmt.Errorf("getting PCI devices: %w", err)
	}

	device, found := findPCIDeviceByDomainAndAddress(pciDevices, intDomain, parts)
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrPassthroughDeviceNotFound, id)
	}
	if strings.HasPrefix(strings.ToLower(device.Name), "ppt") {
		return nil, fmt.Errorf("%w: %s", ErrPassthroughDeviceNeedsImport, id)
	}

	oldDriver := normalizedHostDriver(device.Name)
	pciAddr := pciAddress(intDomain, parts)

	if err := detachPCIDevice(pciAddr); err != nil {
		return nil, err
	}
	if err := clearPCIDeviceDriver(pciAddr); err != nil {
		if rollbackErr := restorePCIDeviceDriver(pciAddr, oldDriver); rollbackErr != nil {
			return nil, fmt.Errorf("clearing the original driver failed (%v); restoring it also failed: %w", err, rollbackErr)
		}
		return nil, err
	}
	if err := setPCIDeviceDriver(pciAddr, "ppt"); err != nil {
		if rollbackErr := restorePCIDeviceDriver(pciAddr, oldDriver); rollbackErr != nil {
			return nil, fmt.Errorf("attaching ppt failed (%v); restoring the original driver also failed: %w", err, rollbackErr)
		}
		return nil, err
	}

	record := models.PassedThroughIDs{
		DeviceID:  id,
		Domain:    intDomain,
		OldDriver: oldDriver,
	}

	if err := s.DB.Create(&record).Error; err != nil {
		rollbackErr := restorePCIDeviceDriver(pciAddr, oldDriver)
		if rollbackErr != nil {
			return nil, fmt.Errorf("saving the passthrough mapping failed (%v); restoring the original driver also failed: %w", err, rollbackErr)
		}
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: %s", ErrPassthroughDeviceAlreadyAdded, id)
		}
		return nil, fmt.Errorf("saving passthrough mapping for %s: %w", id, err)
	}

	if err := s.addLoaderPPTDevice(id); err != nil {
		if rollbackErr := s.DB.Delete(&record).Error; rollbackErr != nil {
			return nil, fmt.Errorf("updating loader.conf failed (%v); removing the new database mapping also failed: %w", err, rollbackErr)
		}
		if rollbackErr := restorePCIDeviceDriver(pciAddr, oldDriver); rollbackErr != nil {
			return nil, fmt.Errorf("updating loader.conf failed (%v); restoring the original driver also failed: %w", err, rollbackErr)
		}
		return nil, fmt.Errorf("updating loader.conf for %s: %w", id, err)
	}

	return &record, nil
}

func (s *Service) PreparePPTDevice(domain string, id string) error {
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
	id = fmt.Sprintf("%d/%d/%d", parts[0], parts[1], parts[2])

	pciDevices, err := getPCIDevicesOperation()
	if err != nil {
		return fmt.Errorf("getting PCI devices: %w", err)
	}

	if _, found := findPCIDeviceByDomainAndAddress(pciDevices, intDomain, parts); !found {
		return fmt.Errorf("%w: %s", ErrPassthroughDeviceNotFound, id)
	}

	return s.addLoaderPPTDevice(id)
}

func (s *Service) ImportPPTDevice(domain string, id string) (*models.PassedThroughIDs, bool, error) {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	intDomain, err := parseDomain(domain)
	if err != nil {
		return nil, false, err
	}

	parts, err := parsePPTAddress(id)
	if err != nil {
		return nil, false, err
	}
	id = fmt.Sprintf("%d/%d/%d", parts[0], parts[1], parts[2])

	pciDevices, err := getPCIDevicesOperation()
	if err != nil {
		return nil, false, fmt.Errorf("getting PCI devices: %w", err)
	}

	device, found := findPCIDeviceByDomainAndAddress(pciDevices, intDomain, parts)
	if !found {
		return nil, false, fmt.Errorf("%w: %s", ErrPassthroughDeviceNotFound, id)
	}

	if !strings.HasPrefix(device.Name, "ppt") {
		return nil, false, fmt.Errorf("%w: %s", ErrPassthroughDeviceNotAttached, id)
	}

	existing, err := s.getPPTDeviceByAddress(id)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if existing.Domain != intDomain {
			return nil, false, fmt.Errorf("%w: %s", ErrPassthroughDeviceAlreadyAdded, id)
		}
		return existing, false, nil
	}

	record := models.PassedThroughIDs{
		DeviceID:  id,
		Domain:    intDomain,
		OldDriver: "",
	}

	if err := s.DB.Create(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, false, fmt.Errorf("%w: %s", ErrPassthroughDeviceAlreadyAdded, id)
		}
		return nil, false, fmt.Errorf("creating passthrough mapping for %s: %w", id, err)
	}

	if err := s.addLoaderPPTDevice(id); err != nil {
		if rollbackErr := s.DB.Delete(&record).Error; rollbackErr != nil {
			return nil, false, fmt.Errorf("updating loader.conf failed (%v); removing the imported database mapping also failed: %w", err, rollbackErr)
		}
		return nil, false, fmt.Errorf("updating loader.conf for %s: %w", id, err)
	}

	return &record, true, nil
}

func (s *Service) RemovePPTDevice(id uint) (bool, error) {
	s.achMutex.Lock()
	defer s.achMutex.Unlock()

	if id == 0 {
		return false, fmt.Errorf("%w: mapping ID must be positive", ErrInvalidPassthroughDevice)
	}

	var existing models.PassedThroughIDs
	if err := s.DB.Where("id = ?", id).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, fmt.Errorf("%w: mapping %d", ErrPassthroughDeviceNotFound, id)
		}
		return false, fmt.Errorf("checking passthrough mapping %d: %w", id, err)
	}

	var vms []vmModels.VM
	if err := s.DB.Find(&vms).Error; err != nil {
		return false, fmt.Errorf("checking VM passthrough assignments: %w", err)
	}

	for _, vm := range vms {
		if slices.Contains(vm.PCIDevices, existing.ID) {
			return false, fmt.Errorf("%w: mapping %d is assigned to VM %d", ErrPassthroughDeviceInUse, existing.ID, vm.RID)
		}
	}

	parts, err := parsePPTAddress(existing.DeviceID)
	if err != nil {
		return false, fmt.Errorf("stored passthrough mapping %d has an invalid device ID: %v", existing.ID, err)
	}

	pciAddr := pciAddress(existing.Domain, parts)

	if err := s.DB.Delete(&existing).Error; err != nil {
		return false, fmt.Errorf("deleting passthrough mapping for %s: %w", existing.DeviceID, err)
	}

	if err := s.removeLoaderPPTDevice(existing.DeviceID); err != nil {
		if rollbackErr := s.DB.Create(&existing).Error; rollbackErr != nil {
			return false, fmt.Errorf("removing %s from loader.conf failed (%v); restoring its database mapping also failed: %w", existing.DeviceID, err, rollbackErr)
		}

		return false, fmt.Errorf("removing %s from loader.conf: %w", existing.DeviceID, err)
	}

	if err := restorePCIDeviceDriver(pciAddr, existing.OldDriver); err != nil {
		logger.L.Warn().
			Err(err).
			Int("mapping_id", existing.ID).
			Str("device_id", existing.DeviceID).
			Str("pci_addr", pciAddr).
			Str("driver", existing.OldDriver).
			Msg("Passthrough mapping was removed persistently, but runtime driver restoration failed")
		return true, nil
	}

	return false, nil
}
