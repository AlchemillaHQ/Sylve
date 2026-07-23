package cmd

import "testing"

func TestNewDownloadsCommandIncludesExpectedWorkflows(t *testing.T) {
	command := newDownloadsCommand()
	want := map[string]bool{"list": false, "start": false, "delete": false}

	for _, subcommand := range command.Commands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("expected downloads %s command", name)
		}
	}
}
