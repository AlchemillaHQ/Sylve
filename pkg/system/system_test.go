package system

import (
	"errors"
	"reflect"
	"testing"
)

func resetTestHooks() {
	runCommand = nil
	unixUserExists = nil
	unixGroupExists = nil
	isUserInGroup = nil
	getEUID = nil

	runCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}
	unixUserExists = UnixUserExists
	unixGroupExists = UnixGroupExists
	isUserInGroup = IsUserInGroup
	getEUID = func() int { return 1000 }
}

func TestUnixUserExists(t *testing.T) {
	t.Run("user exists", func(t *testing.T) {
		defer resetTestHooks()
		runCommand = func(command string, args ...string) (string, error) {
			if command != "/usr/bin/id" {
				t.Fatalf("unexpected command: %s", command)
			}
			if !reflect.DeepEqual(args, []string{"alice"}) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "uid=1001(alice) gid=1001(alice)", nil
		}

		exists, err := UnixUserExists("alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Fatalf("expected user to exist")
		}
	})

	t.Run("user missing no such user", func(t *testing.T) {
		defer resetTestHooks()
		runCommand = func(command string, args ...string) (string, error) {
			return "id: bob: no such user", errors.New("exit status 1")
		}

		exists, err := UnixUserExists("bob")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Fatalf("expected user not to exist")
		}
	})

	t.Run("user missing does not exist", func(t *testing.T) {
		defer resetTestHooks()
		runCommand = func(command string, args ...string) (string, error) {
			return "user does not exist", errors.New("exit status 1")
		}

		exists, err := UnixUserExists("bob")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Fatalf("expected user not to exist")
		}
	})

	t.Run("unexpected error", func(t *testing.T) {
		defer resetTestHooks()
		wantErr := errors.New("boom")
		runCommand = func(command string, args ...string) (string, error) {
			return "some other failure", wantErr
		}

		exists, err := UnixUserExists("alice")
		if err == nil {
			t.Fatalf("expected error")
		}
		if exists {
			t.Fatalf("expected user not to exist")
		}
	})
}

func TestCreateUnixUser(t *testing.T) {
	t.Run("success with explicit values and group", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool {
			return name == "staff"
		}

		runCommand = func(command string, args ...string) (string, error) {
			if command != "/usr/sbin/pw" {
				t.Fatalf("unexpected command: %s", command)
			}

			want := []string{
				"user", "add", "-n", "alice", "-m",
				"-g", "staff",
				"-s", "/bin/sh",
				"-d", "/home/alice",
			}

			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args:\n got: %#v\nwant: %#v", args, want)
			}
			return "", nil
		}

		err := CreateUnixUser("alice", "/bin/sh", "/home/alice", "staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("success with defaults", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			want := []string{
				"user", "add", "-n", "alice",
				"-s", "/usr/sbin/nologin",
				"-d", "/nonexistent",
			}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args:\n got: %#v\nwant: %#v", args, want)
			}
			return "", nil
		}

		err := CreateUnixUser("alice", "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("group does not exist", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return false }

		err := CreateUnixUser("alice", "", "", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("pw failure", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("pw failed")
		}

		err := CreateUnixUser("alice", "", "", "")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestDeleteUnixUser(t *testing.T) {
	t.Run("without remove home", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"userdel", "alice"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := DeleteUnixUser("alice", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("with remove home", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"userdel", "alice", "-r"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := DeleteUnixUser("alice", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("pw failure", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("delete failed")
		}

		err := DeleteUnixUser("alice", false)
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestUnixGroupExists(t *testing.T) {
	t.Run("group exists", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "staff:*:1001:", nil
		}

		if !UnixGroupExists("staff") {
			t.Fatalf("expected group to exist")
		}
	})

	t.Run("group missing", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("not found")
		}

		if UnixGroupExists("staff") {
			t.Fatalf("expected group not to exist")
		}
	})
}

func TestCreateUnixGroup(t *testing.T) {
	t.Run("already exists", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return true }

		err := CreateUnixGroup("staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("success", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return false }
		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"groupadd", "staff"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := CreateUnixGroup("staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return false }
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("groupadd failed")
		}

		err := CreateUnixGroup("staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestDeleteUnixGroup(t *testing.T) {
	t.Run("does not exist", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return false }

		err := DeleteUnixGroup("staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("success", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"groupdel", "staff"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := DeleteUnixGroup("staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("groupdel failed")
		}

		err := DeleteUnixGroup("staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestIsUserInGroup(t *testing.T) {
	t.Run("user missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return false, nil }

		ok, err := IsUserInGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
		if ok {
			t.Fatalf("expected false")
		}
	})

	t.Run("group missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return false }

		ok, err := IsUserInGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
		if ok {
			t.Fatalf("expected false")
		}
	})

	t.Run("membership true", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			return "wheel staff video", nil
		}

		ok, err := IsUserInGroup("alice", "staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatalf("expected true")
		}
	})

	t.Run("membership false", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			return "wheel video", nil
		}

		ok, err := IsUserInGroup("alice", "staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Fatalf("expected false")
		}
	})

	t.Run("id command fails", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("id failed")
		}

		ok, err := IsUserInGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
		if ok {
			t.Fatalf("expected false")
		}
	})
}

