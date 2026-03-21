//go:build !linux && !freebsd

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import "fmt"

func (s *Service) AuthenticatePAM(username, password string) (bool, error) {
	return false, fmt.Errorf("pam_auth_not_supported_on_this_platform")
}
