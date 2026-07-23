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
