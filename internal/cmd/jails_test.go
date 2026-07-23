package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/urfave/cli/v3"
)

func TestNewJailsCommandIncludesCreate(t *testing.T) {
	command := newJailsCommand()
	for _, subcommand := range command.Commands {
		if subcommand.Name == "create" {
			return
		}
	}

	t.Fatal("expected jails create command")
}

func TestNewJailsCommandIncludesBootstrap(t *testing.T) {
	command := newJailsCommand()
	for _, subcommand := range command.Commands {
		if subcommand.Name == "bootstrap" {
			return
		}
	}

	t.Fatal("expected jails bootstrap command")
}

func TestParseBootstrapVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		major   int
		minor   int
		valid   bool
	}{
		{version: "15.0", major: 15, minor: 0, valid: true},
		{version: " 16.1 ", major: 16, minor: 1, valid: true},
		{version: "15"},
		{version: "15.0.1"},
		{version: "0.0"},
		{version: "15.-1"},
		{version: "invalid.0"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			major, minor, err := consoleprotocol.ParseBootstrapVersion(tc.version)
			if tc.valid {
				if err != nil {
					t.Fatalf("ParseBootstrapVersion(%q): %v", tc.version, err)
				}
				if major != tc.major || minor != tc.minor {
					t.Fatalf("version = %d.%d, want %d.%d", major, minor, tc.major, tc.minor)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseBootstrapVersion(%q) succeeded unexpectedly", tc.version)
			}
		})
	}
}

func TestJailCommandsRejectInvalidIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"create", []string{"create", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"get", []string{"get", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"start", []string{"start", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"stop", []string{"stop", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"restart", []string{"restart", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"delete", []string{"delete", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"networks", []string{"networks", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"rmnet ctid", []string{"rmnet", "--ctid", "0", "--net-id", "1"}, "--ctid must be greater than zero"},
		{"rmnet network", []string{"rmnet", "--ctid", "1", "--net-id", "0"}, "--net-id must be greater than zero"},
		{"console", []string{"console", "--ctid", "0"}, "--ctid must be greater than zero"},
		{"negative", []string{"get", "--ctid=-1"}, "--ctid must be greater than zero"},
		{"out of range", []string{"get", "--ctid", "10000"}, "--ctid must be between 1 and 9999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newJailsCommand()
			err := command.Run(context.Background(), append([]string{"jails"}, tc.args...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("command error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildJailCreateRequestAppliesCoreFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jail.json")
	contents := `{
  "name": "ignored-name",
  "ctId": 999,
  "pool": "ignored-pool",
  "base": "ignored-base",
  "bootstrapName": "ignored-bootstrap",
  "switchName": "ignored-switch",
  "type": "linux",
  "hostname": "haos.example.test",
  "description": "Home Assistant workload",
  "resourceLimits": false,
  "cleanEnvironment": true,
  "allowedOptions": ["allow.raw_sockets"]
}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write jail create file: %v", err)
	}

	ctid := uint(101)
	name := "HAOS-W"
	pool := "zroot"
	base := "base-uuid"
	switchName := "NONE"
	jailType := "FreeBSD"
	request, err := buildJailCreateRequest(jailCreateInput{
		File: path,
		Overrides: consoleprotocol.JailCreateOverrides{
			CTID:   &ctid,
			Name:   &name,
			Pool:   &pool,
			Base:   &base,
			Switch: &switchName,
			Type:   &jailType,
		},
	})
	if err != nil {
		t.Fatalf("build jail create request: %v", err)
	}

	if request.CTID == nil || *request.CTID != 101 {
		t.Fatalf("CTID = %v, want 101", request.CTID)
	}
	if request.Name != "HAOS-W" || request.Pool != "zroot" {
		t.Fatalf("core fields = name %q, pool %q", request.Name, request.Pool)
	}
	if request.Base != "base-uuid" || request.BootstrapName != "" {
		t.Fatalf("source fields = base %q, bootstrap %q", request.Base, request.BootstrapName)
	}
	if request.SwitchName != "none" || request.Type != jailModels.JailTypeFreeBSD {
		t.Fatalf("switch/type = %q/%q", request.SwitchName, request.Type)
	}
	if request.Hostname != "haos.example.test" || request.Description != "Home Assistant workload" {
		t.Fatalf("optional text fields were not retained: %#v", request)
	}
	if request.ResourceLimits == nil || *request.ResourceLimits {
		t.Fatalf("resource limits = %v, want false", request.ResourceLimits)
	}
	if request.CleanEnvironment == nil || !*request.CleanEnvironment {
		t.Fatalf("clean environment = %v, want true", request.CleanEnvironment)
	}
	if len(request.AllowedOptions) != 1 || request.AllowedOptions[0] != "allow.raw_sockets" {
		t.Fatalf("allowed options = %v", request.AllowedOptions)
	}
}

func TestBuildJailCreateRequestRequiresExactlyOneSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		base      string
		bootstrap string
	}{
		{name: "no source"},
		{name: "both sources", base: "base-uuid", bootstrap: "14.2-RELEASE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctid := uint(101)
			name := "jail-101"
			pool := "zroot"
			switchName := "none"
			jailType := "freebsd"
			overrides := consoleprotocol.JailCreateOverrides{
				CTID:   &ctid,
				Name:   &name,
				Pool:   &pool,
				Switch: &switchName,
				Type:   &jailType,
			}
			if tc.base != "" {
				base := tc.base
				overrides.Base = &base
			}
			if tc.bootstrap != "" {
				bootstrap := tc.bootstrap
				overrides.Bootstrap = &bootstrap
			}
			_, err := buildJailCreateRequest(jailCreateInput{Overrides: overrides})
			if err == nil || !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("error = %v, want source validation error", err)
			}
		})
	}
}

func TestBuildJailCreateRequestRejectsUnknownFileFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jail.json")
	if err := os.WriteFile(path, []byte(`{"unknownSetting": true}`), 0600); err != nil {
		t.Fatalf("write jail create file: %v", err)
	}

	_, err := buildJailCreateRequest(jailCreateInput{File: path})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want unknown field error", err)
	}
}

func TestBuildJailCreateRequestUsesCompleteFileWithoutOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jail.json")
	contents := `{
  "name": "file-jail",
  "ctId": 101,
  "pool": "zroot",
  "bootstrapName": "15-0-Base",
  "switchName": "inherit",
  "type": "freebsd",
  "hostname": "file-jail.example.test"
}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write jail create file: %v", err)
	}

	request, err := buildJailCreateRequest(jailCreateInput{File: path})
	if err != nil {
		t.Fatalf("build jail create request: %v", err)
	}
	if request.CTID == nil || *request.CTID != 101 || request.Name != "file-jail" || request.BootstrapName != "15-0-Base" {
		t.Fatalf("unexpected file request: %#v", request)
	}
}

func TestBuildJailCreateRequestSupportsCoreFlagsWithoutFile(t *testing.T) {
	ctid := uint(101)
	name := "flag-jail"
	pool := "zroot"
	bootstrap := "15-0-Base"
	switchName := "inherit"
	jailType := "freebsd"

	request, err := buildJailCreateRequest(jailCreateInput{Overrides: consoleprotocol.JailCreateOverrides{
		CTID:      &ctid,
		Name:      &name,
		Pool:      &pool,
		Bootstrap: &bootstrap,
		Switch:    &switchName,
		Type:      &jailType,
	}})
	if err != nil {
		t.Fatalf("build jail create request: %v", err)
	}
	if request.CTID == nil || *request.CTID != 101 || request.Name != "flag-jail" || request.BootstrapName != "15-0-Base" {
		t.Fatalf("unexpected flag request: %#v", request)
	}
}

func TestJailCreateCommandAcceptsFileOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jail.json")
	contents := `{
  "name": "file-jail",
  "ctId": 101,
  "pool": "zroot",
  "bootstrapName": "15-0-Base",
  "switchName": "inherit",
  "type": "freebsd"
}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write jail create file: %v", err)
	}

	command := newJailsCommand()
	var got jailServiceInterfaces.CreateJailRequest
	for _, subcommand := range command.Commands {
		if subcommand.Name != "create" {
			continue
		}
		subcommand.Action = func(_ context.Context, command *cli.Command) error {
			input, err := jailCreateInputFromCommand(command)
			if err != nil {
				return err
			}
			got, err = buildJailCreateRequest(input)
			return err
		}
	}

	if err := command.Run(context.Background(), []string{"jails", "create", "--file", path}); err != nil {
		t.Fatalf("run file-only jail create command: %v", err)
	}
	if got.CTID == nil || *got.CTID != 101 || got.Name != "file-jail" {
		t.Fatalf("unexpected file-only command request: %#v", got)
	}
}