func TestAddUserToGroup(t *testing.T) {
	t.Run("user missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return false, nil }

		err := AddUserToGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("group missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return false }

		err := AddUserToGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("success", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"groupmod", "staff", "-m", "alice"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := AddUserToGroup("alice", "staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("groupmod failed")
		}

		err := AddUserToGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestRemoveUserFromGroup(t *testing.T) {
	t.Run("user missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return false, nil }

		err := RemoveUserFromGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("group missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return false }

		err := RemoveUserFromGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("membership check fails", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		isUserInGroup = func(user, group string) (bool, error) {
			return false, errors.New("check failed")
		}

		err := RemoveUserFromGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("not in group no-op", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		isUserInGroup = func(user, group string) (bool, error) {
			return false, nil
		}

		called := false
		runCommand = func(command string, args ...string) (string, error) {
			called = true
			return "", nil
		}

		err := RemoveUserFromGroup("alice", "staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if called {
			t.Fatalf("did not expect pw command to be called")
		}
	})

	t.Run("success", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		isUserInGroup = func(user, group string) (bool, error) {
			return true, nil
		}
		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"groupmod", "staff", "-d", "alice"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := RemoveUserFromGroup("alice", "staff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) { return true, nil }
		unixGroupExists = func(name string) bool { return true }
		isUserInGroup = func(user, group string) (bool, error) {
			return true, nil
		}
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("groupmod failed")
		}

		err := RemoveUserFromGroup("alice", "staff")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestRenameGroup(t *testing.T) {
	t.Run("old group missing", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool {
			return false
		}

		err := RenameGroup("old", "new")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("new group already exists", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool {
			return name == "old" || name == "new"
		}

		err := RenameGroup("old", "new")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("success", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool {
			return name == "old"
		}
		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"groupmod", "old", "-n", "new"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := RenameGroup("old", "new")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		defer resetTestHooks()

		unixGroupExists = func(name string) bool {
			return name == "old"
		}
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("rename failed")
		}

		err := RenameGroup("old", "new")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestChangeUsername(t *testing.T) {
	t.Run("old user missing", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) {
			return false, nil
		}

		err := ChangeUsername("old", "new")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("new user already exists", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) {
			if name == "old" {
				return true, nil
			}
			if name == "new" {
				return true, nil
			}
			return false, nil
		}

		err := ChangeUsername("old", "new")
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("success", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) {
			if name == "old" {
				return true, nil
			}
			if name == "new" {
				return false, nil
			}
			return false, nil
		}
		runCommand = func(command string, args ...string) (string, error) {
			want := []string{"usermod", "old", "-l", "new"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return "", nil
		}

		err := ChangeUsername("old", "new")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		defer resetTestHooks()

		unixUserExists = func(name string) (bool, error) {
			if name == "old" {
				return true, nil
			}
			if name == "new" {
				return false, nil
			}
			return false, nil
		}
		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("usermod failed")
		}

		err := ChangeUsername("old", "new")
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestPixzExists(t *testing.T) {
	t.Run("exists", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "/usr/local/bin/pixz", nil
		}

		if !PixzExists() {
			t.Fatalf("expected pixz to exist")
		}
	})

	t.Run("not found", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("pixz: not found")
		}

		if PixzExists() {
			t.Fatalf("expected pixz not to exist")
		}
	})

	t.Run("other error", func(t *testing.T) {
		defer resetTestHooks()

		runCommand = func(command string, args ...string) (string, error) {
			return "", errors.New("permission denied")
		}

		if PixzExists() {
			t.Fatalf("expected pixz not to exist")
		}
	})
}

func TestIsRoot(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		defer resetTestHooks()

		getEUID = func() int { return 0 }

		if !IsRoot() {
			t.Fatalf("expected root")
		}
	})

	t.Run("not root", func(t *testing.T) {
		defer resetTestHooks()

		getEUID = func() int { return 1000 }

		if IsRoot() {
			t.Fatalf("expected non-root")
		}
	})
}
