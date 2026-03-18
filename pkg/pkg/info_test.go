package pkg

import (
	"testing"

	"github.com/alchemillahq/sylve/pkg/utils"
)

func resetTestHooks() {
	runCommand = utils.RunCommand
}

func TestIsPackageInstalled(t *testing.T) {
	t.Run("installed", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			if command != "/usr/sbin/pkg" {
				t.Fatalf("unexpected command: %s", command)
			}
			if len(args) != 2 || args[0] != "info" || args[1] != "nginx" {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "nginx-1.27.0", nil
		}

		if !IsPackageInstalled("nginx") {
			t.Fatalf("expected package to be installed")
		}
	})

	t.Run("not installed", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "", assertErr{}
		}

		if IsPackageInstalled("missing-package") {
			t.Fatalf("expected package not to be installed")
		}
	})
}

type assertErr struct{}

func (assertErr) Error() string { return "command failed" }
