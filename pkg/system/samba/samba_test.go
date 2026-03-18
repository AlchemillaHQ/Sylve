package samba

import (
	"errors"
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

func TestCreateSambaUser_UnixUserCheckFails(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		return false, errors.New("lookup failed")
	}

	err := CreateSambaUser("alice", "secret")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "failed to check if user exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSambaUser_UnixUserMissing(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		return false, nil
	}

	err := CreateSambaUser("alice", "secret")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "user alice does not exist in the system") {
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

func TestCreateSambaUser_SmbpasswdFails(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		return true, nil
	}

	runCommandWithInput = func(command, input string, args ...string) (string, error) {
		return "failed to add entry", errors.New("exit status 1")
	}

	err := CreateSambaUser("alice", "secret")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "smbpasswd -a alice failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEditSambaUser_UnixUserCheckFails(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		return false, errors.New("lookup failed")
	}

	err := EditSambaUser("alice", "newsecret")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "failed to check if user exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEditSambaUser_UnixUserMissing(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		return false, nil
	}

	err := EditSambaUser("alice", "newsecret")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "user alice does not exist in the system") {
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

func TestEditSambaUser_SmbpasswdFails(t *testing.T) {
	defer resetMocks()

	unixUserExists = func(name string) (bool, error) {
		return true, nil
	}

	runCommandWithInput = func(command, input string, args ...string) (string, error) {
		return "password change failed", errors.New("exit status 1")
	}

	err := EditSambaUser("alice", "newsecret")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "smbpasswd change alice failed") {
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

func TestDeleteSambaUser_Fails(t *testing.T) {
	defer resetMocks()

	runCommand = func(command string, args ...string) (string, error) {
		return "delete failed", errors.New("exit status 1")
	}

	err := DeleteSambaUser("alice")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "smbpasswd -x alice failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
