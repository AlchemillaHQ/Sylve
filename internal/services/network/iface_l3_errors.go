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
	MinHostInterfaceL3MTU     = 68
	MinHostInterfaceL3IPv6MTU = 1280
	MaxHostInterfaceL3MTU     = 65535
)

var (
	ErrInvalidHostInterfaceL3  = errors.New("invalid host interface l3")
	ErrHostInterfaceL3NotFound = errors.New("host interface l3 not found")
	ErrHostInterfaceL3Conflict = errors.New("host interface l3 conflict")
	ErrHostInterfaceL3Pending  = errors.New("host interface l3 pending operation")
)

type hostInterfaceL3Error struct {
	kind  error
	code  string
	cause error
}

func (e *hostInterfaceL3Error) Error() string {
	return e.code
}

func (e *hostInterfaceL3Error) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func invalidHostInterfaceL3(code string, cause error) error {
	return &hostInterfaceL3Error{kind: ErrInvalidHostInterfaceL3, code: code, cause: cause}
}

func hostInterfaceL3NotFound(cause error) error {
	return &hostInterfaceL3Error{
		kind:  ErrHostInterfaceL3NotFound,
		code:  "host_interface_l3_not_found",
		cause: cause,
	}
}

func hostInterfaceL3Conflict(code string, cause error) error {
	return &hostInterfaceL3Error{kind: ErrHostInterfaceL3Conflict, code: code, cause: cause}
}

func hostInterfaceL3PendingConflict(cause error) error {
	return &hostInterfaceL3Error{
		kind:  ErrHostInterfaceL3Pending,
		code:  "host_interface_l3_pending_conflict",
		cause: cause,
	}
}

func HostInterfaceL3ErrorCode(err error) string {
	var hostErr *hostInterfaceL3Error
	if errors.As(err, &hostErr) && hostErr.code != "" {
		return hostErr.code
	}
	return "host_interface_l3_operation_failed"
}
