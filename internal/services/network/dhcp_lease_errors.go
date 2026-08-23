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
	MaxDHCPLeaseHostnameBytes          = 63
	MaxDHCPLeaseCommentsBytes          = 4096
	MaxDynamicDHCPLeaseIdentifierBytes = 768
)

var (
	ErrInvalidDHCPLease  = errors.New("invalid DHCP lease")
	ErrDHCPLeaseNotFound = errors.New("DHCP lease resource not found")
	ErrDHCPLeaseConflict = errors.New("DHCP lease conflict")
)

type dhcpLeaseError struct {
	kind  error
	code  string
	cause error
}

func (e *dhcpLeaseError) Error() string {
	return e.code
}

func (e *dhcpLeaseError) Unwrap() []error {
	wrapped := []error{e.kind}
	if e.cause == nil {
		return wrapped
	}
	return append(wrapped, e.cause)
}

func invalidDHCPLease(code string, cause error) error {
	return &dhcpLeaseError{kind: ErrInvalidDHCPLease, code: code, cause: cause}
}

func dhcpLeaseNotFound(code string, cause error) error {
	return &dhcpLeaseError{kind: ErrDHCPLeaseNotFound, code: code, cause: cause}
}

func conflictingDHCPLease(code string, cause error) error {
	return &dhcpLeaseError{kind: ErrDHCPLeaseConflict, code: code, cause: cause}
}

// DHCPLeaseErrorCode returns a stable code suitable for an API response.
func DHCPLeaseErrorCode(err error) string {
	var leaseErr *dhcpLeaseError
	if errors.As(err, &leaseErr) && leaseErr.code != "" {
		return leaseErr.code
	}
	return "dhcp_lease_operation_failed"
}
