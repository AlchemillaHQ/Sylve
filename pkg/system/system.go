package system

import (
	"fmt"
	"os"
	"strconv"
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
	args := []string{"user", "add", "-n", name}

	if dir != "" && dir != "/nonexistent" {
		args = append(args, "-m")
	}

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

// UnixUserCreateOpts holds parameters for CreateUnixUserFull.
type UnixUserCreateOpts struct {
	Name       string
	Shell      string
	Dir        string
	Group      string
	UID        int  // 0 = let pw auto-assign
	CreateHome bool // create home directory
}

// CreateUnixUserFull creates a Unix user with full option support.
func CreateUnixUserFull(opts UnixUserCreateOpts) error {
	args := []string{"user", "add", "-n", opts.Name}

	if opts.UID > 0 {
		args = append(args, "-u", strconv.Itoa(opts.UID))
	}

	if opts.CreateHome && opts.Dir != "" && opts.Dir != "/nonexistent" {
		args = append(args, "-m")
	}

	if opts.Group != "" {
		if unixGroupExists(opts.Group) {
			args = append(args, "-g", opts.Group)
		} else {
			return fmt.Errorf("specified group '%s' does not exist", opts.Group)
		}
	}

	if opts.Shell != "" {
		args = append(args, "-s", opts.Shell)
	} else {
		args = append(args, "-s", "/usr/sbin/nologin")
	}

	if opts.Dir != "" {
		args = append(args, "-d", opts.Dir)
	} else {
		args = append(args, "-d", "/nonexistent")
	}

	_, err := runCommand("/usr/sbin/pw", args...)
	if err != nil {
		return fmt.Errorf("pw command failed: %w", err)
	}

	return nil
}

// GetNextUnixUID returns the next available UID >= 1000.
func GetNextUnixUID() (int, error) {
	output, err := runCommand("/usr/sbin/pw", "usershow", "-a")
	if err != nil {
		return 1000, nil
	}

	used := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		uid, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil {
			continue
		}
		used[uid] = true
	}

	for uid := 1000; uid < 65534; uid++ {
		if !used[uid] {
			return uid, nil
		}
	}

	return 0, fmt.Errorf("no available UID found in range 1000-65533")
}

// GetUnixUserInfo returns the UID and shell for an existing Unix user.
func GetUnixUserInfo(username string) (uid int, shell string, err error) {
	output, cmdErr := runCommand("/usr/sbin/pw", "usershow", "-n", username)
	if cmdErr != nil {
		return 0, "", fmt.Errorf("failed to get user info for %s: %w", username, cmdErr)
	}
	parts := strings.Split(strings.TrimSpace(output), ":")
	if len(parts) < 7 {
		return 0, "", fmt.Errorf("unexpected pw usershow output for %s", username)
	}
	uid, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, "", fmt.Errorf("failed to parse UID for %s: %w", username, err)
	}
	shell = parts[6]
	return uid, shell, nil
}

// SetUnixUserShell changes the login shell for a Unix user.
func SetUnixUserShell(username, shell string) error {
	_, err := runCommand("/usr/sbin/pw", "usermod", username, "-s", shell)
	if err != nil {
		return fmt.Errorf("failed to set shell for user %s: %w", username, err)
	}
	return nil
}

// LockUnixUser locks a Unix user account.
func LockUnixUser(username string) error {
	_, err := runCommand("/usr/sbin/pw", "lock", username)
	if err != nil {
		return fmt.Errorf("failed to lock user %s: %w", username, err)
	}
	return nil
}

// UnlockUnixUser unlocks a Unix user account.
func UnlockUnixUser(username string) error {
	_, err := runCommand("/usr/sbin/pw", "unlock", username)
	if err != nil {
		return fmt.Errorf("failed to unlock user %s: %w", username, err)
	}
	return nil
}

// DisableUnixUserPassword disables password authentication for a Unix user.
func DisableUnixUserPassword(username string) error {
	_, err := runCommand("/usr/sbin/pw", "usermod", username, "-h", "-")
	if err != nil {
		return fmt.Errorf("failed to disable password for user %s: %w", username, err)
	}
	return nil
}

