// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
)

var (
	ErrInvalidRequest      = errors.New("invalid_request")
	ErrPoolNotFound        = errors.New("pool_not_found")
	ErrDatasetNotFound     = errors.New("dataset_not_found")
	ErrSnapshotJobNotFound = errors.New("snapshot_job_not_found")
	ErrSourceNotFound      = errors.New("source_not_found")
	ErrConflict            = errors.New("conflict")
)

// classifiedError lets handlers inspect an error category while preserving the
// existing detail string consumed by the frontend's error translators.
type classifiedError struct {
	kind   error
	detail error
}

func (e *classifiedError) Error() string {
	return e.detail.Error()
}

func (e *classifiedError) Unwrap() []error {
	return []error{e.kind, e.detail}
}

func classifyError(kind error, format string, args ...any) error {
	return &classifiedError{
		kind:   kind,
		detail: fmt.Errorf(format, args...),
	}
}

func gzfsCommandReportsMissingResource(err error) bool {
	var commandError *gzfs.CmdError
	if !errors.As(err, &commandError) {
		return false
	}

	detail := strings.ToLower(strings.TrimSpace(commandError.Stderr))
	return strings.Contains(detail, "does not exist") ||
		(strings.Contains(detail, "no such") &&
			(strings.Contains(detail, "dataset") || strings.Contains(detail, "pool")))
}

func datasetLookupError(err error, format string, args ...any) error {
	if err != nil && !gzfsCommandReportsMissingResource(err) {
		return err
	}
	return classifyError(ErrDatasetNotFound, format, args...)
}

func poolLookupError(err error, format string, args ...any) error {
	if err != nil {
		detail := strings.ToLower(strings.TrimSpace(err.Error()))
		guidNotFound := strings.HasPrefix(detail, "pool with guid ") &&
			strings.HasSuffix(detail, " not found")
		if !guidNotFound && !gzfsCommandReportsMissingResource(err) {
			return err
		}
	}
	return classifyError(ErrPoolNotFound, format, args...)
}
