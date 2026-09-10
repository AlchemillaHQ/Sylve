// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bridgevlan

import (
	"sort"
	"strings"
	"sync"
)

var standardSwitchLifecycleLocks sync.Map

func LockStandardSwitchLifecycle(bridgeNames ...string) func() {
	unique := make(map[string]struct{}, len(bridgeNames))
	for _, bridgeName := range bridgeNames {
		bridgeName = strings.TrimSpace(bridgeName)
		if bridgeName != "" {
			unique[bridgeName] = struct{}{}
		}
	}

	names := make([]string, 0, len(unique))
	for bridgeName := range unique {
		names = append(names, bridgeName)
	}
	sort.Strings(names)

	locks := make([]*sync.Mutex, 0, len(names))
	for _, bridgeName := range names {
		value, _ := standardSwitchLifecycleLocks.LoadOrStore(bridgeName, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		locks = append(locks, lock)
	}

	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}
}
