// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package mountutil

import "context"

type unsupportedMountTreeOps struct{}

func newMountTreeOps() mountTreeOps {
	return unsupportedMountTreeOps{}
}

func (unsupportedMountTreeOps) list(context.Context) ([]mountInfo, error) {
	return nil, ErrUnsupported
}

func (unsupportedMountTreeOps) unmount(string, bool) error {
	return ErrUnsupported
}

func (unsupportedMountTreeOps) isBusy(error) bool {
	return false
}

func (unsupportedMountTreeOps) isGone(error) bool {
	return false
}
