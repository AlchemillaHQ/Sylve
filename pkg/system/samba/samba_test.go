package samba

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func resetMocks() {
	runCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}
	runCommandWithInput = func(command, input string, args ...string) (string, error) {
		return "", nil
	}
	unixUserExists = func(name string) (bool, error) {
		return false, nil
	}
}

func TestSambaUserExists_UserExists(t *testing.T) {
	defer resetMocks()

	runCommand = func(command string, args ...string) (string, error) {
		if command != "/usr/local/bin/pdbedit" {
			t.Fatalf("unexpected command: %s", command)
		}
		if len(args) != 2 || args[0] != "-L" || args[1] != "alice" {
			t.Fatalf("unexpected args: %#v", args)
		}
		return "alice:1000:", nil
	}

	exists, err := SambaUserExists("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatalf("expected user to exist")
	}
}

func TestSambaUserExists_NoSuchUser(t *testing.T) {
	cases := []string{
		"no such user",
		"NT_STATUS_NO_SUCH_USER",
		"Username not found!",
	}

	for _, outMsg := range cases {
		t.Run(outMsg, func(t *testing.T) {
			defer resetMocks()

			runCommand = func(command string, args ...string) (string, error) {
				return outMsg, errors.New("exit status 1")
			}

			exists, err := SambaUserExists("alice")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists {
				t.Fatalf("expected user not to exist")
			}
		})
	}
}

func TestSambaUserExists_EmptyLookupIsMissing(t *testing.T) {
	for _, commandErr := range []error{nil, errors.New("exit status 1")} {
		t.Run(fmt.Sprintf("error=%v", commandErr), func(t *testing.T) {
			defer resetMocks()
			runCommand = func(command string, args ...string) (string, error) {
				return "", commandErr
			}
			exists, err := SambaUserExists("alice")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists {
				t.Fatal("empty Samba lookup must not report an existing user")
			}
		})
	}
}

func TestSambaUserExists_UnexpectedError(t *testing.T) {
	defer resetMocks()

	runCommand = func(command string, args ...string) (string, error) {
		return "some unexpected failure", errors.New("exit status 1")
	}

	exists, err := SambaUserExists("alice")
	if err == nil {
		t.Fatalf("expected error")
	}
	if exists {
		t.Fatalf("expected exists=false")
	}
	if !strings.Contains(err.Error(), "pdbedit lookup failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSambaUser_Success(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		if name != "alice" {
			t.Fatalf("unexpected username: %s", name)
		}
		return true, nil
	}

	runCommandWithInput = func(command, input string, args ...string) (string, error) {
		if command != "/usr/local/bin/smbpasswd" {
			t.Fatalf("unexpected command: %s", command)
		}
		if input != "secret\nsecret\n" {
			t.Fatalf("unexpected input: %q", input)
		}
		if len(args) != 3 || args[0] != "-s" || args[1] != "-a" || args[2] != "alice" {
			t.Fatalf("unexpected args: %#v", args)
		}
		return "", nil
	}

	err := CreateSambaUser("alice", "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEditSambaUser_Success(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		if name != "alice" {
			t.Fatalf("unexpected username: %s", name)
		}
		return true, nil
	}
	runCommand = func(command string, args ...string) (string, error) {
		if command != "/usr/local/bin/pdbedit" {
			t.Fatalf("unexpected command: %s", command)
		}
		if len(args) != 2 || args[0] != "-L" || args[1] != "alice" {
			t.Fatalf("unexpected args: %#v", args)
		}
		return "alice:1000:", nil
	}

	runCommandWithInput = func(command, input string, args ...string) (string, error) {
		if command != "/usr/local/bin/smbpasswd" {
			t.Fatalf("unexpected command: %s", command)
		}
		if input != "newsecret\nnewsecret\n" {
			t.Fatalf("unexpected input: %q", input)
		}
		if len(args) != 2 || args[0] != "-s" || args[1] != "alice" {
			t.Fatalf("unexpected args: %#v", args)
		}
		return "", nil
	}

	err := EditSambaUser("alice", "newsecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteSambaUser_Success(t *testing.T) {
	defer resetMocks()

	runCommand = func(command string, args ...string) (string, error) {
		if command != "/usr/local/bin/smbpasswd" {
			t.Fatalf("unexpected command: %s", command)
		}
		if len(args) != 2 || args[0] != "-x" || args[1] != "alice" {
			t.Fatalf("unexpected args: %#v", args)
		}
		return "", nil
	}

	err := DeleteSambaUser("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSambaUserMutationFailures(t *testing.T) {
	create := func() error { return CreateSambaUser("alice", "secret") }
	edit := func() error { return EditSambaUser("alice", "newsecret") }
	tests := []struct {
		name       string
		call       func() error
		unixExists bool
		unixErr    error
		commandOut string // pdbedit/smbpasswd -x output
		commandErr error
		inputErr   error // smbpasswd password prompt
		wantErr    string
	}{
		{name: "create unix user check fails", call: create, unixErr: errors.New("lookup failed"), wantErr: "failed to check if user exists"},
		{name: "create unix user missing", call: create, wantErr: "user alice does not exist in the system"},
		{name: "create smbpasswd fails", call: create, unixExists: true, inputErr: errors.New("exit status 1"), wantErr: "smbpasswd -a alice failed"},
		{name: "edit unix user check fails", call: edit, unixErr: errors.New("lookup failed"), wantErr: "failed to check if user exists"},
		{name: "edit unix user missing", call: edit, wantErr: "user alice does not exist in the system"},
		{
			name: "edit smbpasswd fails", call: edit, unixExists: true, commandOut: "alice:1000:",
			inputErr: errors.New("exit status 1"), wantErr: "smbpasswd change alice failed",
		},
		{
			name: "delete fails", call: func() error { return DeleteSambaUser("alice") },
			commandOut: "delete failed", commandErr: errors.New("exit status 1"), wantErr: "smbpasswd -x alice failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer resetMocks()
			unixUserExists = func(string) (bool, error) { return tt.unixExists, tt.unixErr }
			runCommand = func(string, ...string) (string, error) { return tt.commandOut, tt.commandErr }
			runCommandWithInput = func(string, string, ...string) (string, error) { return "failed", tt.inputErr }

			err := tt.call()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected %q, got %v", tt.wantErr, err)
			}
		})
	}
}
