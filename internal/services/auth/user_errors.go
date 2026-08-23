// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import "fmt"

type UserOperationErrorKind string

const (
	UserOperationValidation UserOperationErrorKind = "validation"
	UserOperationNotFound   UserOperationErrorKind = "not_found"
	UserOperationConflict   UserOperationErrorKind = "conflict"
	UserOperationDependency UserOperationErrorKind = "dependency"
	UserOperationPartial    UserOperationErrorKind = "partial"
	UserOperationInternal   UserOperationErrorKind = "internal"
)

// UserOperationError keeps integration details available to server-side logs
// while exposing only a stable code through the HTTP handler.
type UserOperationError struct {
	Kind  UserOperationErrorKind
	Code  string
	Cause error
}

func (e *UserOperationError) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *UserOperationError) Unwrap() error {
	return e.Cause
}

func newUserOperationError(kind UserOperationErrorKind, code string, cause error) error {
	return &UserOperationError{Kind: kind, Code: code, Cause: cause}
}

func userValidationError(code string) error {
	return newUserOperationError(UserOperationValidation, code, nil)
}

func userNotFoundError(code string) error {
	return newUserOperationError(UserOperationNotFound, code, nil)
}

func userConflictError(code string) error {
	return newUserOperationError(UserOperationConflict, code, nil)
}

func userDependencyError(code string, cause error) error {
	return newUserOperationError(UserOperationDependency, code, cause)
}

func userInternalError(code string, cause error) error {
	return newUserOperationError(UserOperationInternal, code, cause)
}

func userIntegrationError(code string, cause error, partial bool) error {
	if partial {
		return newUserOperationError(UserOperationPartial, code, cause)
	}
	return userDependencyError(code, cause)
}
