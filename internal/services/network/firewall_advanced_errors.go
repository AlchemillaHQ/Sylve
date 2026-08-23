// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import "errors"

const MaxFirewallAdvancedSectionBytes = 256 * 1024

var ErrInvalidFirewallAdvancedSettings = errors.New("invalid firewall advanced settings")

func invalidFirewallAdvancedSettings(cause error) error {
	if cause == nil || errors.Is(cause, ErrInvalidFirewallAdvancedSettings) {
		return ErrInvalidFirewallAdvancedSettings
	}
	return errors.Join(ErrInvalidFirewallAdvancedSettings, cause)
}

// FirewallAdvancedValidationDetail returns a sanitized PF validation diagnostic
// suitable for an authenticated local administrator.
func FirewallAdvancedValidationDetail(err error) string {
	var validationErr *pfValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Error()
	}
	return ""
}
