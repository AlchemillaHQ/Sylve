// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package basicHandlers

import (
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/system"
)

type classifiedInitializationError struct {
	kind system.InitializationErrorKind
}

func (e classifiedInitializationError) Error() string {
	return "initialization_error"
}

func (e classifiedInitializationError) InitializationKind() system.InitializationErrorKind {
	return e.kind
}

func TestInitializationHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		errs []error
		want int
	}{
		{
			name: "bad request",
			errs: []error{classifiedInitializationError{kind: system.InitializationErrorBadRequest}},
			want: http.StatusBadRequest,
		},
		{
			name: "conflict",
			errs: []error{classifiedInitializationError{kind: system.InitializationErrorConflict}},
			want: http.StatusConflict,
		},
		{
			name: "unprocessable",
			errs: []error{classifiedInitializationError{kind: system.InitializationErrorUnprocessable}},
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "internal takes precedence",
			errs: []error{
				classifiedInitializationError{kind: system.InitializationErrorUnprocessable},
				classifiedInitializationError{kind: system.InitializationErrorInternal},
			},
			want: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := initializationHTTPStatus(test.errs); got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}
