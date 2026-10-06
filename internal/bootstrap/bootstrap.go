// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bootstrap

/*
#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <unistd.h>

static int sylve_funlinkat(const char *path, int fd) {
	if (funlinkat(AT_FDCWD, path, fd, 0) == 0) {
		return 0;
	}
	return errno;
}
*/
import "C"

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	DefaultPath         = "/usr/local/etc/sylve/bootstrap.json"
	DefaultResolverPath = "/etc/resolv.conf"
	archiveSuffix       = ".applied"
	documentVersion     = 1
	maxDocumentBytes    = 1 << 20
)

var (
	errSourceReplaced = errors.New("bootstrap_source_replaced")
	errSourceModified = errors.New("bootstrap_source_modified")
)

type Report struct {
	Items           []systemServiceInterfaces.BootstrapItemResult `json:"items"`
	Archived        bool                                          `json:"archived"`
	RestartRequired bool                                          `json:"restartRequired"`
	DocumentError   string                                        `json:"documentError,omitempty"`
	ArchiveError    string                                        `json:"archiveError,omitempty"`
	GeneralError    string                                        `json:"generalError,omitempty"`
}

func (r Report) Failed() bool {
	if r.DocumentError != "" || r.ArchiveError != "" || r.GeneralError != "" {
		return true
	}
	for _, item := range r.Items {
		if item.Status == systemServiceInterfaces.BootstrapFailed {
			return true
		}
	}
	return false
}

func itemResult(kind string, index int, name string, status systemServiceInterfaces.BootstrapItemStatus, message string) systemServiceInterfaces.BootstrapItemResult {
	return systemServiceInterfaces.BootstrapItemResult{
		Kind:    kind,
		Index:   index,
		Name:    name,
		Status:  status,
		Message: message,
	}
}

func failedItem(kind string, index int, name, message string) systemServiceInterfaces.BootstrapItemResult {
	return itemResult(kind, index, name, systemServiceInterfaces.BootstrapFailed, message)
}

type NetworkApplier interface {
	NewStandardSwitch(networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error)
	CreateManualSwitch(name, bridge string) (*networkModels.ManualSwitch, error)
}

type openedSource interface {
	io.Reader
	io.Closer
	Stat() (os.FileInfo, error)
}

type Service struct {
	settings systemServiceInterfaces.BootstrapSettingsApplier
	network  NetworkApplier

	mutex            sync.Mutex
	startupAttempted bool

	startupPath  string
	resolverPath string

	lstatPath     func(string) (os.FileInfo, error)
	openPath      func(string) (openedSource, error)
	readResolver  func(string) ([]byte, error)
	linkPath      func(string, string) error
	renamePath    func(string, string) error
	writeResolver func(string, []byte, os.FileMode) error
	consumeSource func(string, int) error
}

func NewService(settings systemServiceInterfaces.BootstrapSettingsApplier, network NetworkApplier) *Service {
	return &Service{
		settings:      settings,
		network:       network,
		startupPath:   DefaultPath,
		resolverPath:  DefaultResolverPath,
		lstatPath:     os.Lstat,
		openPath:      openSourceFile,
		readResolver:  os.ReadFile,
		linkPath:      os.Link,
		renamePath:    os.Rename,
		writeResolver: utils.AtomicWriteFile,
		consumeSource: consumeVerifiedSource,
	}
}

func (s *Service) ApplyStartup(ctx context.Context) Report {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.startupAttempted {
		return Report{}
	}
	s.startupAttempted = true

	if _, err := s.lstatPath(s.startupPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Report{}
		}
		report := Report{Items: []systemServiceInterfaces.BootstrapItemResult{}}
		report.GeneralError = fmt.Sprintf("inspect %s: %v", s.startupPath, err)
		return report
	}

	return s.applyLocked(ctx, s.startupPath)
}

func (s *Service) Apply(ctx context.Context, path string) Report {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if path == "" {
		path = DefaultPath
	}
	return s.applyLocked(ctx, path)
}

