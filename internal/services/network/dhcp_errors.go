// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import "errors"

const (
	MaxDHCPConfigSwitches    = 256
	MaxDHCPConfigDNSServers  = 16
	MaxDHCPConfigDomainBytes = 253
)

var (
	ErrInvalidDHCPConfig  = errors.New("invalid DHCP config")
	ErrDHCPConfigConflict = errors.New("DHCP config conflict")
)

type dhcpConfigError struct {
	kind  error
	code  string
	cause error
}

func (e *dhcpConfigError) Error() string {
	return e.code
}

func (e *dhcpConfigError) Unwrap() []error {
	wrapped := []error{e.kind}
	if e.cause == nil {
		return wrapped
	}
	return append(wrapped, e.cause)
}

func invalidDHCPConfig(code string, cause error) error {
	return &dhcpConfigError{kind: ErrInvalidDHCPConfig, code: code, cause: cause}

}

func conflictingDHCPConfig(code string, cause error) error {
	return &dhcpConfigError{kind: ErrDHCPConfigConflict, code: code, cause: cause}
}

// DHCPConfigErrorCode returns a stable code suitable for an API response.
func DHCPConfigErrorCode(err error) string {
	var configErr *dhcpConfigError
	if errors.As(err, &configErr) && configErr.code != "" {
		return configErr.code
	}
	return "dhcp_config_operation_failed"
}
