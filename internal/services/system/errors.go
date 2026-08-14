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
)

const (
	// MaxRequestBodyBytes bounds JSON decoding and audit logging for the system API.
	// Multipart File Explorer uploads use their own independently configured limits.
	MaxRequestBodyBytes int64 = 1 * 1024 * 1024
)

var (
	ErrInvalidPassthroughDevice      = errors.New("invalid_passthrough_device")
	ErrUnsupportedPassthroughDomain  = errors.New("unsupported_passthrough_domain")
	ErrPassthroughDeviceNotFound     = errors.New("passthrough_device_not_found")
	ErrPassthroughDeviceAlreadyAdded = errors.New("passthrough_device_already_managed")
	ErrPassthroughDeviceNeedsImport  = errors.New("passthrough_device_requires_import")
	ErrPassthroughDeviceNotAttached  = errors.New("passthrough_device_not_attached")
	ErrPassthroughDeviceInUse        = errors.New("passthrough_device_in_use")
)

func PassthroughErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidPassthroughDevice):
		return ErrInvalidPassthroughDevice.Error()
	case errors.Is(err, ErrUnsupportedPassthroughDomain):
		return ErrUnsupportedPassthroughDomain.Error()
	case errors.Is(err, ErrPassthroughDeviceNotFound):
		return ErrPassthroughDeviceNotFound.Error()
	case errors.Is(err, ErrPassthroughDeviceAlreadyAdded):
		return ErrPassthroughDeviceAlreadyAdded.Error()
	case errors.Is(err, ErrPassthroughDeviceNeedsImport):
		return ErrPassthroughDeviceNeedsImport.Error()
	case errors.Is(err, ErrPassthroughDeviceNotAttached):
		return ErrPassthroughDeviceNotAttached.Error()
	case errors.Is(err, ErrPassthroughDeviceInUse):
		return ErrPassthroughDeviceInUse.Error()
	default:
		return "passthrough_operation_failed"
	}
}
