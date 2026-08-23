// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import "errors"

const (
	// MaxRequestBodyBytes leaves ample room for iSCSI settings while bounding
	// request logging and JSON decoding.
	MaxRequestBodyBytes int64 = 64 * 1024
)

var (
	ErrInvalidRequest = errors.New("invalid iSCSI request")
	ErrNotFound       = errors.New("iSCSI resource not found")
	ErrConflict       = errors.New("iSCSI resource conflict")
	ErrApplyFailed    = errors.New("iSCSI configuration apply failed")
	ErrRuntimeFailed  = errors.New("iSCSI runtime operation failed")
)

type serviceError struct {
	kind    error
	message string
	cause   error
}

func (e *serviceError) Error() string {
	return e.message
}

func (e *serviceError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func invalidRequest(message string) error {
	return &serviceError{kind: ErrInvalidRequest, message: message}
}

func resourceNotFound(message string, cause error) error {
	return &serviceError{kind: ErrNotFound, message: message, cause: cause}
}

func resourceConflict(message string, cause error) error {
	return &serviceError{kind: ErrConflict, message: message, cause: cause}
}

func applyFailed(message string, cause error) error {
	return &serviceError{kind: ErrApplyFailed, message: message, cause: cause}
}

func runtimeFailed(message string, cause error) error {
	return &serviceError{kind: ErrRuntimeFailed, message: message, cause: cause}
}
