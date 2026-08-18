// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/internal/db/models"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/alchemillahq/sylve/pkg/network/mdns"
	"gorm.io/gorm"
)

var _ mdnsInterfaces.MdnsServiceInterface = (*Service)(nil)

var (
	ErrInvalidRecord    = errors.New("invalid mDNS record")
	ErrInvalidSettings  = errors.New("invalid mDNS settings")
	ErrRecordNotFound   = errors.New("mDNS record not found")
	ErrRecordConflict   = errors.New("mDNS record conflicts with an existing record")
	recordTypePattern   = regexp.MustCompile(`^_[a-z0-9-]+\._(tcp|udp)$`)
	mdnsInterfaceByName = net.InterfaceByName
)

type Service struct {
	DB               *gorm.DB
	mu               sync.Mutex
	responder        dnssd.Responder
	responderFactory func() (dnssd.Responder, error)
	handles          []dnssd.ServiceHandle
	cancelFunc       context.CancelFunc
	wg               sync.WaitGroup
	activeState      *mdnsActivationState
}

type mdnsActivationState struct {
	enabled  bool
	records  []mdnsInterfaces.MdnsRecordWithManaged
	settings mdnsModels.MdnsSettings
}

func cloneActivationState(state mdnsActivationState) mdnsActivationState {
	cloned := state
	cloned.records = append([]mdnsInterfaces.MdnsRecordWithManaged(nil), state.records...)
	return cloned
}

func NewService(db *gorm.DB) mdnsInterfaces.MdnsServiceInterface {
	return &Service{DB: db}
}

func mdnsEnabled(db *gorm.DB) (bool, error) {
	var basic models.BasicSettings
	if err := db.First(&basic).Error; err != nil {
		return false, fmt.Errorf("failed to load basic settings: %w", err)
	}
	for _, svc := range basic.Services {
		if svc == models.Mdns {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) Rebuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebuildLocked()
}

func (s *Service) rebuildLocked() error {
	next, err := s.loadActivationState(s.DB)
	if err != nil {
		return err
	}

	changed, err := s.activateStateLocked(next)
	if err != nil {
		if changed && s.activeState != nil {
			_, restoreErr := s.activateStateLocked(cloneActivationState(*s.activeState))
			if restoreErr != nil {
				s.activeState = nil
				return errors.Join(err, fmt.Errorf("failed to restore previous mdns responder: %w", restoreErr))
			}
		}
		return err
	}

	active := cloneActivationState(next)
	s.activeState = &active
	return nil
}

func (s *Service) loadActivationState(db *gorm.DB) (mdnsActivationState, error) {
	enabled, err := mdnsEnabled(db)
	if err != nil {
		return mdnsActivationState{}, err
	}
	state := mdnsActivationState{enabled: enabled}
	if !enabled {
		return state, nil
	}

	managed, err := s.gatherManagedRecords(db)
	if err != nil {
		return mdnsActivationState{}, fmt.Errorf("failed to gather managed records: %w", err)
	}
	user, err := s.userRecords(db)
	if err != nil {
		return mdnsActivationState{}, fmt.Errorf("failed to load user records: %w", err)
	}
	settings, err := getSettings(db)
	if err != nil {
		return mdnsActivationState{}, fmt.Errorf("failed to load mdns settings: %w", err)
	}

	state.records = append(managed, user...)
	state.settings = settings
	return state, nil
}

func (s *Service) activateStateLocked(state mdnsActivationState) (bool, error) {
	if !state.enabled || len(state.records) == 0 {
		return s.unpublishLocked()
	}
	return s.publishLocked(state.records, state.settings)
}

func (s *Service) gatherManagedRecords(db *gorm.DB) ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	var records []mdnsInterfaces.MdnsRecordWithManaged
	var basicSettings models.BasicSettings
	if err := db.First(&basicSettings).Error; err != nil {
		return nil, err
	}

	sambaEnabled := false
	for _, service := range basicSettings.Services {
		if service == models.SambaServer {
			sambaEnabled = true
			break
		}
	}
	if !sambaEnabled {
		return records, nil
	}

	host, _ := os.Hostname()

	var sambaSettings sambaModels.SambaSettings
	if err := db.First(&sambaSettings).Error; err != nil {
		sambaSettings.AppleExtensions = false
	}
	if !sambaSettings.AdvertiseMdns {
		return records, nil
	}

	var shares []sambaModels.SambaShare
	if err := db.Where("enabled = ?", true).Order("name ASC").Find(&shares).Error; err != nil {
		return nil, err
	}

	hasShares := len(shares) > 0

	if hasShares {
		records = append(records, mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: mdnsModels.MdnsRecord{
				Name: host,
				Type: "_smb._tcp",
				Port: 445,
				Txt:  map[string]string{},
			},
			Managed: true,
			Source:  "samba",
		})
	}

	if hasShares && sambaSettings.AppleExtensions {
		records = append(records, mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: mdnsModels.MdnsRecord{
				Name: host,
				Type: "_device-info._tcp",
				Port: 9,
				Txt:  map[string]string{"model": "RackMac"},
			},
			Managed: true,
			Source:  "samba",
		})

		var tmShares []string
		for _, share := range shares {
			if share.TimeMachine {
				tmShares = append(tmShares, share.Name)
			}
		}
		if len(tmShares) > 0 {
			txt := map[string]string{
				"sys": "waMa=0,adVF=0x100",
			}
			for i, name := range tmShares {
				txt[fmt.Sprintf("dk%d", i)] = fmt.Sprintf("adVN=%s,adVF=0x82", name)
			}
			records = append(records, mdnsInterfaces.MdnsRecordWithManaged{
				MdnsRecord: mdnsModels.MdnsRecord{
					Name: host,
					Type: "_adisk._tcp",
					Port: 9,
					Txt:  txt,
				},
				Managed: true,
				Source:  "samba",
			})
		}
	}

	return records, nil
}

