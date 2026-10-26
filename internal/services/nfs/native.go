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
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type kernelMount struct {
	Name, Path string
	Exported   bool
}

type nativeRuntime struct {
	exportsPath, backupPath    string
	nfsdPIDPath, mountdPIDPath string
	run                        func(context.Context, []string, string, ...string) (utils.CommandResult, error)
	mounts                     func() ([]kernelMount, error)
	clear                      func() error
	verifyMount                func(string, string) error
	available                  func() bool
	addresses                  func() ([]string, error)
	kill                       func(int, syscall.Signal) error
}

func newNativeRuntime() *nativeRuntime {
	return &nativeRuntime{
		exportsPath: "/etc/exports", backupPath: "/etc/exports.pre-sylve",
		nfsdPIDPath: "/var/run/sylve-nfsd.pid", mountdPIDPath: "/var/run/mountd.pid",
		run: utils.RunCommandWithEnvContext, mounts: nativeMounts, clear: clearNativeExports,
		verifyMount: verifyDatasetMount, available: func() bool { return os.Geteuid() == 0 },
		kill:      syscall.Kill,
		addresses: nativeAddresses,
	}
}

func (d *nativeRuntime) Available() bool { return d.available() }

func (d *nativeRuntime) command(ctx context.Context, command string, args ...string) (utils.CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := d.run(ctx, []string{"PATH=/sbin:/bin:/usr/sbin:/usr/bin", "LC_ALL=C", "TZ=UTC", "HOME=/root"}, command, args...)
	return result, errors.Join(err, ctx.Err())
}

func (d *nativeRuntime) execute(ctx context.Context, command string, args ...string) error {
	result, err := d.command(ctx, command, args...)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(command), err, strings.TrimSpace(result.Output))
	}
	return nil
}

func readRegularFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	owner, owned := info.Sys().(*syscall.Stat_t)
	if !owned || owner.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 1024*1024 {
		return nil, nil, fmt.Errorf("refusing unsafe NFS configuration file %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, nil, fmt.Errorf("NFS configuration file changed while opening %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err == nil && len(data) > 1024*1024 {
		err = fmt.Errorf("NFS configuration file is too large: %s", path)
	}
	return data, info, err
}

func managedExports(data []byte) bool { return strings.HasPrefix(string(data), exportsMarker+"\n") }

func backupExports(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".exports-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	// A hard link publishes the complete, synced backup atomically and refuses
	// an existing destination. A crash cannot leave a partial original backup.
	if err := os.Link(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (d *nativeRuntime) Publish(ctx context.Context, exports string) error {
	if !d.Available() {
		return errors.New("native NFS requires root privileges")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, info, err := readRegularFile(d.exportsPath)
	if err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if info != nil {
		mode = info.Mode().Perm() & 0644
	}
	if info != nil && !managedExports(data) {
		_, backup, err := readRegularFile(d.backupPath)
		if err != nil {
			return err
		}
		if backup == nil {
			if err := backupExports(d.backupPath, data, mode); err != nil {
				return fmt.Errorf("back up original NFS exports: %w", err)
			}
		}
	}
	if err := utils.AtomicWriteFile(d.exportsPath, []byte(exports), mode); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(d.exportsPath))
}

func (d *nativeRuntime) pids(ctx context.Context, name string) ([]int, error) {
	result, err := d.command(ctx, "/usr/bin/pgrep", "-x", regexp.QuoteMeta(name))
	if err != nil && result.ExitCode != 1 {
		return nil, fmt.Errorf("inspect %s processes: %w", name, err)
	}
	pids := make([]int, 0)
	for _, value := range strings.Fields(result.Output) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 1 {
			return nil, errors.New("invalid daemon process identity")
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func (d *nativeRuntime) identity(ctx context.Context, pid int, name string) (nfsModels.ProcessIdentity, error) {
	result, err := d.command(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "ucomm=")
	if err != nil && result.ExitCode == 1 && strings.TrimSpace(result.Output) == "" {
		return nfsModels.ProcessIdentity{}, nil
	}
	if err != nil {
		return nfsModels.ProcessIdentity{}, err
	}
	fields := strings.Fields(result.Output)
	if len(fields) != 6 || fields[5] != name {
		return nfsModels.ProcessIdentity{}, nil
	}
	return nfsModels.ProcessIdentity{PID: pid, Birth: strings.Join(fields[:5], " ")}, nil
}

func waitFor(ctx context.Context, check func() (bool, error)) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := check()
		if err != nil || ready {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *nativeRuntime) stopProcess(ctx context.Context, process *nfsModels.ProcessIdentity, name string, signal syscall.Signal) error {
	if process.PID == 0 {
		return nil
	}
	current, err := d.identity(ctx, process.PID, name)
	if err != nil {
		return err
	}
	if current == *process && current.Birth != "" {
		if err := d.kill(process.PID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		if err := waitFor(ctx, func() (bool, error) {
			identity, err := d.identity(ctx, process.PID, name)
			return identity != *process, err
		}); err != nil {
			return fmt.Errorf("wait for %s to stop: %w", name, err)
		}
	}
	*process = nfsModels.ProcessIdentity{}
	return nil
}

func (d *nativeRuntime) noServingProcesses(ctx context.Context) error {
	for _, name := range []string{"nfsd", "mountd"} {
		pids, err := d.pids(ctx, name)
		if err != nil {
			return err
		}
		if len(pids) > 0 {
			return fmt.Errorf("NFS %s processes are still running", name)
		}
	}
	return nil
}

func (d *nativeRuntime) Stop(ctx context.Context, state *nfsModels.NFSRuntime) error {
	if !d.Available() {
		return errors.New("native NFS requires root privileges")
	}
	// Stop serving first. Never signal shared rpcbind/nfsuserd/gssd/statd/lockd.
	var stopErr error
	for _, daemon := range []struct {
		name   string
		signal syscall.Signal
	}{{"nfsd", syscall.SIGUSR1}, {"mountd", syscall.SIGTERM}} {
		pids, err := d.pids(ctx, daemon.name)
		stopErr = errors.Join(stopErr, err)
		for _, pid := range pids {
			identity, err := d.identity(ctx, pid, daemon.name)
			if err == nil {
				err = d.stopProcess(ctx, &identity, daemon.name, daemon.signal)
			}
			stopErr = errors.Join(stopErr, err)
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var processErr error
	waitErr := waitFor(waitCtx, func() (bool, error) {
		processErr = d.noServingProcesses(waitCtx)
		return processErr == nil, nil
	})
	stopErr = errors.Join(stopErr, processErr, waitErr)
	clearErr := d.clear()
	if clearErr != nil {
		clearErr = fmt.Errorf("withdraw kernel NFS exports: %w", clearErr)
	}
	if stopErr == nil {
		state.NFSd, state.Mountd = nfsModels.ProcessIdentity{}, nfsModels.ProcessIdentity{}
	}
	return errors.Join(stopErr, clearErr)
}

func (d *nativeRuntime) ensureSharedDaemon(ctx context.Context, name, command string) error {
	pids, err := d.pids(ctx, name)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		if err := d.execute(ctx, command); err != nil {
			return err
		}
	}
	return waitFor(ctx, func() (bool, error) {
		pids, err := d.pids(ctx, name)
		return len(pids) > 0, err
	})
}

func (d *nativeRuntime) startProcess(ctx context.Context, name, command, pidfile string, process *nfsModels.ProcessIdentity, checkpoint func() error, args ...string) error {
	// Read back the pid even on command failure: partial daemon startup still
	// belongs to this apply and must be cleaned up, not left untracked.
	commandErr := d.execute(ctx, command, args...)
	identifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	err := waitFor(identifyCtx, func() (bool, error) {
		data, _, err := readRegularFile(pidfile)
		if err != nil {
			return false, err
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 1 {
			return false, nil
		}
		identity, err := d.identity(identifyCtx, pid, name)
		if err != nil || identity.PID == 0 {
			return false, err
		}
		*process = identity
		return true, nil
	})
	if err != nil {
		return errors.Join(commandErr, fmt.Errorf("unable to identify started %s: %w", name, err))
	}
	return errors.Join(commandErr, checkpoint())
}

func kernelSettings(config nfsModels.NFSConfig) map[string]string {
	minVersion, maxVersion := "3", "3"
	switch config.Protocol {
	case "v4":
		minVersion, maxVersion = "4", "4"
	case "v3v4":
		maxVersion = "4"
	}
	maxMinorVersion := strings.TrimPrefix(config.V4MaxVersion, "4.")
	if maxMinorVersion == "" {
		maxMinorVersion = "2"
	}
	numericIDs := "0"
	if config.NumericIDs {
		numericIDs = "1"
	}
	privport := "1"
	if config.AllowNonroot {
		privport = "0"
	}
	return map[string]string{
		"vfs.nfsd.server_min_nfsvers":       minVersion,
		"vfs.nfsd.server_max_nfsvers":       maxVersion,
		"vfs.nfsd.server_min_minorversion4": "0",
		"vfs.nfsd.server_max_minorversion4": maxMinorVersion,
		"vfs.nfsd.nfs_privport":             privport,
		"vfs.nfsd.issue_delegations":        "0",
		"vfs.nfsd.enable_stringtouid":       numericIDs,
		"vfs.nfs.enable_uidtostring":        numericIDs,
	}
}

func (d *nativeRuntime) Start(ctx context.Context, config nfsModels.NFSConfig, state *nfsModels.NFSRuntime, checkpoint func() error) error {
	if err := d.noServingProcesses(ctx); err != nil {
		return err
	}
	bindings, err := d.bindings(config)
	if err != nil {
		return err
	}
	state.BindAddresses = bindings
	if err := d.execute(ctx, "/sbin/kldstat", "-q", "-m", "nfsd"); err != nil {
		if err := d.execute(ctx, "/sbin/kldload", "nfsd"); err != nil {
			return err
		}
	}
	kernel := kernelSettings(config)
	settings := make([]string, 0, len(kernel))
	for key, value := range kernel {
		settings = append(settings, key+"="+value)
	}
	slices.Sort(settings)
	if err := d.execute(ctx, "/sbin/sysctl", settings...); err != nil {
		return err
	}
	if config.Protocol != "v4" {
		for _, daemon := range []struct{ name, command string }{
			{"rpcbind", "/usr/sbin/rpcbind"}, {"rpc.statd", "/usr/sbin/rpc.statd"}, {"rpc.lockd", "/usr/sbin/rpc.lockd"},
		} {
			if err := d.ensureSharedDaemon(ctx, daemon.name, daemon.command); err != nil {
				return err
			}
		}
	}
	if config.Protocol != "v3" && !config.NumericIDs {
		if err := d.ensureSharedDaemon(ctx, "nfsuserd", "/usr/sbin/nfsuserd"); err != nil {
			return err
		}
	}
	if state.KerberosHash != "" {
		if err := d.execute(ctx, "/sbin/kldstat", "-q", "-m", "kgssapi_krb5"); err != nil {
			if err := d.execute(ctx, "/sbin/kldload", "kgssapi_krb5"); err != nil {
				return err
			}
		}
		if err := d.ensureSharedDaemon(ctx, "gssd", "/usr/sbin/gssd"); err != nil {
			return err
		}
	}
	args := []string{"-S"}
	if config.AllowNonroot {
		args = append(args, "-n")
	}
	if config.Protocol == "v4" {
		args = append(args, "-R")
	}
	if config.MountdPort != 0 {
		args = append(args, "-p", strconv.Itoa(int(config.MountdPort)))
	}
	if len(bindings) > 0 {
		// mountd otherwise adds loopbacks again, causing duplicate binds when
		// a loopback address is already present in the explicit list.
		args = append(args, "-s")
	}
	for _, address := range bindings {
		args = append(args, "-h", address)
	}
	args = append(args, d.exportsPath)
	if err := d.startProcess(ctx, "mountd", "/usr/sbin/mountd", d.mountdPIDPath, &state.Mountd, checkpoint, args...); err != nil {
		return err
	}
	// mountd publishes its pid after reading exports. Check exact export presence
	// before opening NFS sockets, not just after nfsd begins accepting requests.
	if err := d.checkExports(state); err != nil {
		return err
	}
	args = []string{"-t", "-P", d.nfsdPIDPath}
	if state.KerberosHash != "" {
		host, _, _ := strings.Cut(strings.TrimPrefix(config.KerberosPrincipal, "nfs/"), "@")
		args = append(args, "-V", host)
	}
	if config.UDP {
		args = append(args, "-u")
	}
	if config.MinThreads != 0 {
		args = append(args, "--minthreads", strconv.Itoa(int(config.MinThreads)), "--maxthreads", strconv.Itoa(int(config.MaxThreads)))
	}
	for _, address := range bindings {
		args = append(args, "-h", address)
	}
	if err := d.startProcess(ctx, "nfsd", "/usr/sbin/nfsd", d.nfsdPIDPath, &state.NFSd, checkpoint, args...); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var lastErr error
	err = waitFor(readyCtx, func() (bool, error) {
		current := d.checkRPCServices(readyCtx, config)
		if current == nil {
			lastErr = nil
			return true, nil
		}
		if readyCtx.Err() == nil {
			lastErr = current
		}
		return false, nil
	})
	return errors.Join(err, lastErr)
}

func (d *nativeRuntime) Running(ctx context.Context, state *nfsModels.NFSRuntime) (bool, error) {
	if state.NFSd.PID != 0 {
		identity, err := d.identity(ctx, state.NFSd.PID, "nfsd")
		if err != nil {
			return false, err
		}
		if identity == state.NFSd && identity.Birth != "" {
			return true, nil
		}
	}
	pids, err := d.pids(ctx, "nfsd")
	if err == nil && len(pids) > 0 {
		err = errors.New("NFS serving processes do not match the applied configuration")
	}
	return false, err
}

func (d *nativeRuntime) checkExports(state *nfsModels.NFSRuntime) error {
	mounts, err := d.mounts()
	if err != nil {
		return err
	}
	want := make(map[string]string, len(state.Filesystems))
	for _, filesystem := range state.Filesystems {
		if err := d.verifyMount(filesystem.Mountpoint, filesystem.Name); err != nil {
			return fmt.Errorf("exported filesystem identity changed: %s: %w", filesystem.Name, err)
		}
		want[filesystem.Mountpoint] = filesystem.Name
	}
	for _, mount := range mounts {
		if !mount.Exported {
			continue
		}
		if want[mount.Path] != mount.Name {
			return fmt.Errorf("unexpected kernel NFS export: %s", mount.Path)
		}
		delete(want, mount.Path)
	}
	if len(want) > 0 {
		return errors.New("the kernel did not accept all requested NFS filesystem exports")
	}
	return nil
}

func (d *nativeRuntime) Check(ctx context.Context, config nfsModels.NFSConfig, state *nfsModels.NFSRuntime) error {
	data, _, err := readRegularFile(d.exportsPath)
	if err != nil {
		return err
	}
	if !managedExports(data) || exportsHash(string(data)) != state.Hash {
		return errors.New("installed exports do not match the applied configuration")
	}
	if err := d.checkExports(state); err != nil {
		return err
	}
	if len(state.Filesystems) == 0 {
		return d.noServingProcesses(ctx)
	}
	bindings, err := d.bindings(config)
	if err != nil {
		return err
	}
	if !slices.Equal(bindings, state.BindAddresses) {
		return errors.New("NFS listener addresses no longer match the applied configuration")
	}
	if err := d.checkProcesses(ctx, state); err != nil {
		return err
	}
	settings := kernelSettings(config)
	if config.MinThreads != 0 {
		settings["vfs.nfsd.minthreads"] = strconv.Itoa(int(config.MinThreads))
		settings["vfs.nfsd.maxthreads"] = strconv.Itoa(int(config.MaxThreads))
	}
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result, err := d.command(ctx, "/sbin/sysctl", append([]string{"-n"}, keys...)...)
	values := strings.Fields(result.Output)
	if err != nil || len(values) != len(keys) {
		return errors.Join(err, errors.New("unable to read all NFS kernel settings"))
	}
	for index, key := range keys {
		if values[index] != settings[key] {
			return fmt.Errorf("NFS kernel setting %s does not match applied policy", key)
		}
	}
	if state.KerberosHash != "" {
		pids, err := d.pids(ctx, "gssd")
		if err != nil || len(pids) == 0 {
			return errors.Join(err, errors.New("Kerberos GSS service is unavailable"))
		}
	}
	return d.checkRPCServices(ctx, config)
}

func (d *nativeRuntime) checkProcesses(ctx context.Context, state *nfsModels.NFSRuntime) error {
	if state.NFSd.PID <= 1 || state.Mountd.PID <= 1 || state.NFSd.PID == state.Mountd.PID {
		return errors.New("NFS serving processes are unavailable")
	}
	result, err := d.command(ctx, "/bin/ps", "-p", fmt.Sprintf("%d,%d", state.NFSd.PID, state.Mountd.PID), "-o", "pid=", "-o", "lstart=", "-o", "ucomm=")
	if err != nil {
		return err
	}
	want := map[int]struct {
		name  string
		birth string
	}{state.NFSd.PID: {"nfsd", state.NFSd.Birth}, state.Mountd.PID: {"mountd", state.Mountd.Birth}}
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 7 {
			return errors.New("invalid NFS process identity")
		}
		pid, err := strconv.Atoi(fields[0])
		process, ok := want[pid]
		if err != nil || !ok || process.birth == "" || process.name != fields[6] || process.birth != strings.Join(fields[1:6], " ") {
			return errors.New("NFS process identities no longer match the applied configuration")
		}
		delete(want, pid)
	}
	if len(want) != 0 {
		return errors.New("NFS serving process is missing")
	}
	return nil
}
