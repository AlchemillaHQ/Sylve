// SPDX-License-Identifier: BSD-2-Clause

package remoteexec

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCommandPreservesArgumentsThroughLoginShells(t *testing.T) {
	values := []string{
		"space value",
		"semicolon;value",
		"$(printf substituted)",
		"single'quote",
		"double\"quote",
		"line one\nline two",
		"*",
		"",
	}
	argv := append([]string{"/usr/bin/printf", "<%s>\n"}, values...)
	command, err := NewCommand(argv...)
	if err != nil {
		t.Fatalf("new command: %v", err)
	}
	transport, err := command.SSHArgument()
	if err != nil {
		t.Fatalf("transport command: %v", err)
	}
	if strings.Contains(transport, values[3]) {
		t.Fatalf("transport exposed raw command argument: %q", transport)
	}

	var expected strings.Builder
	for _, value := range values {
		expected.WriteString("<")
		expected.WriteString(value)
		expected.WriteString(">\n")
	}
	for _, shell := range []struct {
		name string
		path string
		args []string
	}{
		{name: "posix", path: "/bin/sh", args: []string{"-c", transport}},
		{name: "csh", path: "/bin/csh", args: []string{"-f", "-c", transport}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			if _, err := exec.LookPath(shell.path); err != nil {
				t.Skipf("%s unavailable: %v", shell.path, err)
			}
			output, err := exec.Command(shell.path, shell.args...).CombinedOutput()
			if err != nil {
				t.Fatalf("execute transport: %v\n%s", err, output)
			}
			if string(output) != expected.String() {
				t.Fatalf("output = %q, want %q", output, expected.String())
			}
		})
	}
}

func TestScriptUsesEncodedPOSIXShellTransport(t *testing.T) {
	script := "set -eu\nvalue='quoted value'\nprintf '%s' \"$value\""
	command, err := NewScript(script)
	if err != nil {
		t.Fatalf("new script: %v", err)
	}
	transport, err := command.SSHArgument()
	if err != nil {
		t.Fatalf("transport command: %v", err)
	}
	if strings.Contains(transport, script) {
		t.Fatal("transport exposed raw script")
	}

	for _, shell := range []struct {
		name string
		path string
		args []string
	}{
		{name: "posix", path: "/bin/sh", args: []string{"-c", transport}},
		{name: "csh", path: "/bin/csh", args: []string{"-f", "-c", transport}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			if _, err := exec.LookPath(shell.path); err != nil {
				t.Skipf("%s unavailable: %v", shell.path, err)
			}
			output, err := exec.Command(shell.path, shell.args...).CombinedOutput()
			if err != nil {
				t.Fatalf("execute transport: %v\n%s", err, output)
			}
			if string(output) != "quoted value" {
				t.Fatalf("output = %q, want %q", output, "quoted value")
			}
		})
	}
}

func TestCommandTransportPreservesStdin(t *testing.T) {
	command, err := NewCommand("/bin/cat")
	if err != nil {
		t.Fatalf("new command: %v", err)
	}
	transport, err := command.SSHArgument()
	if err != nil {
		t.Fatalf("transport command: %v", err)
	}
	for _, shell := range []struct {
		name string
		path string
		args []string
	}{
		{name: "posix", path: "/bin/sh", args: []string{"-c", transport}},
		{name: "csh", path: "/bin/csh", args: []string{"-f", "-c", transport}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			if _, err := exec.LookPath(shell.path); err != nil {
				t.Skipf("%s unavailable: %v", shell.path, err)
			}
			cmd := exec.Command(shell.path, shell.args...)
			cmd.Stdin = strings.NewReader("stream payload\n")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("execute transport: %v\n%s", err, output)
			}
			if string(output) != "stream payload\n" {
				t.Fatalf("output = %q", output)
			}
		})
	}
}

func TestCommandSSHArgs(t *testing.T) {
	destination, err := ParseSSHDestination("root@Backup.Example")
	if err != nil {
		t.Fatalf("parse destination: %v", err)
	}
	command, err := NewCommand("zfs", "recv", "-o", "sylve:run-id=secret-token", "tank/backups/vm")
	if err != nil {
		t.Fatalf("new command: %v", err)
	}
	args, err := command.SSHArgs([]string{"-n", "-o", "BatchMode=yes"}, destination, true)
	if err != nil {
		t.Fatalf("ssh args: %v", err)
	}
	if len(args) != 4 || args[0] != "-o" || args[1] != "BatchMode=yes" || args[2] != "root@backup.example" {
		t.Fatalf("ssh args = %v", args)
	}
	if strings.Contains(args[3], "secret-token") || !strings.HasPrefix(args[3], "/bin/sh -c ") {
		t.Fatalf("remote command was not encoded: %q", args[3])
	}
	if _, err := command.SSHArgs(nil, SSHDestination{}, false); err == nil {
		t.Fatal("zero destination was accepted")
	}
}

func TestCommandRejectsInvalidInput(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	tests := []struct {
		name string
		make func() (Command, error)
	}{
		{name: "missing argv", make: func() (Command, error) { return NewCommand() }},
		{name: "empty command", make: func() (Command, error) { return NewCommand("") }},
		{name: "nul argument", make: func() (Command, error) { return NewCommand("zfs", "a\x00b") }},
		{name: "invalid utf8 argument", make: func() (Command, error) { return NewCommand("zfs", invalidUTF8) }},
		{name: "empty script", make: func() (Command, error) { return NewScript(" \n") }},
		{name: "nul script", make: func() (Command, error) { return NewScript("echo\x00value") }},
		{name: "invalid utf8 script", make: func() (Command, error) { return NewScript(invalidUTF8) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.make(); err == nil {
				t.Fatal("expected invalid input to fail")
			}
		})
	}

	if _, err := (Command{}).SSHArgument(); err == nil {
		t.Fatal("expected zero command to fail")
	}
}