func (s *Service) userRecords(db *gorm.DB) ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	var rows []mdnsModels.MdnsRecord
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]mdnsInterfaces.MdnsRecordWithManaged, len(rows))
	for i, r := range rows {
		out[i] = mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: r,
			Managed:    false,
			Source:     "user",
		}
	}
	return out, nil
}

type responderWithReadiness interface {
	RespondReady(context.Context, chan<- error) error
}

func buildMdnsServices(
	records []mdnsInterfaces.MdnsRecordWithManaged,
	settings mdnsModels.MdnsSettings,
) ([]dnssd.Service, error) {
	host, _ := os.Hostname()
	if settings.Hostname != "" {
		host = settings.Hostname
	}

	var ifaces []string
	if settings.Interfaces != "" {
		for _, iface := range strings.Split(settings.Interfaces, ",") {
			iface = strings.TrimSpace(iface)
			if iface != "" {
				ifaces = append(ifaces, iface)
			}
		}
	}

	seen := map[string]bool{}
	var services []dnssd.Service
	for _, r := range records {
		key := recordIdentity(r.Name, r.Type)
		if seen[key] {
			logger.L.Warn().Str("name", r.Name).Str("type", r.Type).Msg("duplicate mdns record, skipping")
			continue
		}
		seen[key] = true

		recordIfaces := ifaces
		if r.Interfaces != "" {
			recordIfaces = nil
			for _, ri := range strings.Split(r.Interfaces, ",") {
				ri = strings.TrimSpace(ri)
				if ri != "" {
					recordIfaces = append(recordIfaces, ri)
				}
			}
		}

		port := r.Port
		if port == 0 {
			port = 9
		}

		service, err := dnssd.NewService(dnssd.Config{
			Name:   r.Name,
			Type:   r.Type,
			Domain: "local",
			Host:   host,
			Port:   port,
			Text:   r.Txt,
			Ifaces: recordIfaces,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prepare mdns service %q of type %q: %w", r.Name, r.Type, err)
		}
		services = append(services, service)
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("no valid mdns records to publish")
	}
	return services, nil
}

func (s *Service) hasResponderLocked() bool {
	return s.responder != nil || s.cancelFunc != nil || len(s.handles) > 0
}

func (s *Service) publishLocked(
	records []mdnsInterfaces.MdnsRecordWithManaged,
	settings mdnsModels.MdnsSettings,
) (bool, error) {
	services, err := buildMdnsServices(records, settings)
	if err != nil {
		return false, err
	}

	hadResponder := s.hasResponderLocked()
	s.stopResponderLocked()

	factory := s.responderFactory
	if factory == nil {
		factory = dnssd.NewResponder
	}
	rp, err := factory()
	if err != nil {
		return hadResponder, fmt.Errorf("failed to create responder: %w", err)
	}

	handles := make([]dnssd.ServiceHandle, 0, len(services))
	for _, service := range services {
		handle, err := rp.Add(service)
		if err != nil {
			rp.Close()
			return hadResponder, fmt.Errorf("failed to add mdns service %q of type %q: %w", service.Name, service.Type, err)
		}
		handles = append(handles, handle)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 1)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		var respondErr error
		if readyResponder, ok := rp.(responderWithReadiness); ok {
			respondErr = readyResponder.RespondReady(ctx, ready)
		} else {
			ready <- nil
			respondErr = rp.Respond(ctx)
		}
		if respondErr != nil && ctx.Err() == nil {
			logger.L.Warn().Err(respondErr).Msg("mdns responder exited with error")
		}
	}()

	if err := <-ready; err != nil {
		cancel()
		s.wg.Wait()
		rp.Close()
		return hadResponder, fmt.Errorf("failed to start mdns responder: %w", err)
	}

	s.responder = rp
	s.handles = handles
	s.cancelFunc = cancel
	return true, nil
}