func (s *Service) applyLocked(ctx context.Context, path string) Report {
	report := Report{Items: []systemServiceInterfaces.BootstrapItemResult{}}

	if err := ctx.Err(); err != nil {
		report.GeneralError = err.Error()
		return report
	}

	data, info, err := s.readSource(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			report.DocumentError = "bootstrap_document_not_found"
		} else {
			report.GeneralError = err.Error()
		}
		return report
	}

	doc, err := parseDocument(data)
	if err != nil {
		report.DocumentError = err.Error()
		return report
	}

	report.Items = append(report.Items, doc.localItems...)

	settingsItems, restartRequired, settingsErr := s.applySettings(ctx, doc)
	report.Items = append(report.Items, settingsItems...)
	report.RestartRequired = restartRequired

	report.Items = append(report.Items, s.applySwitches(ctx, doc)...)

	if settingsErr != nil {
		report.GeneralError = settingsErr.Error()
	}

	if len(doc.dns) > 0 {
		switch {
		case ctx.Err() != nil:
			report.GeneralError = ctx.Err().Error()
			report.Items = append(report.Items,
				itemResult("dns", 0, "", systemServiceInterfaces.BootstrapDeferred, "context_canceled"))
		case report.Failed():
			report.Items = append(report.Items,
				itemResult("dns", 0, "", systemServiceInterfaces.BootstrapDeferred, "deferred_due_to_item_failure"))
		default:
			if verifyErr := s.verifySource(path, data, info); verifyErr != nil {
				report.GeneralError = verifyErr.Error()
				report.Items = append(report.Items,
					itemResult("dns", 0, "", systemServiceInterfaces.BootstrapDeferred, "deferred_source_changed"))
			} else if dnsErr := s.applyDNS(ctx, doc.dns); dnsErr != nil {
				report.Items = append(report.Items,
					itemResult("dns", 0, "", systemServiceInterfaces.BootstrapFailed, dnsErr.Error()))
			} else {
				report.Items = append(report.Items,
					itemResult("dns", 0, "", systemServiceInterfaces.BootstrapApplied, ""))
			}
		}
	}
	sortItems(report.Items)

	if report.Failed() {
		return report
	}

	if err := ctx.Err(); err != nil {
		report.GeneralError = err.Error()
		return report
	}

	if archiveErr := s.archiveSource(path, data, info); archiveErr != nil {
		if errors.Is(archiveErr, errSourceReplaced) {
			report.GeneralError = archiveErr.Error()
		} else {
			report.ArchiveError = archiveErr.Error()
		}
		return report
	}
	report.Archived = true
	return report
}

func (s *Service) applySettings(ctx context.Context, doc *parsedDocument) ([]systemServiceInterfaces.BootstrapItemResult, bool, error) {
	wantsInitialized := doc.initialized != nil
	requestInitialized := wantsInitialized && *doc.initialized
	needsSettings := len(doc.pools) > 0 || len(doc.services) > 0 || requestInitialized

	var (
		result  systemServiceInterfaces.BootstrapSettingsResult
		callErr error
	)
	if needsSettings {
		if s.settings == nil {
			callErr = fmt.Errorf("settings_service_unavailable")
		} else {
			request := systemServiceInterfaces.BootstrapSettingsRequest{
				Initialized: requestInitialized,
				Pools:       doc.pools,
				Services:    doc.services,
			}
			result, callErr = s.settings.ApplyBootstrapSettings(ctx, request)
		}
	}

	items := make([]systemServiceInterfaces.BootstrapItemResult, 0, len(result.Items)+len(doc.pools)+len(doc.services)+1)
	poolOutcome := make([]bool, len(doc.pools))
	serviceOutcome := make([]bool, len(doc.services))
	initializedOutcome := false

	for _, item := range result.Items {
		mapped := item
		switch item.Kind {
		case "service":
			if item.Index >= 0 && item.Index < len(doc.serviceIdx) {
				mapped.Index = doc.serviceIdx[item.Index]
				serviceOutcome[item.Index] = true
			}
		case "pool":
			if item.Index >= 0 && item.Index < len(doc.poolIdx) {
				mapped.Index = doc.poolIdx[item.Index]
				poolOutcome[item.Index] = true
			}
		case "initialized":
			initializedOutcome = true
		}
		items = append(items, mapped)
	}

	if callErr != nil {
		for index, pool := range doc.pools {
			if !poolOutcome[index] {
				items = append(items, failedItem("pool", doc.poolIdx[index], pool, callErr.Error()))
			}
		}
		for index, service := range doc.services {
			if !serviceOutcome[index] {
				items = append(items, failedItem("service", doc.serviceIdx[index], string(service), callErr.Error()))
			}
		}
		if requestInitialized && !initializedOutcome {
			items = append(items, failedItem("initialized", 0, "initialized", callErr.Error()))
		}
	}

	if wantsInitialized && !requestInitialized && !initializedOutcome {
		items = append(items, itemResult("initialized", 0, "initialized", systemServiceInterfaces.BootstrapApplied, "noop"))
	}

	return items, result.RestartRequired, callErr
}

