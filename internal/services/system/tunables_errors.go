// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import "errors"

var (
	ErrTunableNameRequired      = errors.New("tunable_name_required")
	ErrTunableNotFound          = errors.New("tunable_not_found")
	ErrTunableNotWritable       = errors.New("tunable_not_writable")
	ErrInvalidTunableValue      = errors.New("invalid_tunable_value")
	ErrTunableLookupFailed      = errors.New("tunable_lookup_failed")
	ErrTunableRuntimeReadFailed = errors.New("tunable_runtime_read_failed")
	ErrTunablePersistenceFailed = errors.New("tunable_persistence_failed")
)

func TunableErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTunableNameRequired):
		return ErrTunableNameRequired.Error()
	case errors.Is(err, ErrTunableNotFound):
		return ErrTunableNotFound.Error()
	case errors.Is(err, ErrTunableNotWritable):
		return ErrTunableNotWritable.Error()
	case errors.Is(err, ErrInvalidTunableValue):
		return ErrInvalidTunableValue.Error()
	case errors.Is(err, ErrTunableLookupFailed):
		return ErrTunableLookupFailed.Error()
	case errors.Is(err, ErrTunableRuntimeReadFailed):
		return ErrTunableRuntimeReadFailed.Error()
	case errors.Is(err, ErrTunablePersistenceFailed):
		return ErrTunablePersistenceFailed.Error()
	default:
		return "tunable_operation_failed"
	}
}
