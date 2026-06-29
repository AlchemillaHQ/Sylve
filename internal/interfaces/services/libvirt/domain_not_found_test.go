// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtServiceInterfaces

import (
	"fmt"
	"testing"

	"github.com/digitalocean/go-libvirt"
)

func TestIsDomainNotFoundError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "typed ERR_NO_DOMAIN (42)",
			err:  libvirt.Error{Code: 42},
			want: true,
		},
		{
			name: "typed ERR_NO_DOMAIN wrapped",
			err:  fmt.Errorf("failed_to_lookup_domain: %w", libvirt.Error{Code: 42}),
			want: true,
		},
		{
			name: "typed unrelated code (38)",
			err:  libvirt.Error{Code: 38},
			want: false,
		},
		{
			name: "string domain not found",
			err:  fmt.Errorf("failed_to_lookup_domain: Domain not found: no domain with matching name '100'"),
			want: true,
		},
		{
			name: "string no domain phrasing",
			err:  fmt.Errorf("no domain with matching uuid"),
			want: true,
		},
		{
			name: "transient lookup error is not absent",
			err:  fmt.Errorf("failed_to_lookup_domain: rpc timeout"),
			want: false,
		},
		{
			name: "connection error is not absent",
			err:  fmt.Errorf("dial unix libvirt-sock: connection refused"),
			want: false,
		},
		{
			name: "domain-state error is not absent",
			err:  fmt.Errorf("failed_to_get_domain_state: boom"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDomainNotFoundError(tc.err); got != tc.want {
				t.Fatalf("IsDomainNotFoundError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
