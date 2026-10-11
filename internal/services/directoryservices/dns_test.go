// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestJoinDNSPolicyAndWarnings(t *testing.T) {
	for _, test := range []struct {
		name      string
		skip      bool
		output    string
		joinError bool
		warning   bool
	}{
		{name: "register by default"},
		{name: "explicitly skipped", skip: true},
		{name: "warning on successful join", output: "DNS update failed: insufficient permission", warning: true},
		{name: "failed command with established trust", output: "Failed to update DNS records", joinError: true, warning: true},
		{name: "non-DNS post-join failure", output: "post-join setup failed", joinError: true, warning: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService(t)
			input := testADInput()
			input.SkipDNSRegistration = test.skip
			if _, err := s.SaveAD(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			healthy := healthyCommands(t, s, "test-join-password")
			s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
				if strings.Contains(strings.Join(args, " "), "ads join") {
					if slices.Contains(args, "--no-dns-updates") != test.skip {
						t.Fatal("join ignored DNS policy")
					}
					if test.joinError {
						return test.output, errors.New("simulated post-join failure")
					}
					return test.output, nil
				}
				return healthy(ctx, path, args, input, env)
			}
			if err := s.Join(context.Background(), JoinInput{Username: "joinuser", Password: "test-join-password"}); err != nil {
				t.Fatal(err)
			}
			ad, err := s.GetAD()
			if err != nil || !ad.Joined || ad.MembershipPending != "" || (ad.JoinWarning != "") != test.warning || ad.SkipDNSRegistration != test.skip {
				t.Fatal("join lost DNS policy or post-join warning")
			}
			status, err := s.Status(context.Background(), false)
			if err != nil || !strings.Contains(status.Warning, ad.JoinWarning) {
				t.Fatal("cached status omitted persistent DNS warning")
			}
			if err := s.Leave(context.Background(), LeaveInput{LocalOnly: true}); err != nil {
				t.Fatal(err)
			}
			ad, _ = s.GetAD()
			if ad.JoinWarning != "" || ad.SkipDNSRegistration != test.skip {
				t.Fatal("leave did not clear warning while preserving DNS policy")
			}
		})
	}
}
