// SPDX-License-Identifier: BSD-2-Clause

package remoteexec

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

func testSSHSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestSSHHostKeyParsing(t *testing.T) {
	ecdsaPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaKey, err := ssh.NewPublicKey(&ecdsaPrivate.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := ssh.NewPublicKey(&rsaPrivate.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []ssh.PublicKey{testSSHSigner(t).PublicKey(), ecdsaKey, rsaKey} {
		canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		got, err := CanonicalSSHHostKey(canonical + " a comment")
		if err != nil || got != canonical {
			t.Fatalf("canonical key=%q err=%v", got, err)
		}
		fingerprint, err := SSHHostKeyFingerprint(got)
		if err != nil || fingerprint != ssh.FingerprintSHA256(key) {
			t.Fatalf("fingerprint=%q err=%v", fingerprint, err)
		}
		for _, invalid := range []string{"", "broken", "@cert-authority " + canonical, "@revoked " + canonical,
			"no-pty " + canonical, "host " + canonical, canonical + "\n" + canonical, canonical + "\x00"} {
			if _, err := CanonicalSSHHostKey(invalid); err == nil {
				t.Fatalf("accepted invalid key: %q", invalid)
			}
		}
	}
}

func TestManagedSSHHostKeyRepairAndPolicy(t *testing.T) {
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(testSSHSigner(t).PublicKey())))
	dir := filepath.Join(t.TempDir(), "host keys with spaces")
	loginKey := filepath.Join(t.TempDir(), "login key")
	if err := os.WriteFile(loginKey, []byte("first login key"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"root@backup.example", "root@192.0.2.1", "root@[2001:db8::1]"} {
		trust := SSHHostTrust{Scope: "backup", Target: "42", Endpoint: endpoint, Port: 2222, PublicKey: key, Revision: 1}
		path, err := PrepareSSHHostKey(dir, trust)
		if err != nil {
			t.Fatal(err)
		}
		wanted := trust.Alias() + " " + key + "\n"
		for _, repair := range []string{"damaged", "missing", "permissions", "symlink"} {
			switch repair {
			case "damaged":
				err = os.WriteFile(path, []byte("invalid"), 0600)
			case "missing":
				err = os.Remove(path)
			case "permissions":
				err = os.Chmod(path, 0644)
			case "symlink":
				other := filepath.Join(dir, "untouched")
				if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(other, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			repaired, err := PrepareSSHHostKey(dir, trust)
			if err != nil || repaired != path {
				t.Fatalf("repair=%s path=%q err=%v", repair, repaired, err)
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != wanted {
				t.Fatalf("repair=%s content=%q err=%v", repair, content, err)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
				t.Fatalf("unsafe file after %s: %v %v", repair, info, err)
			}
		}
		args, err := SSHHostKeyOptions(trust, path, loginKey, false, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, option := range []string{"StrictHostKeyChecking=yes", "GlobalKnownHostsFile=/dev/null", "KnownHostsCommand=none",
			"VerifyHostKeyDNS=no", "CheckHostIP=no", "UpdateHostKeys=no", "HostKeyAlias=" + trust.Alias(), "ControlMaster=auto"} {
			if !slices.Contains(args, option) {
				t.Fatalf("missing %q in %v", option, args)
			}
		}
		if err := os.WriteFile(loginKey, []byte("replacement login key"), 0600); err != nil {
			t.Fatal(err)
		}
		replaced, err := SSHHostKeyOptions(trust, path, loginKey, false, false)
		if err != nil || strings.Join(replaced, " ") == strings.Join(args, " ") {
			t.Fatalf("login-key replacement reused control identity: %v", err)
		}
		if err := os.WriteFile(loginKey, []byte("first login key"), 0600); err != nil {
			t.Fatal(err)
		}
		reset := trust
		reset.Revision++
		if trust.identity(loginKey) == reset.identity(loginKey) {
			t.Fatal("reset shares the old control identity")
		}
		reset = trust
		reset.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(testSSHSigner(t).PublicKey())))
		if trust.identity(loginKey) == reset.identity(loginKey) {
			t.Fatal("replacement key shares the old control identity")
		}
		fresh, err := SSHHostKeyOptions(trust, path, "", true, false)
		if err != nil || !slices.Contains(fresh, "ControlPath=none") || !slices.Contains(fresh, "ControlMaster=no") {
			t.Fatalf("fresh options=%v err=%v", fresh, err)
		}
		learning, err := SSHHostKeyOptions(trust, path, "", false, true)
		if err != nil || !slices.Contains(learning, "StrictHostKeyChecking=accept-new") || !slices.Contains(learning, "ControlPath=none") {
			t.Fatalf("learning=%v err=%v", learning, err)
		}
		cluster := trust
		cluster.Scope = "cluster"
		if _, err := SSHHostKeyOptions(cluster, path, "", false, true); err == nil {
			t.Fatal("cluster enrollment was allowed")
		}
		if _, err := ReadLearnedSSHHostKey(path, trust); err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []string{"other " + key + "\n", wanted + wanted, "@revoked " + wanted, trust.Alias() + ",other " + key + "\n"} {
			if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadLearnedSSHHostKey(path, trust); err == nil {
				t.Fatalf("accepted learning file %q", invalid)
			}
		}
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode: %v %v", info, err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareSSHHostKey(blocked, SSHHostTrust{Scope: "backup", Target: "42", Endpoint: "backup", Port: 22, PublicKey: key})
	if err == nil || !strings.Contains(err.Error(), "ssh_host_key_file_unavailable") {
		t.Fatalf("write failure=%v", err)
	}
}

func TestSSHHostKeyDiagnostics(t *testing.T) {
	if err := SSHHostKeyError("backup", "target", "Host key verification failed.", nil); err != nil {
		t.Fatal("successful output was classified as an SSH failure")
	}
	for _, output := range []string{"REMOTE HOST IDENTIFICATION HAS CHANGED!", "Host key verification failed.", "No ED25519 host key is known for backup and you have requested strict checking."} {
		if !IsSSHHostKeyFailure(output) {
			t.Fatalf("unclassified failure: %s", output)
		}
		if err := SSHHostKeyError("backup", "Backup-01", output, fmt.Errorf("exit status 255")); !strings.Contains(err.Error(), "Reset host key") {
			t.Fatal(err)
		}
		if err := SSHHostKeyError("cluster", "node-a", output, fmt.Errorf("exit status 255")); !strings.Contains(err.Error(), "cluster_ssh_host_key_mismatch") || strings.Contains(err.Error(), "Reset host key") {
			t.Fatal(err)
		}
	}
	for _, output := range []string{"Permission denied (publickey).", "Connection timed out", "Connection refused"} {
		if IsSSHHostKeyFailure(output) {
			t.Fatalf("misclassified failure: %s", output)
		}
	}
	if len(SSHDiagnostic(strings.Repeat("a", 10000))) > 4100 {
		t.Fatal("unbounded diagnostic")
	}
}

func TestManagedHostPolicyWithRealOpenSSH(t *testing.T) {
	binary, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mutex sync.Mutex
	signers := []ssh.Signer{testSSHSigner(t)}
	rejectLogin := false
	var connections []net.Conn
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mutex.Lock()
			config := &ssh.ServerConfig{NoClientAuth: !rejectLogin, PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
				return nil, errors.New("login denied")
			}}
			for _, signer := range signers {
				config.AddHostKey(signer)
			}
			connections = append(connections, connection)
			mutex.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connection.Close()
				server, channels, requests, err := ssh.NewServerConn(connection, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						_ = incoming.Reject(ssh.UnknownChannelType, "session required")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						continue
					}
					for request := range requests {
						if request.Type != "exec" {
							_ = request.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
							_ = channel.Close()
							break
						}
						_ = request.Reply(true, nil)
						cmd := exec.Command("/bin/sh", "-c", payload.Command)
						cmd.Stdin, cmd.Stdout, cmd.Stderr = channel, channel, channel.Stderr()
						status := uint32(0)
						if err := cmd.Run(); err != nil {
							status = 1
						}
						_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						_ = channel.Close()
						break
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mutex.Lock()
		for _, connection := range connections {
			_ = connection.Close()
		}
		mutex.Unlock()
		workers.Wait()
	})
	port := listener.Addr().(*net.TCPAddr).Port
	dir := filepath.Join(t.TempDir(), "SSH paths with spaces")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	trust := SSHHostTrust{Scope: "backup", Target: "42", Endpoint: "test@127.0.0.1", Port: port,
		PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signers[0].PublicKey()))), Revision: 1}
	systemFile := filepath.Join(dir, "system known hosts")
	systemContent := fmt.Sprintf("[%s]:%d %s\ninvalid text\n", "127.0.0.1", port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(testSSHSigner(t).PublicKey()))))
	if err := os.WriteFile(systemFile, []byte(systemContent), 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(dir, "config")
	config := fmt.Sprintf("Host *\n UserKnownHostsFile \"%s\"\n GlobalKnownHostsFile \"%s\"\n HostKeyAlias inherited-alias\n KnownHostsCommand /bin/false\n VerifyHostKeyDNS yes\n UpdateHostKeys yes\n CheckHostIP yes\n ControlMaster auto\n ControlPath \"%s\"\n ControlPersist 60\n", systemFile, systemFile, filepath.Join(dir, "inherited.sock"))
	if err := os.WriteFile(configFile, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(trust SSHHostTrust, fresh, learning bool, path, input string) (string, error) {
		t.Helper()
		options, err := SSHHostKeyOptions(trust, path, "", fresh, learning)
		if err != nil {
			t.Fatal(err)
		}
		if !fresh && !learning {
			for _, option := range options {
				if strings.HasPrefix(option, "ControlPath=") {
					socket := strings.TrimPrefix(option, "ControlPath=")
					t.Cleanup(func() { _ = exec.Command(binary, "-S", socket, "-O", "exit", "test@127.0.0.1").Run() })
				}
			}
		}
		command, err := NewCommand("/bin/cat")
		if err != nil {
			t.Fatal(err)
		}
		destination, err := ParseSSHDestination(trust.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		options = append(options, "-F", configFile, "-p", strconv.Itoa(port), "-o", "ConnectTimeout=3", "-o", "LogLevel=ERROR")
		args, err := command.SSHArgs(options, destination, true)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	path, err := PrepareSSHHostKey(dir, trust)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(trust, false, false, path, "stream input\n"); err != nil || output != "stream input\n" {
		t.Fatalf("approved key: %v %q", err, output)
	}
	if output, err := run(trust, false, false, path, "reused connection\n"); err != nil || output != "reused connection\n" {
		t.Fatalf("connection reuse: %v %q", err, output)
	}
	mutex.Lock()
	connectionCount := len(connections)
	mutex.Unlock()
	if connectionCount != 1 {
		t.Fatalf("normal commands opened %d connections instead of reusing one", connectionCount)
	}
	ecdsaPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaPrivate)
	if err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	signers = append(signers, ecdsaSigner)
	mutex.Unlock()
	if output, err := run(trust, true, false, path, "multiple algorithms\n"); err != nil || output != "multiple algorithms\n" {
		t.Fatalf("algorithm negotiation: %v %q", err, output)
	}
	mutex.Lock()
	signers = []ssh.Signer{testSSHSigner(t)}
	newKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signers[0].PublicKey())))
	mutex.Unlock()
	if output, err := run(trust, true, false, path, ""); err == nil || !IsSSHHostKeyFailure(output) {
		t.Fatalf("unexpected replacement accepted: %v %q", err, output)
	}
	learningFile := filepath.Join(dir, "learning file")
	if err := os.WriteFile(learningFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := run(trust, true, true, learningFile, "enrollment\n"); err != nil || output != "enrollment\n" {
		t.Fatalf("enrollment failed: %v %q", err, output)
	}
	learned, err := ReadLearnedSSHHostKey(learningFile, trust)
	if err != nil || learned != newKey {
		t.Fatalf("learned=%q err=%v", learned, err)
	}
	replacement := trust
	replacement.PublicKey, replacement.Revision = learned, trust.Revision+1
	if trust.identity("") == replacement.identity("") {
		t.Fatal("replacement reused old socket")
	}
	path, err = PrepareSSHHostKey(dir, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(replacement, false, false, path, "strict replacement\n"); err != nil || output != "strict replacement\n" {
		t.Fatalf("strict replacement: %v %q", err, output)
	}
	cluster := replacement
	cluster.Scope, cluster.Target = "cluster", "node-a"
	clusterPath, err := PrepareSSHHostKey(dir, cluster)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(cluster, true, false, clusterPath, "cluster\n"); err != nil || output != "cluster\n" {
		t.Fatalf("published cluster identity: %v %q", err, output)
	}
	mutex.Lock()
	signers = []ssh.Signer{testSSHSigner(t)}
	clusterReplacementKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signers[0].PublicKey())))
	mutex.Unlock()
	if output, err := run(cluster, true, false, clusterPath, ""); err == nil || !IsSSHHostKeyFailure(output) {
		t.Fatalf("unpublished cluster replacement accepted: %v %q", err, output)
	}
	cluster.PublicKey = clusterReplacementKey
	clusterPath, err = PrepareSSHHostKey(dir, cluster)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(cluster, true, false, clusterPath, "approved cluster replacement\n"); err != nil || output != "approved cluster replacement\n" {
		t.Fatalf("cluster publication: %v %q", err, output)
	}
	mutex.Lock()
	rejectLogin = true
	mutex.Unlock()
	if err := os.WriteFile(learningFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	failedLoginTrust := replacement
	failedLoginTrust.PublicKey = clusterReplacementKey
	if output, err := run(failedLoginTrust, true, true, learningFile, ""); err == nil || IsSSHHostKeyFailure(output) || !strings.Contains(output, "Permission denied") {
		t.Fatalf("login failure: %v %q", err, output)
	}
	if key, err := ReadLearnedSSHHostKey(learningFile, failedLoginTrust); err != nil || key != clusterReplacementKey {
		t.Fatalf("OpenSSH login failure did not leave a pre-login key: %q %v", key, err)
	}
	if raw, err := os.ReadFile(systemFile); err != nil || !reflect.DeepEqual(raw, []byte(systemContent)) {
		t.Fatalf("system file changed: %q %v", raw, err)
	}
}
