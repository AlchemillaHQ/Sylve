// SPDX-License-Identifier: BSD-2-Clause

package cmd

import (
	"context"
	"slices"
	"strings"
	"testing"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/urfave/cli/v3"
)

func runSpecFlags(t *testing.T, spec containerServiceInterfaces.Spec, args ...string) (containerServiceInterfaces.Spec, error) {
	t.Helper()
	var applyErr error
	command := &cli.Command{Name: "update", DisableSliceFlagSeparator: true, Flags: specFlags(),
		Action: func(_ context.Context, command *cli.Command) error {
			applyErr = applySpecFlags(command, &spec)
			return nil
		}}
	if err := command.Run(t.Context(), append([]string{"update"}, args...)); err != nil {
		t.Fatal(err)
	}
	return spec, applyErr
}

func TestContainerSpecFlagsAddReplaceAndRemove(t *testing.T) {
	spec := containerServiceInterfaces.Spec{
		Ports: []containerModels.Port{{Protocol: "tcp", HostPort: 8080, ContainerPort: 80}, {Protocol: "udp", HostPort: 53, ContainerPort: 53}},
		Env:   []containerModels.EnvVar{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}},
		Mounts: []containerModels.Mount{{Type: containerModels.MountVolume, Source: "data", Target: "/data"},
			{Type: containerModels.MountPath, Source: "/srv", Target: "/srv"}},
	}
	spec, err := runSpecFlags(t, spec,
		"--remove-port", "8080", "--unset-env", "B", "--remove-mount", "/srv",
		"--env", "A=3", "--env", "JAVA_OPTS=-Xms1g,-Xmx2g", "--port", "9090:90",
		"--volume", "cache:/cache", "--volume-owner", "1000:1000",
		"--entrypoint", "/init", "--entrypoint", "run",
		"--workdir", "/app", "--user", "1000", "--hostname", "web", "--restart-retries", "3", "--stop-timeout", "30")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.Ports, []containerModels.Port{{Protocol: "udp", HostPort: 53, ContainerPort: 53}, {Protocol: "tcp", HostPort: 9090, ContainerPort: 90}}) {
		t.Fatal("only the named port is removed before new ones are added", spec.Ports)
	}
	if !slices.Equal(spec.Env, []containerModels.EnvVar{{Name: "A", Value: "3"}, {Name: "JAVA_OPTS", Value: "-Xms1g,-Xmx2g"}}) {
		t.Fatal("--env replaces a value, keeps commas, and --unset-env removes", spec.Env)
	}
	want := []containerModels.Mount{{Type: containerModels.MountVolume, Source: "data", Target: "/data"},
		{Type: containerModels.MountVolume, Source: "cache", Target: "/cache", Owner: "1000:1000"}}
	if !slices.Equal(spec.Mounts, want) {
		t.Fatal("--volume-owner owns new volumes and --remove-mount removes by target", spec.Mounts)
	}
	if !slices.Equal(spec.Entrypoint, []string{"/init", "run"}) || spec.WorkDir != "/app" || spec.User != "1000" ||
		spec.Hostname != "web" || spec.RestartRetries != 3 || spec.StopTimeout != 30 {
		t.Fatal("process and runtime flags are applied", spec)
	}
}

func TestContainerSpecFlagsRefuseRemovingWhatIsNotThere(t *testing.T) {
	for _, args := range [][]string{{"--remove-port", "8080/udp"}, {"--unset-env", "MISSING"}, {"--remove-mount", "/nowhere"}} {
		spec := containerServiceInterfaces.Spec{Ports: []containerModels.Port{{Protocol: "tcp", HostPort: 8080, ContainerPort: 80}}}
		if _, err := runSpecFlags(t, spec, args...); err == nil || !strings.Contains(err.Error(), "nothing to remove") {
			t.Fatal("expected a refusal for", args, err)
		}
	}
}

func TestParseContainerPortsAndMounts(t *testing.T) {
	for value, want := range map[string]containerModels.Port{
		"8080:80":               {Protocol: "tcp", HostPort: 8080, ContainerPort: 80},
		"127.0.0.1:53:53/udp":   {Protocol: "udp", HostIP: "127.0.0.1", HostPort: 53, ContainerPort: 53},
		"10.0.0.1:443:8443/tcp": {Protocol: "tcp", HostIP: "10.0.0.1", HostPort: 443, ContainerPort: 8443},
	} {
		if port, err := parsePort(value); err != nil || port != want {
			t.Fatal("unexpected port", value, port, err)
		}
	}
	for _, bad := range []string{"80", "a:b", "1:2:3:4", "70000:80"} {
		if _, err := parsePort(bad); err == nil {
			t.Fatal("expected a bad port to be refused:", bad)
		}
	}
	if source, target, readOnly, err := splitMount("tank/media:/media:ro"); err != nil || source != "tank/media" || target != "/media" || !readOnly {
		t.Fatal("unexpected mount", source, target, readOnly, err)
	}
	for _, bad := range []string{"data", ":/data", "data:/data:rw", "a:b:ro:x"} {
		if _, _, _, err := splitMount(bad); err == nil {
			t.Fatal("expected a bad mount to be refused:", bad)
		}
	}
}

func TestContainerListFlagsKeepCommas(t *testing.T) {
	for _, subcommand := range newContainersCommand().Commands {
		if (subcommand.Name == "create" || subcommand.Name == "update") && !subcommand.DisableSliceFlagSeparator {
			t.Fatal("list flags must keep commas in values:", subcommand.Name)
		}
	}
}
