package cmd

import (
	"context"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func TestNewObjectsCommandIncludesExpectedWorkflows(t *testing.T) {
	command := newObjectsCommand()
	want := map[string]bool{"list": false, "create": false, "edit": false, "delete": false}

	for _, subcommand := range command.Commands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("expected objects %s command", name)
		}
	}
}

func TestObjectCreateCommandBuildsRepeatedValuePayload(t *testing.T) {
	command := newObjectsCommand()
	var got consoleprotocol.ObjectCreatePayload
	for _, subcommand := range command.Commands {
		if subcommand.Name != "create" {
			continue
		}
		subcommand.Action = func(_ context.Context, command *cli.Command) error {
			got = consoleprotocol.ObjectCreatePayload{Request: networkObjectRequestFromCommand(command)}
			return nil
		}
	}

	err := command.Run(context.Background(), []string{
		"objects", "create", "--name", "lan4", "--type", "network",
		"--value", "192.0.2.0/24", "--value", "198.51.100.0/24",
	})
	if err != nil {
		t.Fatalf("run object create command: %v", err)
	}
	if got.Request.Name != "lan4" || got.Request.Type != "network" {
		t.Fatalf("unexpected object request: %#v", got.Request)
	}
	if len(got.Request.Values) != 2 || got.Request.Values[0] != "192.0.2.0/24" || got.Request.Values[1] != "198.51.100.0/24" {
		t.Fatalf("unexpected object values: %#v", got.Request.Values)
	}
}

func TestObjectEditCommandBuildsPartialPayload(t *testing.T) {
	command := newObjectsCommand()
	var got consoleprotocol.ObjectEditPayload
	for _, subcommand := range command.Commands {
		if subcommand.Name != "edit" {
			continue
		}
		subcommand.Action = func(_ context.Context, command *cli.Command) error {
			request, err := networkObjectEditRequestFromCommand(command)
			if err != nil {
				return err
			}
			got = consoleprotocol.ObjectEditPayload{ID: uint(command.Int("id")), Request: request}
			return nil
		}
	}

	err := command.Run(context.Background(), []string{
		"objects", "edit", "--id", "1", "--value", "16:8C:61:52:FF:60",
	})
	if err != nil {
		t.Fatalf("run object edit command: %v", err)
	}
	if got.ID != 1 || got.Request.Values == nil || len(*got.Request.Values) != 1 || (*got.Request.Values)[0] != "16:8C:61:52:FF:60" {
		t.Fatalf("unexpected object edit payload: %#v", got)
	}
	if got.Request.Name != nil || got.Request.Type != nil {
		t.Fatalf("expected name and type to remain absent: %#v", got.Request)
	}
}
