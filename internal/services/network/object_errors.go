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
	// MaxRequestBodyBytes bounds JSON decoding and audit logging for the network API.
	MaxRequestBodyBytes int64 = 1 * 1024 * 1024

	MaxNetworkObjectNameBytes     = 128
	MaxNetworkObjectValues        = 1024
	MaxNetworkObjectValueBytes    = 2048
	MaxNetworkObjectListSources   = 16
	MaxBulkNetworkObjectDeleteIDs = 1024

	maxNetworkObjectListPayloadBytes int64 = 64 * 1024 * 1024
)

var (
	ErrInvalidNetworkObject  = errors.New("invalid network object")
	ErrNetworkObjectNotFound = errors.New("network object not found")
	ErrNetworkObjectConflict = errors.New("network object conflict")
	ErrNetworkObjectUpstream = errors.New("network object upstream failure")
)

type networkObjectError struct {
	kind  error
	code  string
	cause error
}

func (e *networkObjectError) Error() string {
	return e.code
}

func (e *networkObjectError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func invalidNetworkObject(code string, cause error) error {
	return &networkObjectError{kind: ErrInvalidNetworkObject, code: code, cause: cause}
}

func networkObjectNotFound(cause error) error {
	return &networkObjectError{
		kind:  ErrNetworkObjectNotFound,
		code:  "network_object_not_found",
		cause: cause,
	}
}

func networkObjectConflict(code string, cause error) error {
	return &networkObjectError{kind: ErrNetworkObjectConflict, code: code, cause: cause}
}

func networkObjectUpstream(code string, cause error) error {
	return &networkObjectError{kind: ErrNetworkObjectUpstream, code: code, cause: cause}
}

// NetworkObjectErrorCode returns the stable, public error code for an object operation.
func NetworkObjectErrorCode(err error) string {
	var objectErr *networkObjectError
	if errors.As(err, &objectErr) && objectErr.code != "" {
		return objectErr.code
	}
	return "network_object_operation_failed"
}
