// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import "fmt"

const (
	GuestAudit            = "Guest.Audit"
	GuestPowerMgmt        = "Guest.PowerMgmt"
	GuestConsole          = "Guest.Console"
	GuestConfigCPU        = "Guest.Config.CPU"
	GuestConfigMemory     = "Guest.Config.Memory"
	GuestConfigDisk       = "Guest.Config.Disk"
	GuestConfigNetwork    = "Guest.Config.Network"
	GuestConfigCloudinit  = "Guest.Config.Cloudinit"
	GuestConfigHWType     = "Guest.Config.HWType"
	GuestConfigOptions    = "Guest.Config.Options"
	GuestAllocate         = "Guest.Allocate"
	GuestClone            = "Guest.Clone"
	GuestMigrate          = "Guest.Migrate"
	GuestSnapshot         = "Guest.Snapshot"
	GuestSnapshotRollback = "Guest.Snapshot.Rollback"

	DatastoreAudit            = "Datastore.Audit"
	DatastoreAllocateSpace    = "Datastore.AllocateSpace"
	DatastoreAllocateTemplate = "Datastore.AllocateTemplate"

	NetworkAudit = "Network.Audit"
	NetworkUse   = "Network.Use"

	SysAudit     = "Sys.Audit"
	SysSyslog    = "Sys.Syslog"
	SysConsole   = "Sys.Console"
	SysModify    = "Sys.Modify"
	SysPowerMgmt = "Sys.PowerMgmt"

	UserModify        = "User.Modify"
	PermissionsModify = "Permissions.Modify"
)

func GuestPath(kind string, id uint) string { return fmt.Sprintf("/%s/%d", kind, id) }

func StoragePath(pool string) string { return "/storage/" + pool }

func SwitchPath(name string) string { return "/switch/" + name }
