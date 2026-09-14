// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package interfaceref

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"gorm.io/gorm"
)

var ErrFilteredStandardSwitchL2Only = errors.New("filtered_standard_switch_l2_only")

type Coordinator struct {
	mutex sync.RWMutex
}

func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

func (c *Coordinator) ReadLock() func() {
	if c == nil {
		return func() {}
	}
	c.mutex.RLock()
	return c.mutex.RUnlock
}

func (c *Coordinator) WriteLock() func() {
	if c == nil {
		return func() {}
	}
	c.mutex.Lock()
	return c.mutex.Unlock
}

func RejectFilteredStandardBridgeInterfaces(db *gorm.DB, interfaces ...string) error {
	if db == nil {
		return fmt.Errorf("db_not_initialized")
	}

	names := make([]string, 0, len(interfaces))
	seen := make(map[string]struct{}, len(interfaces))
	for _, value := range interfaces {
		name := strings.TrimSpace(value)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}

	var filtered []string
	if err := db.Model(&networkModels.StandardSwitch{}).
		Where("vlan_filtering = ? AND bridge_name IN ?", true, names).
		Pluck("bridge_name", &filtered).Error; err != nil {
		return fmt.Errorf("check filtered Standard Switch interfaces: %w", err)
	}
	if len(filtered) == 0 {
		return nil
	}

	sort.Strings(filtered)
	return fmt.Errorf("%w: %s", ErrFilteredStandardSwitchL2Only, strings.Join(filtered, ", "))
}
