// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type nativeFixture struct {
	driver    *nativeRuntime
	commands  []string
	processes map[int]string
	settings  map[string]string
	exported  bool
	killed    []string
	probes    []uint32
}

func newNativeFixture(t *testing.T) *nativeFixture {
	t.Helper()
	dir := t.TempDir()
	d := newNativeRuntime()
	d.exportsPath, d.backupPath = filepath.Join(dir, "exports"), filepath.Join(dir, "exports.pre-sylve")
	d.nfsdPIDPath, d.mountdPIDPath = filepath.Join(dir, "nfsd.pid"), filepath.Join(dir, "mountd.pid")
	d.available = func() bool { return true }
	d.verifyMount = func(string, string) error { return nil }
	d.addresses = func() ([]string, error) { return []string{"127.0.0.1", "192.0.2.10", "::1"}, nil }
	f := &nativeFixture{driver: d, processes: map[int]string{}, settings: map[string]string{}}
	d.mounts = func() ([]kernelMount, error) {
		return []kernelMount{{Name: "tank/data", Path: "/mnt/data", Exported: f.exported}}, nil
	}
	d.clear = func() error { f.exported = false; return nil }
	d.kill = func(pid int, signal syscall.Signal) error {
		name := f.processes[pid]
		if name != "nfsd" && name != "mountd" {
			t.Fatalf("signalled a shared or unrelated daemon %s (%d)", name, pid)
		}
		if name == "nfsd" && signal != syscall.SIGUSR1 || name == "mountd" && signal != syscall.SIGTERM {
			t.Fatalf("wrong stop signal for %s: %v", name, signal)
		}
		f.killed = append(f.killed, name)
		delete(f.processes, pid)
		return nil
	}
	d.run = func(ctx context.Context, env []string, command string, args ...string) (utils.CommandResult, error) {
		if ctx.Err() != nil {
			return utils.CommandResult{}, ctx.Err()
		}
		if !slices.Contains(env, "LC_ALL=C") || !slices.Contains(env, "PATH=/sbin:/bin:/usr/sbin:/usr/bin") {
			t.Fatal("uncontrolled daemon environment")
		}
		f.commands = append(f.commands, command+" "+strings.Join(args, " "))
		result := utils.CommandResult{}
		switch command {
		case "/usr/bin/pgrep":
			pattern := regexp.MustCompile("^(?:" + args[1] + ")$")
			for pid, name := range f.processes {
				if pattern.MatchString(name) {
					result.Output += strconv.Itoa(pid) + "\n"
				}
			}
			if result.Output == "" {
				return utils.CommandResult{ExitCode: 1}, errors.New("no process")
			}
		case "/bin/ps":
			if strings.Contains(args[1], ",") {
				for _, value := range strings.Split(args[1], ",") {
					pid, _ := strconv.Atoi(value)
					if name := f.processes[pid]; name != "" {
						result.Output += value + " Sun Oct 25 12:00:00 2026 " + name + "\n"
					}
				}
				break
			}
			pid, err := strconv.Atoi(args[1])
			if err != nil {
				t.Fatal(err)
			}
			if name := f.processes[pid]; name != "" {
				result.Output = "Sun Oct 25 12:00:00 2026 " + name
			} else {
				return utils.CommandResult{ExitCode: 1}, errors.New("missing process")
			}
		case "/sbin/sysctl":
			if args[0] == "-n" {
				for _, key := range args[1:] {
					result.Output += f.settings[key] + "\n"
				}
			} else {
				for _, argument := range args {
					key, value, ok := strings.Cut(argument, "=")
					if !ok {
						t.Fatal("unexpected sysctl argument")
					}
					f.settings[key] = value
				}
			}
		case "/usr/bin/rpcinfo":
			if args[0] == "-l" {
				result.Output = "   program vers tp_family/name/class address service\n    100005 3 inet6/tcp/cots_ord " + args[1] + ".3.118 mountd\n"
			}
			if args[0] == "-a" {
				if len(args) != 6 || args[2] != "-T" || !slices.Contains([]string{"tcp", "tcp6", "udp", "udp6"}, args[3]) || !slices.Contains([]string{"100003", "100005"}, args[4]) {
					t.Fatalf("uncontrolled direct NFS protocol probe: %v", args)
				}
				version, err := strconv.ParseUint(args[5], 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				if args[4] == "100003" {
					f.probes = append(f.probes, uint32(version))
				}
			}
		case "/sbin/kldstat":
		case "/usr/sbin/rpcbind", "/usr/sbin/rpc.statd", "/usr/sbin/rpc.lockd", "/usr/sbin/nfsuserd", "/usr/sbin/gssd":
			if len(args) != 0 {
				t.Fatal("shared daemon options were changed")
			}
			f.processes[100+len(f.processes)] = filepath.Base(command)
		case "/usr/sbin/mountd", "/usr/sbin/nfsd":
			name, pid, pidfile := "mountd", 201, d.mountdPIDPath
			if command == "/usr/sbin/nfsd" {
				name, pid, pidfile = "nfsd", 202, d.nfsdPIDPath
				for i, argument := range args {
					if argument == "--minthreads" {
						f.settings["vfs.nfsd.minthreads"] = args[i+1]
					}
					if argument == "--maxthreads" {
						f.settings["vfs.nfsd.maxthreads"] = args[i+1]
					}
				}
			}
			f.processes[pid] = name
			if err := os.WriteFile(pidfile, []byte(strconv.Itoa(pid)+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if name == "mountd" {
				f.exported = true
			}
		default:
			t.Fatalf("unexpected native command: %s %v", command, args)
		}
		return result, nil
	}
	return f
}

func TestNativePublishBacksUpOnceAndNeverRestoresOnDisable(t *testing.T) {
	f := newNativeFixture(t)
	d := f.driver
	original := []byte("# original\n/mnt/old -ro 192.0.2.1\n")
	if err := os.WriteFile(d.exportsPath, original, 0640); err != nil {
		t.Fatal(err)
	}
	if err := d.Publish(t.Context(), exportsMarker+"\n"); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(d.backupPath)
	if err != nil || string(backup) != string(original) {
		t.Fatalf("original backup = %q, %v", backup, err)
	}
	if err := d.Publish(t.Context(), exportsMarker+"\n/mnt/data -ro 192.0.2.2\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.exportsPath, []byte("manually edited exports"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(t.Context(), &nfsModels.NFSRuntime{Managed: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.Publish(t.Context(), exportsMarker+"\n"); err != nil {
		t.Fatal(err)
	}
	backup, err = os.ReadFile(d.backupPath)
	if err != nil || string(backup) != string(original) {
		t.Fatal("publication or disable replaced the original backup")
	}
	active, err := os.ReadFile(d.exportsPath)
	if err != nil || string(active) != exportsMarker+"\n" {
		t.Fatal("disable restored original access")
	}
	info, err := os.Stat(d.exportsPath)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("safe original permissions were not preserved: %v, %v", info, err)
	}
}

func TestNativePublicationRejectsUnsafeFiles(t *testing.T) {
	for _, test := range []string{"exports symlink", "backup symlink", "writable exports", "directory backup"} {
		t.Run(test, func(t *testing.T) {
			f := newNativeFixture(t)
			d := f.driver
			if strings.Contains(test, "backup") {
				if err := os.WriteFile(d.exportsPath, []byte("original exports"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			switch test {
			case "exports symlink", "backup symlink":
				path := d.exportsPath
				if test == "backup symlink" {
					path = d.backupPath
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "writable exports":
				if err := os.WriteFile(d.exportsPath, nil, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(d.exportsPath, 0666); err != nil {
					t.Fatal(err)
				}
			case "directory backup":
				if err := os.Mkdir(d.backupPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := d.Publish(t.Context(), exportsMarker+"\n"); err == nil {
				t.Fatal("unsafe configuration file was overwritten")
			}
		})
	}
}

func TestNativeDaemonArgumentsProtocolBoundsAndSharedConsumers(t *testing.T) {
	for _, test := range []struct{ protocol, min, max string }{
		{"v3", "3", "3"},
		{"v4", "4", "4"},
		{"v3v4", "3", "4"},
	} {
		protocol := test.protocol
		t.Run(protocol, func(t *testing.T) {
			f := newNativeFixture(t)
			d := f.driver
			// A host NFS client may already depend on these. No stop/reconfigure.
			f.processes[71], f.processes[72], f.processes[73], f.processes[74], f.processes[75] = "rpcbind", "rpc.statd", "rpc.lockd", "nfsuserd", "gssd"
			exports := exportsMarker + "\n/mnt/data -sec=sys -ro 192.0.2.1\n"
			if protocol != "v3" {
				exports += "V4: / -sec=sys\n"
			}
			if err := d.Publish(t.Context(), exports); err != nil {
				t.Fatal(err)
			}
			state := &nfsModels.NFSRuntime{Managed: true, Hash: exportsHash(exports), Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
			config := nfsModels.NFSConfig{Protocol: protocol}
			checkpoints := 0
			if err := d.Start(t.Context(), config, state, func() error { checkpoints++; return nil }); err != nil {
				t.Fatal(err)
			}
			if checkpoints != 2 {
				t.Fatalf("process ownership checkpoints = %d", checkpoints)
			}
			if f.settings["vfs.nfsd.server_min_nfsvers"] != test.min || f.settings["vfs.nfsd.server_max_nfsvers"] != test.max {
				t.Fatalf("protocol bounds: %v", f.settings)
			}
			if slices.Contains(f.probes, 3) != (protocol != "v4") || slices.Contains(f.probes, 4) != (protocol != "v3") {
				t.Fatalf("requested protocol responses were not checked: %v", f.probes)
			}
			mountCommand := "/usr/sbin/mountd -S "
			if protocol == "v4" {
				mountCommand += "-R "
			}
			if !slices.Contains(f.commands, mountCommand+d.exportsPath) || !slices.Contains(f.commands, "/usr/sbin/nfsd -t -P "+d.nfsdPIDPath) {
				t.Fatalf("uncontrolled native daemon argv: %v", f.commands)
			}
			for _, command := range f.commands {
				if strings.Contains(command, "/usr/sbin/service ") || strings.HasPrefix(command, "/usr/sbin/nfsd ") && strings.Contains(command, "-S") {
					t.Fatalf("unsafe rc action or unsupported nfsd flag: %s", command)
				}
			}
			if err := d.Stop(t.Context(), state); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.killed, []string{"nfsd", "mountd"}) || len(f.processes) != 5 || f.exported {
				t.Fatalf("stop affected shared consumers: killed=%v processes=%v", f.killed, f.processes)
			}
		})
	}
}

func TestNativeNumericIdentitySettingsAndDrift(t *testing.T) {
	for _, numericIDs := range []bool{false, true} {
		t.Run(fmt.Sprint(numericIDs), func(t *testing.T) {
			f := newNativeFixture(t)
			exports := exportsMarker + "\nV4: / -sec=sys\n/mnt/data -sec=sys\n"
			if err := f.driver.Publish(t.Context(), exports); err != nil {
				t.Fatal(err)
			}
			state := &nfsModels.NFSRuntime{Managed: true, Hash: exportsHash(exports), Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
			config := nfsModels.NFSConfig{Protocol: "v4", NumericIDs: numericIDs}
			if err := f.driver.Start(t.Context(), config, state, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			want := "0"
			if numericIDs {
				want = "1"
			}
			for _, key := range []string{"vfs.nfs.enable_uidtostring", "vfs.nfsd.enable_stringtouid"} {
				if f.settings[key] != want {
					t.Fatalf("%s = %s, want %s", key, f.settings[key], want)
				}
			}
			if slices.Contains(f.commands, "/usr/sbin/nfsuserd ") == numericIDs {
				t.Fatal("nfsuserd must only be started for named identity mapping")
			}
			f.settings["vfs.nfs.enable_uidtostring"] = "changed"
			if err := f.driver.Check(t.Context(), config, state); err == nil {
				t.Fatal("numeric identity drift was ignored")
			}
		})
	}
}

func TestNativeListenerTransportAndThreadOptions(t *testing.T) {
	for _, address := range []string{"192.0.2.10", "::1"} {
		t.Run(address, func(t *testing.T) {
			f := newNativeFixture(t)
			exports := exportsMarker + "\nV4: / -sec=sys\n/mnt/data -sec=sys\n"
			if err := f.driver.Publish(t.Context(), exports); err != nil {
				t.Fatal(err)
			}
			state := &nfsModels.NFSRuntime{Managed: true, Hash: exportsHash(exports), Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
			config := nfsModels.NFSConfig{Protocol: "v3v4", NumericIDs: true, UDP: true, AllowNonroot: true, BindAddresses: []string{address}, MountdPort: 42048, MinThreads: 1, MaxThreads: 4}
			if err := f.driver.Start(t.Context(), config, state, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			mountd := "/usr/sbin/mountd -S -n -p 42048 -s -h " + address + " " + f.driver.exportsPath
			nfsd := "/usr/sbin/nfsd -t -P " + f.driver.nfsdPIDPath + " -u --minthreads 1 --maxthreads 4 -h " + address
			if !slices.Contains(f.commands, mountd) || !slices.Contains(f.commands, nfsd) || f.settings["vfs.nfsd.nfs_privport"] != "0" {
				t.Fatalf("wrong listener arguments: %v", f.commands)
			}
			if !slices.Equal(state.BindAddresses, config.BindAddresses) {
				t.Fatalf("listener identity was not checkpointed: %v", state.BindAddresses)
			}
			f.settings["vfs.nfsd.maxthreads"] = "5"
			if err := f.driver.Check(t.Context(), config, state); err == nil {
				t.Fatal("thread drift ignored")
			}
			f.settings["vfs.nfsd.maxthreads"] = "4"
			f.driver.addresses = func() ([]string, error) { return nil, nil }
			if err := f.driver.Check(t.Context(), config, state); err == nil {
				t.Fatal("removed listener address ignored")
			}
		})
	}
}

func TestNativeUDPUsesConcreteAddressesAndRejectsUnavailableBindings(t *testing.T) {
	f := newNativeFixture(t)
	bindings, err := f.driver.bindings(nfsModels.NFSConfig{Protocol: "v3", UDP: true})
	if err != nil || !slices.Contains(bindings, "127.0.0.1") || !slices.Contains(bindings, "192.0.2.10") {
		t.Fatalf("UDP wildcard was not expanded: %v, %v", bindings, err)
	}
	if _, err := f.driver.bindings(nfsModels.NFSConfig{Protocol: "v3", BindAddresses: []string{"192.0.2.11"}}); err == nil {
		t.Fatal("unassigned bind address accepted")
	}
}

func TestNativeNFSv4MinorVersionBoundsAndAllExplicitListenersAreChecked(t *testing.T) {
	for _, maximum := range []string{"4.0", "4.1", "4.2"} {
		config := nfsModels.NFSConfig{Protocol: "v4", V4MaxVersion: maximum, BindAddresses: []string{"192.0.2.10", "::1"}}
		kernel := kernelSettings(config)
		if kernel["vfs.nfsd.server_min_minorversion4"] != "0" || kernel["vfs.nfsd.server_max_minorversion4"] != strings.TrimPrefix(maximum, "4.") {
			t.Fatalf("incorrect minor version bounds: %#v", kernel)
		}
		f := newNativeFixture(t)
		if err := f.driver.checkRPCServices(t.Context(), config); err != nil {
			t.Fatal(err)
		}
		for _, probe := range []string{
			"/usr/bin/rpcinfo -a 192.0.2.10.8.1 -T tcp 100003 4",
			"/usr/bin/rpcinfo -a ::1.8.1 -T tcp6 100003 4",
		} {
			if !slices.Contains(f.commands, probe) {
				t.Fatalf("listener was not probed: %s", probe)
			}
		}
	}
}

func TestNativeIPv6AutomaticMountdPortUsesDirectProbe(t *testing.T) {
	f := newNativeFixture(t)
	if err := f.driver.checkRPCServices(t.Context(), nfsModels.NFSConfig{Protocol: "v3v4", BindAddresses: []string{"::1"}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.commands, "/usr/bin/rpcinfo -a ::1.3.118 -T tcp6 100005 3") || slices.Contains(f.commands, "/usr/bin/rpcinfo -T tcp6 ::1 100005 3") {
		t.Fatalf("IPv6 mountd endpoint was not probed directly: %v", f.commands)
	}
}

func TestNativeReadinessClearsTransientProbeFailureAfterRecovery(t *testing.T) {
	f := newNativeFixture(t)
	exports := exportsMarker + "\n/mnt/data -sec=sys\n"
	if err := f.driver.Publish(t.Context(), exports); err != nil {
		t.Fatal(err)
	}
	run := f.driver.run
	failed := false
	f.driver.run = func(ctx context.Context, env []string, command string, args ...string) (utils.CommandResult, error) {
		if command == "/usr/bin/rpcinfo" && !failed {
			failed = true
			return utils.CommandResult{ExitCode: 1, Output: "starting"}, errors.New("connection refused")
		}
		return run(ctx, env, command, args...)
	}
	state := &nfsModels.NFSRuntime{Managed: true, Hash: exportsHash(exports), Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
	if err := f.driver.Start(t.Context(), nfsModels.NFSConfig{Protocol: "v3"}, state, func() error { return nil }); err != nil {
		t.Fatalf("recovered readiness retained an old failure: %v", err)
	}
}

func TestNativeIPv6MountdEndpointRejectsUnexpectedRegistrations(t *testing.T) {
	for _, line := range []string{
		"100005 3 inet6/tcp/cots_ord ::2.3.118 mountd",
		"100005 3 inet6/udp/clts ::1.3.118 mountd",
		"100005 1 inet6/tcp/cots_ord ::1.3.118 mountd",
		"100003 3 inet6/tcp/cots_ord ::1.3.118 nfs",
		"100005 3 inet6/tcp/cots_ord ::1.256.1 mountd",
		"100005 3 inet6/tcp/cots_ord ::1.0.0 mountd",
		"100005 3 inet6/tcp/cots_ord ::1.bad.1 mountd",
	} {
		f := newNativeFixture(t)
		run := f.driver.run
		f.driver.run = func(ctx context.Context, env []string, command string, args ...string) (utils.CommandResult, error) {
			if command == "/usr/bin/rpcinfo" && args[0] == "-l" {
				return utils.CommandResult{Output: line}, nil
			}
			return run(ctx, env, command, args...)
		}
		if _, err := f.driver.mountdIPv6Port(t.Context(), "::1"); err == nil {
			t.Fatalf("unexpected endpoint accepted: %s", line)
		}
	}
}

func TestNativeChecksRejectMissingAndUnexpectedExports(t *testing.T) {
	f := newNativeFixture(t)
	state := &nfsModels.NFSRuntime{Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
	if err := f.driver.checkExports(state); err == nil {
		t.Fatal("accepted an omitted export")
	}
	f.exported = true
	if err := f.driver.checkExports(state); err != nil {
		t.Fatal(err)
	}
	state.Filesystems[0].Name = "tank/other"
	if err := f.driver.checkExports(state); err == nil {
		t.Fatal("accepted an unexpected kernel filesystem")
	}
	f.driver.verifyMount = func(string, string) error { return errors.New("mount identity changed") }
	if err := f.driver.checkExports(state); err == nil {
		t.Fatal("ignored mount identity drift")
	}
}

func TestNativeChecksAllowUnexportedMountsButRejectExtraExports(t *testing.T) {
	for _, exported := range []bool{false, true} {
		t.Run(fmt.Sprint(exported), func(t *testing.T) {
			f := newNativeFixture(t)
			f.driver.mounts = func() ([]kernelMount, error) {
				return []kernelMount{
					{Name: "tank/ROOT/default", Path: "/"},
					{Name: "tank", Path: "/tank"},
					{Name: "tank/data", Path: "/mnt/data", Exported: true},
					{Name: "tank/data/child", Path: "/mnt/data/child"},
					{Name: "tmpfs", Path: "/mnt/data/private", Exported: exported},
				}, nil
			}
			state := &nfsModels.NFSRuntime{Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
			err := f.driver.checkExports(state)
			if exported && err == nil || !exported && err != nil {
				t.Fatalf("unconfigured mount exported=%t: %v", exported, err)
			}
		})
	}
}

func TestNativeStopDoesNotSignalReusedPID(t *testing.T) {
	f := newNativeFixture(t)
	f.processes[42] = "nfsd"
	process := nfsModels.ProcessIdentity{PID: 42, Birth: "old birth"}
	if err := f.driver.stopProcess(t.Context(), &process, "nfsd", syscall.SIGUSR1); err != nil || len(f.killed) != 0 || process.PID != 0 {
		t.Fatalf("reused PID was signalled: %v, %v", f.killed, err)
	}
}

func TestNativePublicationReplacesManualChanges(t *testing.T) {
	f := newNativeFixture(t)
	if err := f.driver.Publish(t.Context(), exportsMarker+"\n"); err != nil {
		t.Fatal(err)
	}
	state := &nfsModels.NFSRuntime{Hash: exportsHash(exportsMarker + "\n")}
	if err := f.driver.Check(t.Context(), nfsModels.NFSConfig{Protocol: "v3"}, state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.driver.exportsPath, []byte(exportsMarker+"\n/mnt/unexpected\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Check(t.Context(), nfsModels.NFSConfig{Protocol: "v3"}, state); err == nil {
		t.Fatal("ignored manual exports changes")
	}
	if err := f.driver.Publish(t.Context(), exportsMarker+"\n"); err != nil {
		t.Fatal(err)
	}
	f.driver.available = func() bool { return false }
	if err := f.driver.Publish(t.Context(), fmt.Sprintln(exportsMarker)); err == nil {
		t.Fatal("published without root privileges")
	}
	if err := f.driver.Stop(t.Context(), state); err == nil {
		t.Fatal("stopped NFS without root privileges")
	}
}

func TestNativeBackupFailurePreservesOriginalFile(t *testing.T) {
	f := newNativeFixture(t)
	original := "/mnt/data -ro 198.51.100.1\n"
	if err := os.WriteFile(f.driver.exportsPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	f.driver.backupPath = filepath.Join(t.TempDir(), "missing", "backup")
	if err := f.driver.Publish(t.Context(), exportsMarker+"\n"); err == nil {
		t.Fatal("original exports replaced without a backup")
	}
	active, err := os.ReadFile(f.driver.exportsPath)
	if err != nil || string(active) != original {
		t.Fatalf("backup failure changed the original exports: %q, %v", active, err)
	}
}

func TestEnablingNFSAutomaticallyReplacesExistingServerConfiguration(t *testing.T) {
	f := newNativeFixture(t)
	f.processes[42], f.processes[43], f.processes[71] = "nfsd", "mountd", "rpcbind"
	f.exported = true
	original := "/mnt/data -ro 198.51.100.1\n"
	if err := os.WriteFile(f.driver.exportsPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	service.driver = f.driver
	if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 0 {
		t.Fatal("disabled NFS touched the manual server")
	}
	if err := setEnabled(t, service, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.killed, []string{"nfsd", "mountd"}) || f.processes[71] != "rpcbind" {
		t.Fatalf("existing server was not replaced while preserving shared consumers: %v, %v", f.killed, f.processes)
	}
	backup, err := os.ReadFile(f.driver.backupPath)
	if err != nil || string(backup) != original {
		t.Fatalf("original exports were not backed up: %q, %v", backup, err)
	}
	active, err := os.ReadFile(f.driver.exportsPath)
	if err != nil || !strings.Contains(string(active), "192.0.2.1") || strings.Contains(string(active), "198.51.100.1") {
		t.Fatalf("manual grants survived automatic enable: %q, %v", active, err)
	}
}