func (s *Service) applySwitches(ctx context.Context, doc *parsedDocument) []systemServiceInterfaces.BootstrapItemResult {
	items := make([]systemServiceInterfaces.BootstrapItemResult, 0, len(doc.switches))
	for _, spec := range doc.switches {
		if ctx.Err() != nil {
			items = append(items, failedItem("switch", spec.index, spec.name, "context_canceled"))
			continue
		}
		if s.network == nil {
			items = append(items, failedItem("switch", spec.index, spec.name, "network_service_unavailable"))
			continue
		}

		var err error
		switch spec.kind {
		case "standard":
			_, err = s.network.NewStandardSwitch(consoleprotocol.StandardSwitchServiceRequest(spec.standard))
		case "manual":
			_, err = s.network.CreateManualSwitch(spec.manual.Name, spec.manual.Bridge)
		}

		if err != nil {
			items = append(items, failedItem("switch", spec.index, spec.name, err.Error()))
			continue
		}
		items = append(items, itemResult("switch", spec.index, spec.name, systemServiceInterfaces.BootstrapApplied, ""))
	}
	return items
}

func (s *Service) applyDNS(ctx context.Context, servers []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	path := s.resolverPath

	var existing []byte
	data, err := s.readResolver(path)
	switch {
	case err == nil:
		existing = data
	case errors.Is(err, fs.ErrNotExist):
		existing = nil
	default:
		return fmt.Errorf("read resolver: %w", err)
	}

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.writeResolver(path, rewriteResolver(existing, servers), mode); err != nil {
		return fmt.Errorf("write resolver: %w", err)
	}
	return nil
}

func (s *Service) archiveSource(path string, data []byte, info os.FileInfo) error {
	archivePath := path + archiveSuffix

	if err := s.verifySource(path, data, info); err != nil {
		return fmt.Errorf("%w: %v", errSourceReplaced, err)
	}

	var claim string
	var claimErr error
	for range 8 {
		claim = archivePath + ".claim-" + rand.Text()
		claimErr = s.linkPath(path, claim)
		if !errors.Is(claimErr, fs.ErrExist) {
			break
		}
	}
	if claimErr != nil {
		return fmt.Errorf("claim source for archive: %w", claimErr)
	}
	cleanupClaim := func() { _ = os.Remove(claim) }

	claimInfo, err := s.lstatPath(claim)
	if err != nil || !claimInfo.Mode().IsRegular() || !os.SameFile(info, claimInfo) {
		cleanupClaim()
		return fmt.Errorf("%w: source replaced before archive", errSourceReplaced)
	}

	if err := s.verifySource(path, data, info); err != nil {
		cleanupClaim()
		return fmt.Errorf("%w: %v", errSourceReplaced, err)
	}

	if err := s.renamePath(claim, archivePath); err != nil {
		cleanupClaim()
		return err
	}

	archiveFile, err := os.OpenFile(archivePath, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer archiveFile.Close()

	archiveInfo, err := archiveFile.Stat()
	if err != nil {
		return fmt.Errorf("verify archive: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() || !os.SameFile(info, archiveInfo) {
		return fmt.Errorf("verify archive: archived entry is not the verified source")
	}
	archiveData, err := readBounded(archiveFile, maxDocumentBytes)
	if err != nil {
		return fmt.Errorf("verify archive: %w", err)
	}
	if !bytes.Equal(data, archiveData) {
		return fmt.Errorf("verify archive: archived bytes changed")
	}

	if err := s.consumeSource(path, int(archiveFile.Fd())); err != nil {
		if errors.Is(err, syscall.EDEADLK) {
			return fmt.Errorf("%w: source replaced before unlink", errSourceReplaced)
		}
		return fmt.Errorf("consume verified source: %w", err)
	}
	return nil
}

func consumeVerifiedSource(path string, fd int) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	if errno := C.sylve_funlinkat(cpath, C.int(fd)); errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}

func (s *Service) readSource(path string) ([]byte, os.FileInfo, error) {
	snapshot, err := s.lstatPath(path)
	if err != nil {
		return nil, nil, err
	}
	if !snapshot.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("bootstrap_source_not_regular_file")
	}
	if snapshot.Size() > maxDocumentBytes {
		return nil, nil, fmt.Errorf("bootstrap_source_too_large")
	}

	handle, err := s.openPath(path)
	if err != nil {
		return nil, nil, err
	}
	defer handle.Close()

	opened, err := handle.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("bootstrap_source_not_regular_file")
	}
	if !os.SameFile(snapshot, opened) {
		return nil, nil, fmt.Errorf("%w: replaced before open", errSourceReplaced)
	}
	if opened.Size() > maxDocumentBytes {
		return nil, nil, fmt.Errorf("bootstrap_source_too_large")
	}

	data, err := readBounded(handle, maxDocumentBytes)
	if err != nil {
		return nil, nil, err
	}

	after, err := handle.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, nil, fmt.Errorf("%w: changed during read", errSourceModified)
	}

	current, err := s.lstatPath(path)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(snapshot, current) {
		return nil, nil, fmt.Errorf("%w: path replaced during read", errSourceReplaced)
	}

	return data, opened, nil
}

