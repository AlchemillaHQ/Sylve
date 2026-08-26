// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import "errors"

const (
	// MaxRequestBodyBytes is intentionally generous for disk mutation and
	// S.M.A.R.T. schedule requests while bounding JSON decoding and logging.
	MaxRequestBodyBytes int64 = 64 * 1024

	MaxPartitionsPerRequest = 128
)

var (
	ErrInvalidDiskRequest    = errors.New("invalid disk request")
	ErrDiskResourceNotFound  = errors.New("disk resource not found")
	ErrDiskOperationConflict = errors.New("disk operation conflict")
	ErrDiskOperationFailed   = errors.New("disk operation failed")
)

type operationError struct {
	kind    error
	message string
	cause   error
}

func (e *operationError) Error() string {
	return e.message
}

func (e *operationError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func invalidDiskRequest(message string, cause error) error {
	return &operationError{kind: ErrInvalidDiskRequest, message: message, cause: cause}
}

func diskResourceNotFound(message string, cause error) error {
	return &operationError{kind: ErrDiskResourceNotFound, message: message, cause: cause}
}

func diskOperationConflict(message string, cause error) error {
	return &operationError{kind: ErrDiskOperationConflict, message: message, cause: cause}
}

func diskOperationFailed(message string, cause error) error {
	return &operationError{kind: ErrDiskOperationFailed, message: message, cause: cause}
}
