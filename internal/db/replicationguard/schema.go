// SPDX-License-Identifier: BSD-2-Clause

// Package replicationguard tracks which replication safety tables have
// completed schema initialization for a concrete database connection. Runtime
// hooks use this explicit bootstrap state instead of Migrator.HasTable, whose
// bool-only API cannot distinguish an absent table from a schema lookup error.
package replicationguard

import (
	"database/sql"
	"sync"
	"sync/atomic"

	"gorm.io/gorm"
)

const (
	capabilityPolicy uint32 = 1 << iota
	capabilityGuestOperation
)

var schemaCapabilities sync.Map // map[*sql.DB]*atomic.Uint32

func databaseHandle(db *gorm.DB) *sql.DB {
	if db == nil {
		return nil
	}
	handle, err := db.DB()
	if err != nil {
		return nil
	}
	return handle
}

func mark(db *gorm.DB, capability uint32) {
	handle := databaseHandle(db)
	if handle == nil {
		return
	}
	value, _ := schemaCapabilities.LoadOrStore(handle, &atomic.Uint32{})
	state := value.(*atomic.Uint32)
	for {
		current := state.Load()
		if current&capability != 0 || state.CompareAndSwap(current, current|capability) {
			return
		}
	}
}

func ready(db *gorm.DB, capability uint32) bool {
	handle := databaseHandle(db)
	if handle == nil {
		return false
	}
	value, ok := schemaCapabilities.Load(handle)
	return ok && value.(*atomic.Uint32).Load()&capability != 0
}

func MarkPolicySchemaReady(db *gorm.DB) {
	mark(db, capabilityPolicy)
}

func MarkGuestOperationSchemaReady(db *gorm.DB) {
	mark(db, capabilityGuestOperation)
}

func PolicySchemaReady(db *gorm.DB) bool {
	return ready(db, capabilityPolicy)
}

func GuestOperationSchemaReady(db *gorm.DB) bool {
	return ready(db, capabilityGuestOperation)
}