func openSourceFile(path string) (openedSource, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}

func (s *Service) verifySource(path string, original []byte, info os.FileInfo) error {
	current, currentInfo, err := s.readSource(path)
	if err != nil {
		return fmt.Errorf("%w: %v", errSourceReplaced, err)
	}
	if info != nil && !os.SameFile(info, currentInfo) {
		return errSourceReplaced
	}
	if !bytes.Equal(original, current) {
		return errSourceModified
	}
	return nil
}

func sortItems(items []systemServiceInterfaces.BootstrapItemResult) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := kindRank(items[i].Kind), kindRank(items[j].Kind)
		if left != right {
			return left < right
		}
		return items[i].Index < items[j].Index
	})
}

func kindRank(kind string) int {
	switch kind {
	case "initialized":
		return 0
	case "service":
		return 1
	case "pool":
		return 2
	case "switch":
		return 3
	case "dns":
		return 4
	default:
		return 5
	}
}

type parsedDocument struct {
	initialized *bool
	services    []models.AvailableService
	serviceIdx  []int
	pools       []string
	poolIdx     []int
	dns         []string
	switches    []switchSpec
	localItems  []systemServiceInterfaces.BootstrapItemResult
}

type switchSpec struct {
	index    int
	kind     string
	name     string
	standard consoleprotocol.StandardSwitchCreateRequest
	manual   consoleprotocol.ManualSwitchCreateRequest
}

func parseDocument(data []byte) (*parsedDocument, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("bootstrap_document_empty")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("bootstrap_document_root_not_object")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return nil, fmt.Errorf("bootstrap_document_invalid_json")
	}
	versionRaw, ok := root["version"]
	if !ok {
		return nil, fmt.Errorf("bootstrap_document_missing_version")
	}
	var version int
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return nil, fmt.Errorf("bootstrap_document_invalid_version")
	}
	if version != documentVersion {
		return nil, fmt.Errorf("bootstrap_document_unsupported_version")
	}

	doc := &parsedDocument{}
	if initializedRaw, ok := root["initialized"]; ok {
		var initialized bool
		if isJSONNull(initializedRaw) || json.Unmarshal(initializedRaw, &initialized) != nil {
			return nil, fmt.Errorf("bootstrap_document_invalid_initialized")
		}
		doc.initialized = &initialized
	}
	if dnsRaw, ok := root["dns"]; ok {
		dns, err := decodeArray(dnsRaw)
		if err != nil {
			return nil, fmt.Errorf("bootstrap_document_invalid_dns")
		}
		for _, element := range dns {
			var address string
			if err := json.Unmarshal(element, &address); err != nil {
				return nil, fmt.Errorf("bootstrap_document_invalid_dns_entry")
			}
			address = strings.TrimSpace(address)
			if address == "" || net.ParseIP(address) == nil {
				return nil, fmt.Errorf("bootstrap_document_invalid_dns_address")
			}
			doc.dns = append(doc.dns, address)
		}
	}
	if servicesRaw, ok := root["services"]; ok {
		services, err := decodeArray(servicesRaw)
		if err != nil {
			return nil, fmt.Errorf("bootstrap_document_invalid_services")
		}
		for index, element := range services {
			var value string
			if err := json.Unmarshal(element, &value); err != nil {
				doc.localItems = append(doc.localItems, failedItem("service", index, "", "invalid_service_entry"))
				continue
			}
			value = strings.TrimSpace(value)
			service := models.AvailableService(value)
			if !models.IsAvailableService(service) {
				doc.localItems = append(doc.localItems, failedItem("service", index, value, "unsupported_service"))
				continue
			}
			doc.services = append(doc.services, service)
			doc.serviceIdx = append(doc.serviceIdx, index)
		}
	}
	if poolsRaw, ok := root["pools"]; ok {
		pools, err := decodeArray(poolsRaw)
		if err != nil {
			return nil, fmt.Errorf("bootstrap_document_invalid_pools")
		}
		for index, element := range pools {
			var value string
			if json.Unmarshal(element, &value) != nil || strings.TrimSpace(value) == "" {
				doc.localItems = append(doc.localItems, failedItem("pool", index, "", "invalid_pool_entry"))
				continue
			}
			doc.pools = append(doc.pools, strings.TrimSpace(value))
			doc.poolIdx = append(doc.poolIdx, index)
		}
	}
	if switchesRaw, ok := root["switches"]; ok {
		switches, err := decodeArray(switchesRaw)
		if err != nil {
			return nil, fmt.Errorf("bootstrap_document_invalid_switches")
		}
		for index, element := range switches {
			spec, err := parseSwitchEntry(index, element)
			if err != nil {
				doc.localItems = append(doc.localItems, failedItem("switch", index, "", err.Error()))
				continue
			}
			doc.switches = append(doc.switches, spec)
		}
	}
	return doc, nil
}