func (s *Service) unpublishLocked() (bool, error) {
	changed := s.hasResponderLocked()
	s.stopResponderLocked()
	return changed, nil
}

func (s *Service) stopResponderLocked() {
	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
		s.wg.Wait()
	}

	if s.responder != nil {
		s.responder.Close()
		s.responder = nil
	}
	s.handles = nil
}

func (s *Service) GetSettings() (mdnsModels.MdnsSettings, error) {
	return getSettings(s.DB)
}

func getSettings(db *gorm.DB) (mdnsModels.MdnsSettings, error) {
	var settings mdnsModels.MdnsSettings
	if err := db.First(&settings).Error; err != nil {
		return settings, fmt.Errorf("failed to get mdns settings: %w", err)
	}
	return settings, nil
}

func (s *Service) restoreMutationStateLocked(
	previous mdnsActivationState,
	changed bool,
	primaryErr error,
) error {
	if !changed {
		return primaryErr
	}

	if _, err := s.activateStateLocked(previous); err != nil {
		s.activeState = nil
		return errors.Join(
			primaryErr,
			fmt.Errorf("failed to restore previous mdns responder: %w", err),
		)
	}

	restored := cloneActivationState(previous)
	s.activeState = &restored
	return primaryErr
}

func (s *Service) applyMutationLocked(
	persistedBefore mdnsActivationState,
	desired *mdnsActivationState,
	persist func(*gorm.DB) error,
) error {
	previousActive := cloneActivationState(persistedBefore)
	if s.activeState != nil {
		previousActive = cloneActivationState(*s.activeState)
	}

	changed, err := s.activateStateLocked(*desired)
	if err != nil {
		return s.restoreMutationStateLocked(previousActive, changed, err)
	}

	if err := s.DB.Transaction(persist); err != nil {
		return s.restoreMutationStateLocked(previousActive, changed, err)
	}

	active := cloneActivationState(*desired)
	s.activeState = &active
	return nil
}

