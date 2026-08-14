// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import "fmt"

type GroupOperationErrorKind string

const (
	GroupOperationValidation GroupOperationErrorKind = "validation"
	GroupOperationNotFound   GroupOperationErrorKind = "not_found"
	GroupOperationConflict   GroupOperationErrorKind = "conflict"
	GroupOperationDependency GroupOperationErrorKind = "dependency"
	GroupOperationPartial    GroupOperationErrorKind = "partial"
	GroupOperationInternal   GroupOperationErrorKind = "internal"
)

// GroupOperationError keeps integration details available to server-side logs
// while exposing only a stable code through the HTTP handler.
type GroupOperationError struct {
	Kind  GroupOperationErrorKind
	Code  string
	Cause error
}

func (e *GroupOperationError) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *GroupOperationError) Unwrap() error {
	return e.Cause
}

func newGroupOperationError(kind GroupOperationErrorKind, code string, cause error) error {
	return &GroupOperationError{Kind: kind, Code: code, Cause: cause}
}

func groupValidationError(code string) error {
	return newGroupOperationError(GroupOperationValidation, code, nil)
}

func groupNotFoundError(code string) error {
	return newGroupOperationError(GroupOperationNotFound, code, nil)
}

func groupConflictError(code string) error {
	return newGroupOperationError(GroupOperationConflict, code, nil)
}

func groupDependencyError(code string, cause error) error {
	return newGroupOperationError(GroupOperationDependency, code, cause)
}

func groupInternalError(code string, cause error) error {
	return newGroupOperationError(GroupOperationInternal, code, cause)
}

func groupPartialError(code string, cause error) error {
	return newGroupOperationError(GroupOperationPartial, code, cause)
}
