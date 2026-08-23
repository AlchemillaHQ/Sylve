// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"fmt"
	"testing"

	"github.com/digitalocean/go-libvirt"
)

func TestIsLibvirtDomainAbsent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "genuine domain not found (libvirt reachable)",
			err:  fmt.Errorf("failed_to_lookup_domain: Domain not found: no domain with matching name '100'"),
			want: true,
		},
		{
			name: "no domain with matching name phrasing",
			err:  fmt.Errorf("failed_to_lookup_domain: no domain with matching name '42'"),
			want: true,
		},
		{
			name: "transient lookup rpc error is NOT absent",
			err:  fmt.Errorf("failed_to_lookup_domain: internal error: rpc timeout"),
			want: false,
		},
		{
			name: "libvirt connection failure is NOT absent",
			err:  fmt.Errorf("failed to connect: dial unix /var/run/libvirt/libvirt-sock: connect: no such file or directory"),
			want: false,
		},
		{
			name: "unrelated domain-state error is NOT absent",
			err:  fmt.Errorf("failed_to_get_domain_state: some error"),
			want: false,
		},
		{
			name: "typed ERR_NO_DOMAIN (code 42) via errors.As",
			err:  libvirt.Error{Code: 42},
			want: true,
		},
		{
			name: "typed ERR_NO_DOMAIN wrapped via fmt.Errorf %w",
			err:  fmt.Errorf("failed_to_lookup_domain: %w", libvirt.Error{Code: 42}),
			want: true,
		},
		{
			name: "typed ERROR with unrelated code is NOT absent",
			err:  libvirt.Error{Code: 38},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLibvirtDomainAbsent(tc.err); got != tc.want {
				t.Fatalf("isLibvirtDomainAbsent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