func (s *Service) SetSettings(interfaces, hostname string) error {
	if err := validateSettingsInput(interfaces, hostname); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	settings, err := getSettings(s.DB)
	if err != nil {
		return err
	}
	previous, err := s.loadActivationState(s.DB)
	if err != nil {
		return err
	}

	settings.Interfaces = interfaces
	settings.Hostname = hostname
	desired := cloneActivationState(previous)
	if desired.enabled {
		desired.settings = settings
	}

	return s.applyMutationLocked(previous, &desired, func(tx *gorm.DB) error {
		if err := tx.Save(&settings).Error; err != nil {
			return fmt.Errorf("failed to save mdns settings: %w", err)
		}
		return nil
	})
}

func (s *Service) GetRecords() ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	managed, err := s.gatherManagedRecords(s.DB)
	if err != nil {
		return nil, err
	}
	user, err := s.userRecords(s.DB)
	if err != nil {
		return nil, err
	}
	records := append(managed, user...)
	active := make(map[string]struct{})
	if s.activeState != nil && s.activeState.enabled {
		for _, record := range s.activeState.records {
			active[recordIdentity(record.Name, record.Type)] = struct{}{}
		}
	}
	reported := make(map[string]struct{})
	for i := range records {
		identity := recordIdentity(records[i].Name, records[i].Type)
		_, isActive := active[identity]
		_, alreadyReported := reported[identity]
		records[i].Active = isActive && !alreadyReported
		if records[i].Active {
			reported[identity] = struct{}{}
		}
	}
	return records, nil
}

func recordIdentity(name, recordType string) string {
	return strings.ToLower(name) + "\x00" + strings.ToLower(recordType)
}

func (s *Service) ensureRecordIdentityAvailable(db *gorm.DB, excludeID uint, name, recordType string) error {
	query := db.Model(&mdnsModels.MdnsRecord{}).
		Where("LOWER(name) = LOWER(?) AND LOWER(type) = LOWER(?)", name, recordType)
	if excludeID != 0 {
		query = query.Where("id <> ?", excludeID)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check mdns record identity: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%w: name %q and type %q already exist", ErrRecordConflict, name, recordType)
	}

	managed, err := s.gatherManagedRecords(db)
	if err != nil {
		return fmt.Errorf("failed to check managed mdns record identities: %w", err)
	}
	for _, record := range managed {
		if recordIdentity(record.Name, record.Type) == recordIdentity(name, recordType) {
			return fmt.Errorf(
				"%w: name %q and type %q are managed by %s",
				ErrRecordConflict,
				name,
				recordType,
				record.Source,
			)
		}
	}

	return nil
}

func (s *Service) CreateRecord(name, recordType string, port int, txt map[string]string, interfaces string) (mdnsModels.MdnsRecord, error) {
	if err := validateRecordInput(name, recordType, port, txt, interfaces); err != nil {
		return mdnsModels.MdnsRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRecordIdentityAvailable(s.DB, 0, name, recordType); err != nil {
		return mdnsModels.MdnsRecord{}, err
	}
	previous, err := s.loadActivationState(s.DB)
	if err != nil {
		return mdnsModels.MdnsRecord{}, err
	}

	record := mdnsModels.MdnsRecord{
		Name:       name,
		Type:       recordType,
		Port:       port,
		Txt:        txt,
		Interfaces: interfaces,
	}
	desired := cloneActivationState(previous)
	if desired.enabled {
		desired.records = append(desired.records, mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: record,
			Managed:    false,
			Source:     "user",
		})
	}

	if err := s.applyMutationLocked(previous, &desired, func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: name %q and type %q already exist", ErrRecordConflict, name, recordType)
			}
			return fmt.Errorf("failed to create mdns record: %w", err)
		}
		if desired.enabled {
			desired.records[len(desired.records)-1].MdnsRecord = record
		}
		return nil
	}); err != nil {
		return mdnsModels.MdnsRecord{}, err
	}
	return record, nil
}