// WriteSSHAuthorizedKey writes a public key to ~/.ssh/authorized_keys.
func WriteSSHAuthorizedKey(homeDir, key string) error {
	sshDir := homeDir + "/.ssh"
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("failed to create .ssh directory: %w", err)
	}
	keyFile := sshDir + "/authorized_keys"
	if err := os.WriteFile(keyFile, []byte(strings.TrimSpace(key)+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write authorized_keys: %w", err)
	}
	return nil
}

// RemoveSSHAuthorizedKey removes ~/.ssh/authorized_keys for a user.
func RemoveSSHAuthorizedKey(homeDir string) error {
	keyFile := homeDir + "/.ssh/authorized_keys"
	if err := os.Remove(keyFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove authorized_keys: %w", err)
	}
	return nil
}

// DoasAvailable checks whether the doas binary is installed on the system.
func DoasAvailable() bool {
	_, err := os.Stat("/usr/local/bin/doas")
	return err == nil
}

// AddDoasPerm appends a `permit nopass <username>` line to doas.conf.
func AddDoasPerm(username string) error {
	const doasConf = "/usr/local/etc/doas.conf"
	entry := fmt.Sprintf("permit nopass %s\n", username)

	f, err := os.OpenFile(doasConf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open doas.conf: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("failed to write doas.conf: %w", err)
	}
	return nil
}

// RemoveDoasPerm removes the `permit nopass <username>` line from doas.conf.
func RemoveDoasPerm(username string) error {
	const doasConf = "/usr/local/etc/doas.conf"

	data, err := os.ReadFile(doasConf)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read doas.conf: %w", err)
	}

	target := fmt.Sprintf("permit nopass %s\n", username)
	newData := strings.ReplaceAll(string(data), target, "")

	if err := os.WriteFile(doasConf, []byte(newData), 0600); err != nil {
		return fmt.Errorf("failed to write doas.conf: %w", err)
	}
	return nil
}

// ChangeUnixUserHomeDir changes the home directory for a Unix user.
// If createHome is true and the new directory is not /nonexistent, the -m flag
// is passed so that pw(8) creates the directory if it doesn't already exist.
func ChangeUnixUserHomeDir(username, dir string, createHome bool) error {
	args := []string{"usermod", username, "-d", dir}
	if createHome && dir != "" && dir != "/nonexistent" {
		args = append(args, "-m")
	}
	_, err := runCommand("/usr/sbin/pw", args...)
	if err != nil {
		return fmt.Errorf("failed to change home directory for user %s: %w", username, err)
	}
	return nil
}

// ChangeUnixUserUID changes the UID of an existing Unix user.
func ChangeUnixUserUID(username string, uid int) error {
	_, err := runCommand("/usr/sbin/pw", "usermod", username, "-u", strconv.Itoa(uid))
	if err != nil {
		return fmt.Errorf("failed to change UID for user %s: %w", username, err)
	}
	return nil
}

// ChangeUnixUserPrimaryGroup changes the primary group of a Unix user.
func ChangeUnixUserPrimaryGroup(username, group string) error {
	_, err := runCommand("/usr/sbin/pw", "usermod", username, "-g", group)
	if err != nil {
		return fmt.Errorf("failed to change primary group for user %s: %w", username, err)
	}
	return nil
}

// GetUnixGroupGID returns the numeric GID for a group name.
func GetUnixGroupGID(group string) (int, error) {
	output, err := runCommand("/usr/bin/getent", "group", group)
	if err != nil {
		return 0, fmt.Errorf("failed to get group info for %s: %w", group, err)
	}
	parts := strings.Split(strings.TrimSpace(output), ":")
	if len(parts) < 3 {
		return 0, fmt.Errorf("unexpected getent output for group %s", group)
	}
	gid, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("failed to parse GID for %s: %w", group, err)
	}
	return gid, nil
}

// ChownHome recursively chowns a home directory to the given UID and group.
// Skips if the path is empty or /nonexistent.
func ChownHome(homeDir string, uid int, groupName string) error {
	if homeDir == "" || homeDir == "/nonexistent" {
		return nil
	}

	gid, err := GetUnixGroupGID(groupName)
	if err != nil {
		return fmt.Errorf("failed to resolve group %s: %w", groupName, err)
	}

	_, err = runCommand("/usr/sbin/chown", "-R", fmt.Sprintf("%d:%d", uid, gid), homeDir)
	if err != nil {
		return fmt.Errorf("failed to chown %s: %w", homeDir, err)
	}
	return nil
}
