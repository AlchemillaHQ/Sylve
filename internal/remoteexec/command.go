// SPDX-License-Identifier: BSD-2-Clause

package remoteexec

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

type Command struct{ script string }

func NewCommand(argv ...string) (Command, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return Command{}, errors.New("remote_command_required")
	}

	var script strings.Builder
	script.WriteString("exec")
	for _, arg := range argv {
		if !validShellText(arg) {
			return Command{}, errors.New("remote_command_argument_invalid")
		}
		script.WriteByte(' ')
		script.WriteString(quotePOSIX(arg))
	}
	return Command{script: script.String()}, nil
}

func NewScript(script string) (Command, error) {
	if strings.TrimSpace(script) == "" {
		return Command{}, errors.New("remote_script_required")
	}
	if !validShellText(script) {
		return Command{}, errors.New("remote_script_invalid")
	}
	return Command{script: script}, nil
}

func (command Command) SSHArgument() (string, error) {
	if command.script == "" {
		return "", errors.New("remote_command_required")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(command.script))
	return `/bin/sh -c 'eval "$(/usr/bin/printf %s "$1" | /usr/bin/base64 -d)"' sh ` + encoded, nil
}

func (command Command) SSHArgs(base []string, destination SSHDestination, readsStdin bool) ([]string, error) {
	if destination.String() == "" {
		return nil, errors.New("invalid_ssh_destination")
	}
	remoteArgument, err := command.SSHArgument()
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, len(base)+2)
	for _, arg := range base {
		if readsStdin && arg == "-n" {
			continue
		}
		args = append(args, arg)
	}
	return append(args, destination.String(), remoteArgument), nil
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validShellText(value string) bool {
	return !strings.ContainsRune(value, 0) && utf8.ValidString(value)
}

type SSHHostTrust struct {
	Scope     string
	Target    string
	Endpoint  string
	Port      int
	PublicKey string
	Revision  uint64
}

func CanonicalSSHHostKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 16*1024 || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("invalid_ssh_host_key")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(raw))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return "", errors.New("invalid_ssh_host_key")
	}
	if _, certificate := key.(*ssh.Certificate); certificate {
		return "", errors.New("ssh_host_certificate_not_supported")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}

func SSHHostKeyFingerprint(raw string) (string, error) {
	canonical, err := CanonicalSSHHostKey(raw)
	if err != nil {
		return "", err
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(canonical))
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(key), nil
}

func (trust SSHHostTrust) Alias() string {
	sum := sha256.Sum256([]byte(trust.Scope + "\x00" + trust.Target))
	return fmt.Sprintf("sylve-%x", sum[:16])
}

func (trust SSHHostTrust) identity(loginKey string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("host-policy-v1\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s",
		trust.Scope, trust.Target, trust.Endpoint, trust.Port, trust.Revision, trust.PublicKey, loginKey)))
	return fmt.Sprintf("%x", sum[:16])
}

func privateSSHDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid_ssh_host_key_directory")
	}
	return os.Chmod(dir, 0700)
}

func PrepareSSHHostKey(dir string, trust SSHHostTrust) (string, error) {
	canonical, err := CanonicalSSHHostKey(trust.PublicKey)
	if err != nil {
		return "", err
	}
	trust.PublicKey = canonical
	if trust.Scope == "" || trust.Target == "" || trust.Endpoint == "" || trust.Port < 1 || trust.Port > 65535 {
		return "", errors.New("invalid_ssh_host_identity")
	}
	if err := privateSSHDirectory(dir); err != nil {
		return "", fmt.Errorf("ssh_host_key_file_unavailable: Check the Sylve data directory and permissions: %w", err)
	}
	path := filepath.Join(dir, trust.identity("")+".known_hosts")
	content := []byte(trust.Alias() + " " + canonical + "\n")
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0600 {
		if current, readErr := os.ReadFile(path); readErr == nil && string(current) == string(content) {
			return path, nil
		}
	}
	file, err := os.CreateTemp(dir, ".host-key-*")
	if err != nil {
		return "", fmt.Errorf("ssh_host_key_file_unavailable: Check the Sylve data directory and permissions: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return "", fmt.Errorf("ssh_host_key_file_unavailable: Check the Sylve data directory and permissions: %w", err)
	}
	return path, nil
}

func SSHHostKeyOptions(trust SSHHostTrust, path, loginKey string, fresh, learning bool) ([]string, error) {
	if path == "" || !validShellText(path) || strings.ContainsAny(path, "\r\n") {
		return nil, errors.New("invalid_ssh_host_key_path")
	}
	checking := "yes"
	if learning {
		if trust.Scope != "backup" {
			return nil, errors.New("ssh_host_key_enrollment_not_permitted")
		}
		checking = "accept-new"
		fresh = true
	}
	quotedPath := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`
	args := []string{
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=" + checking,
		"-o", "UserKnownHostsFile=" + quotedPath, "-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "KnownHostsCommand=none", "-o", "VerifyHostKeyDNS=no", "-o", "CheckHostIP=no",
		"-o", "UpdateHostKeys=no", "-o", "HashKnownHosts=no", "-o", "HostKeyAlias=" + trust.Alias(),
	}
	if fresh {
		return append(args, "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no"), nil
	}
	if loginKey != "" {
		key, err := os.ReadFile(loginKey)
		if err != nil {
			return nil, fmt.Errorf("ssh_login_key_file_unavailable: Check the SSH login key file and permissions: %w", err)
		}
		digest := sha256.Sum256(key)
		loginKey += fmt.Sprintf("\x00%x", digest)
	}
	socketDir := filepath.Join(os.TempDir(), "sylve-ssh-v1-"+strconv.Itoa(os.Getuid()))
	if err := privateSSHDirectory(socketDir); err != nil {
		return nil, fmt.Errorf("ssh_control_directory_unavailable: %w", err)
	}
	socket := filepath.Join(socketDir, trust.identity(loginKey)+".sock")
	if len(socket) >= 104 {
		return nil, errors.New("ssh_control_path_too_long")
	}
	return append(args, "-o", "ControlMaster=auto", "-o", "ControlPath="+socket, "-o", "ControlPersist=60"), nil
}

func ReadLearnedSSHHostKey(path string, trust SSHHostTrust) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil || len(content) > 16*1024 {
		return "", errors.New("ssh_host_key_enrollment_failed")
	}
	marker, hosts, key, _, rest, err := ssh.ParseKnownHosts(content)
	if err != nil || marker != "" || len(hosts) != 1 || hosts[0] != trust.Alias() || len(strings.TrimSpace(string(rest))) != 0 {
		return "", errors.New("ssh_host_key_enrollment_failed")
	}
	return CanonicalSSHHostKey(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
}

func IsSSHHostKeyFailure(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "remote host identification has changed") || strings.Contains(output, "host key verification failed") ||
		(strings.Contains(output, "host key is known") && strings.Contains(output, "strict checking"))
}

func SSHHostKeyError(scope, target, output string, err error) error {
	if err == nil || !IsSSHHostKeyFailure(output) {
		return err
	}
	if scope == "cluster" {
		return fmt.Errorf("cluster_ssh_host_key_mismatch: The host key for %s does not match its published cluster identity. Reconcile that node's SSH identity, then retry: %w", target, err)
	}
	return fmt.Errorf("backup_target_host_key_changed: The SSH host key for %s changed. If this change is expected, select Reset host key, then retry: %w", target, err)
}

func SSHDiagnostic(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 4096 {
		output = output[:4096] + "..."
	}
	return output
}

func ShellCommandString(argv ...string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = quotePOSIX(arg)
	}
	return strings.Join(quoted, " ")
}