func parseSwitchEntry(index int, raw json.RawMessage) (switchSpec, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return switchSpec{}, fmt.Errorf("invalid_switch_entry")
	}
	if _, ok := fields["json"]; ok {
		return switchSpec{}, fmt.Errorf("switch_display_only_json_forbidden")
	}
	typeRaw, ok := fields["type"]
	if !ok {
		return switchSpec{}, fmt.Errorf("switch_type_required")
	}
	var switchType string
	if err := json.Unmarshal(typeRaw, &switchType); err != nil {
		return switchSpec{}, fmt.Errorf("invalid_switch_type")
	}
	switchType = strings.ToLower(strings.TrimSpace(switchType))
	_, standardKey := fields["standard"]
	_, manualKey := fields["manual"]
	spec := switchSpec{index: index, kind: switchType}
	switch switchType {
	case "standard":
		if !standardKey || isJSONNull(fields["standard"]) || manualKey {
			return switchSpec{}, fmt.Errorf("switch_payload_type_mismatch")
		}
		var request consoleprotocol.StandardSwitchCreateRequest
		if err := json.Unmarshal(fields["standard"], &request); err != nil {
			return switchSpec{}, fmt.Errorf("invalid_standard_switch_entry")
		}
		request.Name = strings.TrimSpace(request.Name)
		if request.Name == "" || request.MTU < 0 || request.VLAN < 0 {
			return switchSpec{}, fmt.Errorf("invalid_standard_switch_entry")
		}
		for portIndex := range request.Ports {
			request.Ports[portIndex] = strings.TrimSpace(request.Ports[portIndex])
			if request.Ports[portIndex] == "" {
				return switchSpec{}, fmt.Errorf("invalid_standard_switch_ports")
			}
		}
		spec.name, spec.standard = request.Name, request
	case "manual":
		if !manualKey || isJSONNull(fields["manual"]) || standardKey {
			return switchSpec{}, fmt.Errorf("switch_payload_type_mismatch")
		}
		var request consoleprotocol.ManualSwitchCreateRequest
		if err := json.Unmarshal(fields["manual"], &request); err != nil {
			return switchSpec{}, fmt.Errorf("invalid_manual_switch_entry")
		}
		request.Name = strings.TrimSpace(request.Name)
		request.Bridge = strings.TrimSpace(request.Bridge)
		if request.Name == "" || request.Bridge == "" {
			return switchSpec{}, fmt.Errorf("invalid_manual_switch_entry")
		}
		spec.name, spec.manual = request.Name, request
	default:
		return switchSpec{}, fmt.Errorf("invalid_switch_type")
	}
	return spec, nil
}

func decodeArray(raw []byte) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("expected_array")
	}
	return values, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("bootstrap_source_too_large")
	}
	return data, nil
}

func rewriteResolver(existing []byte, servers []string) []byte {
	var lines []string
	if len(existing) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(existing), "\n"), "\n")
	}
	output := make([]string, 0, len(lines)+len(servers))
	inserted := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "nameserver" {
			if !inserted {
				for _, server := range servers {
					output = append(output, "nameserver "+server)
				}
				inserted = true
			}
			continue
		}
		output = append(output, line)
	}
	if !inserted {
		for _, server := range servers {
			output = append(output, "nameserver "+server)
		}
	}
	result := strings.Join(output, "\n")
	if len(output) > 0 {
		result += "\n"
	}
	return []byte(result)
}
