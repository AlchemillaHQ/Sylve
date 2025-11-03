// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemServiceInterfaces

import "github.com/alchemillahq/sylve/pkg/zfs"

type SystemServiceInterface interface {
	SyncPPTDevices() error
	GetUsablePools() ([]*zfs.Zpool, error)
	GetValidPool(identifier string) (*zfs.Zpool, error)
}
