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
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

const tunablesCacheTTL = 5 * time.Second

func managedStandardSwitchTunable(name string) (string, bool) {
	switch name {
	case models.SystemTunableBridgeInheritMACOID:
		return "standard switch MAC identity", true
	case models.SystemTunableIPv6RFC6204W3OID:
		return "standard switch IPv6 route ownership", true
	default:
		return "", false
	}
}

type TunablesResponse struct {
	LastPage int              `json:"last_page"`
	Data     []sysctl.Tunable `json:"data"`
}

// listTunables returns the full sysctl MIB, cached for a short window to keep
// remote pagination requests cheap.
func (s *Service) listTunables(force bool) ([]sysctl.Tunable, error) {
	s.tunMutex.Lock()
	defer s.tunMutex.Unlock()

	if !force && s.tunCache != nil && time.Since(s.tunCachedAt) < tunablesCacheTTL {
		return s.tunCache, nil
	}

	listFn := s.tunList
	if listFn == nil {
		listFn = sysctl.List
	}
	list, err := listFn()
	if err != nil {
		return nil, err
	}

	s.tunCache = list
	s.tunCachedAt = time.Now()

	return list, nil
}

func (s *Service) invalidateTunablesCache() {
	s.tunMutex.Lock()
	s.tunCache = nil
	s.tunMutex.Unlock()
}

func (s *Service) storedTunables() (map[string]string, error) {
	var rows []models.SystemTunable
	if err := s.DB.Find(&rows).Error; err != nil {
		return nil, err
	}

	stored := make(map[string]string, len(rows))
	for _, r := range rows {
		stored[r.Name] = r.Value
	}

	return stored, nil
}

func (s *Service) configuredTunables(stored map[string]string) ([]sysctl.Tunable, error) {
	describe := s.tunDescribe
	if describe == nil {
		describe = sysctl.Describe
	}

	configured := make([]sysctl.Tunable, 0, len(stored))
	for name, value := range stored {
		tunable, found, err := describe(name)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		tunable.Value = value
		if _, managed := managedStandardSwitchTunable(name); managed {
			tunable.Writable = false
		}
		configured = append(configured, tunable)
	}
	return configured, nil
}

// ListTunablesPaginated returns a filtered, sorted and paginated slice of the
// sysctl MIB, shaped to match the remote Tabulator contract.
func (s *Service) ListTunablesPaginated(page, size int, sortField, sortDir, search string, configuredOnly bool) (*TunablesResponse, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 25
	}

	stored, err := s.storedTunables()
	if err != nil {
		return nil, err
	}

	var all []sysctl.Tunable
	if configuredOnly {
		all, err = s.configuredTunables(stored)
	} else {
		all, err = s.listTunables(false)
	}
	if err != nil {
		return nil, err
	}

	filtered := make([]sysctl.Tunable, 0, len(all))
	needle := strings.ToLower(strings.TrimSpace(search))
	for _, t := range all {
		v, configured := stored[t.Name]
		if configured {
			t.Value = v
		}
		if _, managed := managedStandardSwitchTunable(t.Name); managed {
			t.Writable = false
		}
		if needle != "" && !strings.Contains(strings.ToLower(t.Name), needle) {
			continue
		}
		filtered = append(filtered, t)
	}

	desc := strings.EqualFold(sortDir, "desc")
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := i, j
		if desc {
			a, b = j, i
		}
		switch sortField {
		case "value":
			return filtered[a].Value < filtered[b].Value
		case "type":
			return filtered[a].Type < filtered[b].Type
		case "writable":
			return !filtered[a].Writable && filtered[b].Writable
		default:
			return filtered[a].Name < filtered[b].Name
		}
	})

	total := len(filtered)
	lastPage := total / size
	if total%size > 0 {
		lastPage++
	}
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		page = lastPage
	}

	offset := (page - 1) * size
	if offset > total {
		offset = total
	}
	end := offset + size
	if end > total {
		end = total
	}

	return &TunablesResponse{
		LastPage: lastPage,
		Data:     filtered[offset:end],
	}, nil
}

func (s *Service) findTunable(name string) (sysctl.Tunable, bool, error) {
	all, err := s.listTunables(false)
	if err != nil {
		return sysctl.Tunable{}, false, err
	}

	for _, t := range all {
		if t.Name == name {
			return t, true, nil
		}
	}

	return sysctl.Tunable{}, false, nil
}

func readTunableRuntime(name string) (string, error) {
	value, err := utils.RunCommand("/sbin/sysctl", "-n", name)
	if err != nil {
		return "", err
	}

	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	return value, nil
}

// applyTunableRuntime applies a value via sysctl(8), which handles type
// conversion and rejects read-only oids.
func applyTunableRuntime(name, value string) error {
	if _, err := utils.RunCommand("/sbin/sysctl", fmt.Sprintf("%s=%s", name, value)); err != nil {
		return err
	}

	return nil
}

func (s *Service) currentTunableRuntimeValue(name string) (string, error) {
	if s.tunRead != nil {
		return s.tunRead(name)
	}
	return readTunableRuntime(name)
}

func (s *Service) setTunableRuntimeValue(name, value string) error {
	if s.tunApply != nil {
		return s.tunApply(name, value)
	}
	return applyTunableRuntime(name, value)
}

// SetTunable validates that the oid is writable, applies it at runtime and
// persists it so it can be re-applied on the next boot.
func (s *Service) SetTunable(name, value string) error {
	s.tunMutationMutex.Lock()
	defer s.tunMutationMutex.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return ErrTunableNameRequired
	}
	if owner, managed := managedStandardSwitchTunable(name); managed {
		return fmt.Errorf("%w: %s is managed by %s", ErrTunableNotWritable, name, owner)
	}

	t, found, err := s.findTunable(name)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrTunableLookupFailed, name, err)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrTunableNotFound, name)
	}
	if !t.Writable {
		return fmt.Errorf("%w: %s", ErrTunableNotWritable, name)
	}

	previousValue, err := s.currentTunableRuntimeValue(name)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrTunableRuntimeReadFailed, name, err)
	}

	runtimeChanged := previousValue != value
	if runtimeChanged {
		if err := s.setTunableRuntimeValue(name, value); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidTunableValue, name, err)
		}
	}

	tunable := models.SystemTunable{Name: name, Value: value}
	if err := s.DB.Where(models.SystemTunable{Name: name}).
		Assign(map[string]any{"value": value}).
		FirstOrCreate(&tunable).Error; err != nil {
		var rollbackErr error
		if runtimeChanged {
			rollbackErr = s.setTunableRuntimeValue(name, previousValue)
			if rollbackErr != nil {
				s.invalidateTunablesCache()
			}
		}
		return fmt.Errorf("%w: %s: %v", ErrTunablePersistenceFailed, name, errors.Join(err, rollbackErr))
	}

	s.invalidateTunablesCache()

	return nil
}

// ReapplyStoredTunables re-applies every persisted tunable at startup. Failures
// are logged and skipped so one bad entry never blocks boot.
func (s *Service) ReapplyStoredTunables() error {
	s.tunMutationMutex.Lock()
	defer s.tunMutationMutex.Unlock()

	var rows []models.SystemTunable
	if err := s.DB.Find(&rows).Error; err != nil {
		return err
	}

	for _, r := range rows {
		if _, managed := managedStandardSwitchTunable(r.Name); managed {
			continue
		}
		if err := s.setTunableRuntimeValue(r.Name, r.Value); err != nil {
			logger.L.Error().Msgf("Error re-applying stored tunable %s=%s: %v", r.Name, r.Value, err)
		}
	}

	return nil
}
