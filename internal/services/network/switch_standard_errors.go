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
	MaxStandardSwitchNameBytes          = 128
	MaxStandardSwitchPorts              = 256
	MaxStandardSwitchPortNameBytes      = 64
	MaxStandardSwitchManualAddressBytes = 256
)

var (
	ErrInvalidStandardSwitch  = errors.New("invalid standard switch")
	ErrStandardSwitchNotFound = errors.New("standard switch not found")
	ErrStandardSwitchConflict = errors.New("standard switch conflict")
	ErrStandardSwitchInUse    = errors.New("standard switch in use")
)

type standardSwitchError struct {
	kind  error
	code  string
	cause error
}

func (e *standardSwitchError) Error() string {
	return e.code
}

func (e *standardSwitchError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func invalidStandardSwitch(code string, cause error) error {
	return &standardSwitchError{kind: ErrInvalidStandardSwitch, code: code, cause: cause}
}

func standardSwitchNotFound(cause error) error {
	return &standardSwitchError{
		kind:  ErrStandardSwitchNotFound,
		code:  "standard_switch_not_found",
		cause: cause,
	}
}

func standardSwitchConflict(code string, cause error) error {
	return &standardSwitchError{kind: ErrStandardSwitchConflict, code: code, cause: cause}
}

func standardSwitchInUse(code string) error {
	return &standardSwitchError{kind: ErrStandardSwitchInUse, code: code}
}

// StandardSwitchErrorCode returns a stable code suitable for an API response.
func StandardSwitchErrorCode(err error) string {
	var switchErr *standardSwitchError
	if errors.As(err, &switchErr) && switchErr.code != "" {
		return switchErr.code
	}
	return "standard_switch_operation_failed"
}
