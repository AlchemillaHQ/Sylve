// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"fmt"
)

type SettingsErrorKind uint8

const (
	SettingsErrorInternal SettingsErrorKind = iota
	SettingsErrorBadRequest
	SettingsErrorNotFound
	SettingsErrorConflict
)

type settingsError struct {
	kind     SettingsErrorKind
	code     string
	resource string
	cause    error
}

func (e *settingsError) Error() string {
	detail := e.code
	if e.resource != "" {
		detail = fmt.Sprintf("%s: %s", detail, e.resource)
	}
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", detail, e.cause)
	}
	return detail
}

func (e *settingsError) Unwrap() error {
	return e.cause
}

func newSettingsError(kind SettingsErrorKind, code, resource string, cause error) error {
	return &settingsError{kind: kind, code: code, resource: resource, cause: cause}
}

func ClassifySettingsError(err error) SettingsErrorKind {
	if errors.Is(err, ErrBasicSettingsNotFound) {
		return SettingsErrorNotFound
	}

	var settingsErr *settingsError
	if errors.As(err, &settingsErr) {
		return settingsErr.kind
	}

	return SettingsErrorInternal
}

func SettingsErrorCode(err error) string {
	if errors.Is(err, ErrBasicSettingsNotFound) {
		return ErrBasicSettingsNotFound.Error()
	}

	var settingsErr *settingsError
	if errors.As(err, &settingsErr) {
		return settingsErr.code
	}

	return "basic_settings_operation_failed"
}

func SettingsErrorResource(err error) string {
	var settingsErr *settingsError
	if errors.As(err, &settingsErr) {
		return settingsErr.resource
	}

	return ""
}
