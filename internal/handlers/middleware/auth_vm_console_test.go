// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import "testing"

func TestIsVMConsoleWebSocketPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/vm/107/console", want: true},
		{path: "/api/vm/not-a-rid/console", want: true},
		{path: "/api/vm/console", want: false},
		{path: "/api/vm//console", want: false},
		{path: "/api/vm/107/subresource/console", want: false},
		{path: "/api/vm/107/console/extra", want: false},
		{path: "/api/jail/107/console", want: false},
	}

	for _, test := range tests {
		if got := isVMConsoleWebSocketPath(test.path); got != test.want {
			t.Fatalf("path=%q websocket=%v want=%v", test.path, got, test.want)
		}
	}
}
