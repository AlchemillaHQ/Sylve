package system

import (
	"fmt"
	"os"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

var runCommand = utils.RunCommand
var unixUserExists = UnixUserExists
var unixGroupExists = UnixGroupExists
var isUserInGroup = IsUserInGroup
var getEUID = os.Geteuid

func UnixUserExists(name string) (bool, error) {
	output, err := runCommand("/usr/bin/id", name)

	if err != nil {
		lowerOutput := strings.ToLower(output)
		if strings.Contains(lowerOutput, "no such user") || strings.Contains(lowerOutput, "does not exist") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func CreateUnixUser(name string, shell string, dir string, group string) error {
	args := []string{"user", "add", "-n", name, "-m"}

	if group != "" {
		if unixGroupExists(group) {
			args = append(args, "-g", group)
		} else {
			return fmt.Errorf("specified group '%s' does not exist", group)
		}
	}

	if shell != "" {
		args = append(args, "-s", shell)
	} else {
		args = append(args, "-s", "/usr/sbin/nologin")
	}

	if dir != "" {
		args = append(args, "-d", dir)
	} else {
		args = append(args, "-d", "/nonexistent")
	}

	_, err := runCommand("/usr/sbin/pw", args...)
	if err != nil {
		return fmt.Errorf("pw command failed: %w", err)
	}

	return nil
}

func DeleteUnixUser(name string, removeHome bool) error {
	args := []string{"userdel", name}

	if removeHome {
		args = append(args, "-r")
	}

	_, err := runCommand("/usr/sbin/pw", args...)
	if err != nil {
		return fmt.Errorf("failed to delete user %s: %w", name, err)
	}

	return nil
}

func UnixGroupExists(name string) bool {
	output, err := runCommand("/usr/bin/getent", "group", name)

	if err != nil {
		return false
	}

	return strings.TrimSpace(output) != ""
}

func CreateUnixGroup(name string) error {
	if exists := unixGroupExists(name); exists {
		return fmt.Errorf("group %s already exists", name)
	}

	_, err := runCommand("/usr/sbin/pw", "groupadd", name)
	if err != nil {
		return fmt.Errorf("failed to create group %s: %w", name, err)
	}

	return nil
}

func DeleteUnixGroup(name string) error {
	if exists := unixGroupExists(name); !exists {
		return fmt.Errorf("group %s does not exist", name)
	}

	_, err := runCommand("/usr/sbin/pw", "groupdel", name)
	if err != nil {
		return fmt.Errorf("failed to delete group %s: %w", name, err)
	}

	return nil
}

func IsUserInGroup(user string, group string) (bool, error) {
	if exists, _ := unixUserExists(user); !exists {
		return false, fmt.Errorf("user %s does not exist", user)
	}

	if exists := unixGroupExists(group); !exists {
		return false, fmt.Errorf("group %s does not exist", group)
	}

	output, err := runCommand("/usr/bin/id", "-nG", user)
	if err != nil {
		return false, fmt.Errorf("failed to check group membership for user %s: %w", user, err)
	}

	groups := strings.Fields(output)
	for _, g := range groups {
		if g == group {
			return true, nil
		}
	}

	return false, nil
}

func AddUserToGroup(user string, group string) error {
	if exists, _ := unixUserExists(user); !exists {
		return fmt.Errorf("user %s does not exist", user)
	}

	if exists := unixGroupExists(group); !exists {
		return fmt.Errorf("group %s does not exist", group)
	}

	_, err := runCommand("/usr/sbin/pw", "groupmod", group, "-m", user)
	if err != nil {
		return fmt.Errorf("failed to add user %s to group %s: %w", user, group, err)
	}

	return nil
}

func RemoveUserFromGroup(user string, group string) error {
	if exists, _ := unixUserExists(user); !exists {
		return fmt.Errorf("user %s does not exist", user)
	}
	if exists := unixGroupExists(group); !exists {
		return fmt.Errorf("group %s does not exist", group)
	}

	inGroup, err := isUserInGroup(user, group)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}

	if !inGroup {
		return nil
	}

	_, err = runCommand("/usr/sbin/pw", "groupmod", group, "-d", user)
	if err != nil {
		return fmt.Errorf("failed to remove user %s from group %s: %w", user, group, err)
	}

	return nil
}

func RenameGroup(oldName, newName string) error {
	if exists := unixGroupExists(oldName); !exists {
		return fmt.Errorf("group %s does not exist", oldName)
	}

	if exists := unixGroupExists(newName); exists {
		return fmt.Errorf("group %s already exists", newName)
	}

	_, err := runCommand("/usr/sbin/pw", "groupmod", oldName, "-n", newName)
	if err != nil {
		return fmt.Errorf("failed to rename group %s to %s: %w", oldName, newName, err)
	}

	return nil
}

func ChangeUsername(oldUsername, newUsername string) error {
	if exists, _ := unixUserExists(oldUsername); !exists {
		return fmt.Errorf("user %s does not exist", oldUsername)
	}

	if exists, _ := unixUserExists(newUsername); exists {
		return fmt.Errorf("user %s already exists", newUsername)
	}

	_, err := runCommand("/usr/sbin/pw", "usermod", oldUsername, "-l", newUsername)
	if err != nil {
		return fmt.Errorf("failed to change username from %s to %s: %w", oldUsername, newUsername, err)
	}

	return nil
}

func PixzExists() bool {
	_, err := runCommand("which", "pixz")
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false
		}
		return false
	}

	return true
}

func IsRoot() bool {
	return getEUID() == 0
}
