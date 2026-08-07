// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import "errors"

const MaxDHCPRangeExpirySeconds uint64 = 1<<32 - 1

var (
	ErrInvalidDHCPRange  = errors.New("invalid DHCP range")
	ErrDHCPRangeNotFound = errors.New("DHCP range resource not found")
	ErrDHCPRangeConflict = errors.New("DHCP range conflict")
)

type dhcpRangeError struct {
	kind  error
	code  string
	cause error
}

func (e *dhcpRangeError) Error() string {
	return e.code
}

func (e *dhcpRangeError) Unwrap() []error {
	errors := []error{e.kind}
	if e.cause != nil {
		errors = append(errors, e.cause)
	}
	return errors
}

func invalidDHCPRange(code string, cause error) error {
	return &dhcpRangeError{kind: ErrInvalidDHCPRange, code: code, cause: cause}
}

func dhcpRangeNotFound(code string, cause error) error {
	return &dhcpRangeError{kind: ErrDHCPRangeNotFound, code: code, cause: cause}
}

func conflictingDHCPRange(code string, cause error) error {
	return &dhcpRangeError{kind: ErrDHCPRangeConflict, code: code, cause: cause}
}

// DHCPRangeErrorCode returns a stable code suitable for an API response.
func DHCPRangeErrorCode(err error) string {
	var rangeErr *dhcpRangeError
	if errors.As(err, &rangeErr) && rangeErr.code != "" {
		return rangeErr.code
	}
	return "dhcp_range_operation_failed"
}