func (s *Service) UpdateRecord(id uint, name, recordType string, port int, txt map[string]string, interfaces string) error {
	if err := validateRecordInput(name, recordType, port, txt, interfaces); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var record mdnsModels.MdnsRecord
	if err := s.DB.First(&record, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: id %d", ErrRecordNotFound, id)
		}
		return fmt.Errorf("failed to get mdns record %d: %w", id, err)
	}
	if err := s.ensureRecordIdentityAvailable(s.DB, id, name, recordType); err != nil {
		return err
	}
	previous, err := s.loadActivationState(s.DB)
	if err != nil {
		return err
	}

	record.Name = name
	record.Type = recordType
	record.Port = port
	record.Txt = txt
	record.Interfaces = interfaces

	desired := cloneActivationState(previous)
	if desired.enabled {
		found := false
		for i := range desired.records {
			if !desired.records[i].Managed && desired.records[i].ID == id {
				desired.records[i].MdnsRecord = record
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("failed to prepare mdns record %d update: record missing from activation state", id)
		}
	}

	return s.applyMutationLocked(previous, &desired, func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: name %q and type %q already exist", ErrRecordConflict, name, recordType)
			}
			return fmt.Errorf("failed to update mdns record: %w", err)
		}
		if desired.enabled {
			for i := range desired.records {
				if !desired.records[i].Managed && desired.records[i].ID == id {
					desired.records[i].MdnsRecord = record
					break
				}
			}
		}
		return nil
	})
}

func (s *Service) DeleteRecord(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var record mdnsModels.MdnsRecord
	if err := s.DB.First(&record, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: id %d", ErrRecordNotFound, id)
		}
		return fmt.Errorf("failed to get mdns record %d: %w", id, err)
	}
	previous, err := s.loadActivationState(s.DB)
	if err != nil {
		return err
	}

	desired := cloneActivationState(previous)
	if desired.enabled {
		filtered := desired.records[:0]
		found := false
		for _, activeRecord := range desired.records {
			if !activeRecord.Managed && activeRecord.ID == id {
				found = true
				continue
			}
			filtered = append(filtered, activeRecord)
		}
		if !found {
			return fmt.Errorf("failed to prepare mdns record %d deletion: record missing from activation state", id)
		}
		desired.records = filtered
	}

	return s.applyMutationLocked(previous, &desired, func(tx *gorm.DB) error {
		result := tx.Delete(&record)
		if result.Error != nil {
			return fmt.Errorf("failed to delete mdns record: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: id %d", ErrRecordNotFound, id)
		}
		return nil
	})
}

func validateRecordInput(name, recordType string, port int, txt map[string]string, interfaces string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRecord)
	}
	if !utf8.ValidString(name) || len([]byte(name)) > 63 || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("%w: name must be valid UTF-8, at most 63 bytes, and contain no control line breaks", ErrInvalidRecord)
	}
	if !recordTypePattern.MatchString(recordType) {
		return fmt.Errorf("%w: invalid record type %q: must match _name._tcp or _name._udp", ErrInvalidRecord, recordType)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidRecord)
	}
	for key, value := range txt {
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
			return fmt.Errorf("%w: TXT keys must be non-empty and cannot contain '=' or control line breaks", ErrInvalidRecord)
		}
		if len([]byte(key+"="+value)) > 255 {
			return fmt.Errorf("%w: TXT entry %q exceeds 255 bytes", ErrInvalidRecord, key)
		}
	}
	if err := validateInterfaces(interfaces); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	return nil
}

func validateSettingsInput(interfaces, hostname string) error {
	if err := validateInterfaces(interfaces); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	if hostname == "" {
		return nil
	}
	if len(hostname) > 253 || strings.ContainsAny(hostname, "\x00\r\n") {
		return fmt.Errorf("%w: invalid target hostname", ErrInvalidSettings)
	}
	for _, label := range strings.Split(strings.TrimSuffix(hostname, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%w: invalid target hostname", ErrInvalidSettings)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("%w: invalid target hostname", ErrInvalidSettings)
			}
		}
	}
	return nil
}

func validateInterfaces(interfaces string) error {
	seen := make(map[string]struct{})
	for _, name := range strings.Split(interfaces, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		if _, err := mdnsInterfaceByName(name); err != nil {
			return fmt.Errorf("network interface %q does not exist", name)
		}
	}
	return nil
}
