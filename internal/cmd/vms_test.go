// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import "testing"

func TestNewVMsCommandIncludesExpectedWorkflows(t *testing.T) {
	command := newVMsCommand()
	want := map[string]bool{
		"create":   false,
		"list":     false,
		"get":      false,
		"start":    false,
		"stop":     false,
		"shutdown": false,
		"reboot":   false,
		"delete":   false,
		"purge":    false,
		"networks": false,
		"addnet":   false,
		"rmnet":    false,
		"qga":      false,
	}

	for _, subcommand := range command.Commands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("expected vms %s command", name)
		}
	}
}
