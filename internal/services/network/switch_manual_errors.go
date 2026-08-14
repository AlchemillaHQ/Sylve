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
	MaxManualSwitchNameBytes   = 128
	MaxManualSwitchBridgeBytes = 128
)

var (
	ErrInvalidManualSwitch  = errors.New("invalid manual switch")
	ErrManualSwitchNotFound = errors.New("manual switch not found")
	ErrManualSwitchConflict = errors.New("manual switch conflict")
	ErrManualSwitchInUse    = errors.New("manual switch in use")
)

type manualSwitchError struct {
	kind  error
	code  string
	cause error
}

func (e *manualSwitchError) Error() string {
	return e.code
}

func (e *manualSwitchError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func invalidManualSwitch(code string, cause error) error {
	return &manualSwitchError{kind: ErrInvalidManualSwitch, code: code, cause: cause}
}

func manualSwitchNotFound(cause error) error {
	return &manualSwitchError{
		kind:  ErrManualSwitchNotFound,
		code:  "manual_switch_not_found",
		cause: cause,
	}
}

func manualSwitchConflict(code string, cause error) error {
	return &manualSwitchError{kind: ErrManualSwitchConflict, code: code, cause: cause}
}

func manualSwitchInUse(code string) error {
	return &manualSwitchError{kind: ErrManualSwitchInUse, code: code}
}

// ManualSwitchErrorCode returns a stable code suitable for an API response.
func ManualSwitchErrorCode(err error) string {
	var switchErr *manualSwitchError
	if errors.As(err, &switchErr) && switchErr.code != "" {
		return switchErr.code
	}
	return "manual_switch_operation_failed"
}
