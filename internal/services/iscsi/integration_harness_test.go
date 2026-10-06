// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const iscsiTestIQNPrefix = "iqn.2026-10.io.sylve:test-"

type integrationDevice struct {
	Number int    `json:"lun"`
	Device string `json:"device"`
}

type integrationSession struct {
	ID        int `json:"sessionId"`
	Initiator struct {
		Name string `json:"name"`
	} `json:"initiator"`
	Target struct {
		Name   string `json:"name"`
		Portal string `json:"portal"`
	} `json:"target"`
	State         string `json:"state"`
	FailureReason string `json:"failureReason"`
	Devices       struct {
		LUNs []integrationDevice `json:"lun"`
	} `json:"devices"`
}

type integrationProcess struct {
	PID   int    `json:"pid"`
	Birth string `json:"birth"`
	Role  string `json:"role"`
}

type integrationDisk struct {
	Device string `json:"device"`
	Serial string `json:"serial"`
}

type integrationFileBacking struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type integrationNVMeController struct {
	NQN          string `json:"nqn"`
	HostNQN      string `json:"hostNqn"`
	Endpoint     string `json:"endpoint"`
	Device       string `json:"device,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Serial       string `json:"serial,omitempty"`
	ControllerID *int   `json:"controllerId,omitempty"`
}

type integrationManifest struct {
	RunID           string                      `json:"runID"`
	Pool            string                      `json:"pool"`
	PoolOwner       string                      `json:"poolOwner"`
	Targets         []string                    `json:"targets"`
	Endpoints       []string                    `json:"endpoints"`
	Processes       []integrationProcess        `json:"processes"`
	SessionIDs      []int                       `json:"sessionIDs"`
	Disks           []integrationDisk           `json:"disks"`
	Ports           []ctlPort                   `json:"ports"`
	LUNs            []ctlLUN                    `json:"luns"`
	FileBackings    []integrationFileBacking    `json:"fileBackings,omitempty"`
	NVMeControllers []integrationNVMeController `json:"nvmeControllers,omitempty"`
	NamedLUNs       []string                    `json:"namedLuns,omitempty"`
}

type iscsiIntegrationFixture struct {
	service        *Service
	directory      string
	manifest       integrationManifest
	clean          bool
	startedISCSID  bool
	iscsidIdentity integrationProcess
	nvmeDevices    func() ([]string, error)
}

func integrationCommand(svc *Service, command string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return svc.runTargetCommand(ctx, "", command, args...)
}

func integrationSessions(ctx context.Context, svc *Service) ([]integrationSession, error) {
	out, err := svc.runTargetCommand(ctx, "", "/usr/bin/iscsictl", "--libxo", "json", "-L", "-v")
	if err != nil {
		return nil, errors.New("cannot inspect kernel initiator sessions")
	}
	var result struct {
		ISCSI *struct {
			Sessions []integrationSession `json:"session"`
		} `json:"iscsictl"`
	}
	if json.Unmarshal([]byte(out), &result) != nil || result.ISCSI == nil {
		return nil, errors.New("invalid initiator session inventory")
	}
	for _, session := range result.ISCSI.Sessions {
		if session.ID <= 0 || session.Target.Portal == "" || session.Initiator.Name == "" {
			return nil, errors.New("incomplete initiator session identity")
		}
	}
	return result.ISCSI.Sessions, nil
}

func requireISCSIIntegrationFixture(t *testing.T) *iscsiIntegrationFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("requires real FreeBSD iSCSI; skipped in short mode")
	}
	if os.Geteuid() != 0 {
		t.Fatal("iSCSI integration tests require root")
	}
	for _, tool := range []string{"ctld", "iscsid", "iscsictl", "ctladm", "zfs", "zpool", "diskinfo", "sockstat", "camcontrol", "dd", "pgrep", "ps"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("iSCSI integration prerequisite missing: %s", tool)
		}
	}
	for _, device := range []string{"/dev/cam/ctl", "/dev/iscsi"} {
		if info, err := os.Stat(device); err != nil || info.Mode()&os.ModeDevice == 0 {
			t.Fatalf("iSCSI integration prerequisite missing: %s (load CTL and iSCSI kernel support)", device)
		}
	}
	lock, err := os.OpenFile("/var/run/sylve-iscsi-test.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal("another iSCSI integration fixture owns the host lock")
	}
	svc := &Service{}
	jailed, err := integrationCommand(svc, "/sbin/sysctl", "-n", "security.jail.jailed")
	if err != nil || strings.TrimSpace(jailed) != "0" {
		t.Fatal("run the iSCSI suite on a FreeBSD test host or VM, not in a jail")
	}
	leftovers, err := filepath.Glob("/tmp/sylve-iscsi-test-*")
	if err != nil || len(leftovers) != 0 {
		t.Fatal("interrupted iSCSI fixture exists; inspect scripts/check-iscsi-test-leaks.sh before retrying")
	}
	processes, processErr := integrationCommand(svc, "/bin/pgrep", "-x", "ctld")
	var exitErr *exec.ExitError
	if strings.TrimSpace(processes) != "" {
		t.Fatal("host is not reserved for iSCSI tests: an existing ctld process must be stopped by the operator; setup will not stop it")
	}
	if processErr == nil || !errors.As(processErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("cannot confirm an empty ctld process inventory: %v", processErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ports, err := svc.readCTLPorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	luns, err := svc.readCTLLUNs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range ports.Ports {
		if port.Group != "" || port.TransportGroup != "" {
			t.Fatal("host contains existing ctld-owned ports; setup leaves them untouched")
		}
	}
	for _, lun := range luns.LUNs {
		if lun.Name != "" {
			t.Fatal("host contains existing ctld-owned LUNs; setup leaves them untouched")
		}
	}
	sessions, err := integrationSessions(ctx, svc)
	if err != nil || len(sessions) != 0 {
		t.Fatal("host contains initiator sessions or their state cannot be checked")
	}
	if _, err := integrationCommand(svc, "/sbin/zpool", "list", "-H", "-o", "name"); err != nil {
		t.Fatal("ZFS prerequisite check failed")
	}
	directory, err := os.MkdirTemp("/tmp", "sylve-iscsi-test-")
	if err != nil {
		t.Fatal(err)
	}
	runID := strings.TrimPrefix(filepath.Base(directory), "sylve-iscsi-test-")
	f := &iscsiIntegrationFixture{service: svc, directory: directory, manifest: integrationManifest{RunID: runID}}
	t.Cleanup(func() { f.removeFiles(t) })
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	pool, _ := zfstest.DedicatedPool(t)
	f.manifest.Pool = pool
	owner, err := integrationCommand(svc, "/sbin/zfs", "get", "-H", "-o", "value", "org.alchemilla:sylve-test-owner", pool)
	if err != nil {
		zfstest.PreserveDedicatedPool(t, pool)
		t.Fatal("cannot record test pool ownership")
	}
	f.manifest.PoolOwner = strings.TrimSpace(owner)
	db, err := gorm.Open(sqlite.Open(filepath.Join(directory, "test.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal("cannot open fixture database")
	}
	svc.DB = db
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("cannot get fixture database handle")
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close fixture database: %v", err)
		}
	})
	if err := db.AutoMigrate(&models.BasicSettings{}, &iscsiModels.ISCSISettings{}, &iscsiModels.ISCSIInitiator{}, &iscsiModels.ISCSITarget{}, &iscsiModels.ISCSITargetPortal{}, &iscsiModels.ISCSITargetLUN{}); err != nil {
		t.Fatal("cannot create fixture database tables")
	}
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.ISCSI}}).Error; err != nil {
		t.Fatal("cannot create fixture service flag")
	}
	svc.runtime = &targetRuntime{configFile: filepath.Join(directory, "ctl.conf"), initiatorFile: filepath.Join(directory, "iscsi.conf"), pidFile: filepath.Join(directory, "ctld.pid")}
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/bin/kill" || command == "/usr/sbin/ctld" && !slices.Contains(args, "-t") || command == "/usr/sbin/ctladm" && len(args) > 0 && args[0] == "port" || command == "/sbin/nvmecontrol" && len(args) > 0 && (args[0] == "connect" || args[0] == "disconnect" || args[0] == "io-passthru") {
			if err := f.checkOwnedNamespace(ctx); err != nil {
				return "", err
			}
		}
		return utils.RunCommandWithInputContext(ctx, input, command, args...)
	}
	t.Cleanup(func() {
		if err := f.close(); err != nil {
			zfstest.PreserveDedicatedPool(t, pool)
			t.Errorf("iSCSI teardown failed; ownership evidence retained in %s: %v", directory, err)
		}
	})
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	status, statusErr := integrationCommand(svc, "/usr/sbin/service", "iscsid", "onestatus")
	if statusErr != nil {
		if !errors.As(statusErr, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatal("cannot inspect initiator daemon")
		}
		f.startedISCSID = true
		if _, err := integrationCommand(svc, "/usr/sbin/service", "iscsid", "onestart"); err != nil {
			t.Fatal("cannot start test initiator daemon")
		}
		status, statusErr = integrationCommand(svc, "/usr/sbin/service", "iscsid", "onestatus")
	}
	if statusErr != nil {
		t.Fatal("cannot identify initiator daemon")
	}
	f.iscsidIdentity, err = f.processIdentity(ctx, status, "iscsid")
	if err != nil {
		t.Fatal(err)
	}
	if f.startedISCSID {
		f.manifest.Processes = append(f.manifest.Processes, f.iscsidIdentity)
	}
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *iscsiIntegrationFixture) initiatorName() string {
	return iscsiTestIQNPrefix + f.manifest.RunID + ":initiator"
}

func (f *iscsiIntegrationFixture) saveManifest() error {
	data, err := json.Marshal(f.manifest)
	if err != nil {
		return err
	}
	return utils.AtomicWriteFile(filepath.Join(f.directory, "manifest.json"), data, 0600)
}

func (f *iscsiIntegrationFixture) processIdentity(ctx context.Context, status, role string) (integrationProcess, error) {
	match := targetPIDPattern.FindStringSubmatch(status)
	if len(match) != 2 {
		return integrationProcess{}, errors.New("invalid native daemon identity")
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil || pid <= 1 {
		return integrationProcess{}, errors.New("invalid native daemon PID")
	}
	birth, err := f.service.runTargetCommand(ctx, "", "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil || strings.TrimSpace(birth) == "" {
		return integrationProcess{}, errors.New("cannot identify native daemon start time")
	}
	return integrationProcess{PID: pid, Birth: strings.TrimSpace(birth), Role: role}, nil
}

func (f *iscsiIntegrationFixture) ownsSession(session integrationSession) bool {
	return session.Initiator.Name == f.initiatorName() && slices.Contains(f.manifest.Endpoints, session.Target.Portal) &&
		(session.Target.Name == "" || slices.Contains(f.manifest.Targets, session.Target.Name))
}

func (f *iscsiIntegrationFixture) ownsLUN(lun ctlLUN) bool {
	path := lun.File
	if path == "" {
		path = lun.Device
	}
	ownedBacking := strings.HasPrefix(path, "/dev/zvol/"+f.manifest.Pool+"/")
	for _, file := range f.manifest.FileBackings {
		if file.Path == path && f.ownsFileBacking(file) {
			ownedBacking = true
		}
	}
	if !ownedBacking {
		return false
	}
	if slices.Contains(f.manifest.NamedLUNs, lun.Name) && strings.HasPrefix(lun.Name, iscsiTestIQNPrefix+f.manifest.RunID+":") {
		return true
	}
	for _, target := range f.manifest.Targets {
		if strings.HasPrefix(lun.Name, target+",lun,") || strings.HasPrefix(lun.Name, target+",nsid,") {
			return true
		}
	}
	return false
}

func (f *iscsiIntegrationFixture) ownsFileBacking(file integrationFileBacking) bool {
	if filepath.Dir(file.Path) != f.directory {
		return false
	}
	info, err := os.Lstat(file.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid() && uint64(stat.Dev) == file.Device && uint64(stat.Ino) == file.Inode
}

func (f *iscsiIntegrationFixture) fileBacking(t *testing.T, label string) string {
	t.Helper()
	path := filepath.Join(f.directory, label+".img")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal("cannot create owned file backing")
	}
	defer file.Close()
	if err := file.Truncate(16 << 20); err != nil {
		t.Fatal("cannot size owned file backing")
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal("cannot inspect file backing")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("cannot identify file backing")
	}
	f.manifest.FileBackings = append(f.manifest.FileBackings, integrationFileBacking{Path: path, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)})
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f *iscsiIntegrationFixture) checkOwnedNamespace(ctx context.Context) error {
	if err := f.checkTargetProcesses(ctx); err != nil {
		return err
	}
	ports, err := f.service.readCTLPorts(ctx)
	if err != nil {
		return err
	}
	luns, err := f.service.readCTLLUNs(ctx)
	if err != nil {
		return err
	}
	for _, port := range ports.Ports {
		if port.Group != "" && !slices.Contains(f.manifest.Targets, port.Target) || port.TransportGroup != "" && !f.ownsNVMeController(port.controllerName()) {
			return errors.New("non-test target port appeared; left untouched")
		}
	}
	for _, lun := range luns.LUNs {
		if lun.Name != "" && !f.ownsLUN(lun) {
			return errors.New("non-test LUN appeared; left untouched")
		}
	}
	sessions, err := integrationSessions(ctx, f.service)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if !f.ownsSession(session) {
			return errors.New("non-test initiator session appeared; left untouched")
		}
	}
	if len(f.manifest.NVMeControllers) != 0 {
		if err := f.checkNVMeNamespace(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (f *iscsiIntegrationFixture) checkTargetProcesses(ctx context.Context) error {
	out, err := f.service.runTargetCommand(ctx, "", "/bin/pgrep", "-x", "ctld")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(out) == "" {
			return nil
		}
		return errors.New("cannot inspect target daemon processes")
	}
	parent, err := f.service.targetPID(ctx)
	if err != nil || parent == 0 {
		return errors.New("non-test target daemon appeared; left untouched")
	}
	if err := f.service.checkFixtureProcess(ctx, parent); err != nil {
		return err
	}
	for _, value := range strings.Fields(out) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 1 {
			return errors.New("invalid target process inventory")
		}
		info, err := f.service.runTargetCommand(ctx, "", "/bin/ps", "-p", value, "-o", "ppid=", "-o", "lstart=")
		var exitErr *exec.ExitError
		if pid != parent && errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			continue
		}
		fields := strings.Fields(info)
		if err != nil || len(fields) < 2 {
			return errors.New("target process identity changed")
		}
		if pid != parent {
			if fields[0] != strconv.Itoa(parent) {
				return errors.New("non-test target daemon appeared; left untouched")
			}
			continue
		}
		birth := strings.Join(fields[1:], " ")
		for _, process := range f.manifest.Processes {
			if process.Role == "ctld" && process.PID == pid && strings.Join(strings.Fields(process.Birth), " ") != birth {
				return errors.New("recorded target daemon identity changed; left untouched")
			}
		}
	}
	return nil
}

func (f *iscsiIntegrationFixture) record(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := f.checkOwnedNamespace(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.targetService(ctx, "onestatus")
	if err == nil {
		process, err := f.processIdentity(ctx, status, "ctld")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(f.manifest.Processes, process) {
			f.manifest.Processes = append(f.manifest.Processes, process)
		}
	} else if !errors.Is(err, errTargetStopped) {
		t.Fatal(err)
	}
	ports, err := f.service.readCTLPorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	luns, err := f.service.readCTLLUNs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.manifest.Ports = nil
	f.manifest.LUNs = nil
	for _, port := range ports.Ports {
		if port.Group != "" || port.TransportGroup != "" {
			f.manifest.Ports = append(f.manifest.Ports, port)
		}
	}
	for _, lun := range luns.LUNs {
		if lun.Name != "" {
			f.manifest.LUNs = append(f.manifest.LUNs, lun)
		}
	}
	sessions, err := integrationSessions(ctx, f.service)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.recordSessions(sessions, luns); err != nil {
		t.Fatal(err)
	}
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
}

func (f *iscsiIntegrationFixture) recordSessions(sessions []integrationSession, luns *ctlLUNs) error {
	for _, session := range sessions {
		if !f.ownsSession(session) {
			return errors.New("cannot record a non-test initiator session")
		}
		if !slices.Contains(f.manifest.SessionIDs, session.ID) {
			f.manifest.SessionIDs = append(f.manifest.SessionIDs, session.ID)
		}
		for _, device := range session.Devices.LUNs {
			if iscsiTestProbePattern.MatchString(device.Device) {
				continue
			}
			matched := false
			for _, lun := range luns.LUNs {
				if lun.Name != session.Target.Name+",lun,"+strconv.Itoa(device.Number) {
					continue
				}
				if !f.ownsLUN(lun) || !iscsiTestDiskPattern.MatchString(device.Device) {
					return errors.New("cannot record an owned session disk identity")
				}
				disk := integrationDisk{Device: device.Device, Serial: strings.Trim(lun.Serial, " \t\r\n\x00")}
				if disk.Serial == "" {
					return errors.New("owned session disk has no serial")
				}
				if !slices.Contains(f.manifest.Disks, disk) {
					f.manifest.Disks = append(f.manifest.Disks, disk)
				}
				matched = true
			}
			if !matched {
				return errors.New("cannot join an owned session disk to a target LUN")
			}
		}
	}
	return nil
}

func (f *iscsiIntegrationFixture) endpoint(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := listener.Addr().String()
	listener.Close()
	f.manifest.Endpoints = append(f.manifest.Endpoints, endpoint)
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func (f *iscsiIntegrationFixture) volume(t *testing.T, label string) string {
	t.Helper()
	zvol := f.manifest.Pool + "/" + label
	if _, err := integrationCommand(f.service, "/sbin/zfs", "create", "-V", "16M", zvol); err != nil {
		t.Fatal("cannot create test zvol")
	}
	return zvol
}

func (f *iscsiIntegrationFixture) target(t *testing.T, label, endpoint, auth, user, secret, mutualUser, mutualSecret string) iscsiModels.ISCSITarget {
	t.Helper()
	name := iscsiTestIQNPrefix + f.manifest.RunID + ":" + label
	f.manifest.Targets = append(f.manifest.Targets, name)
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	zvol := f.volume(t, label)
	if err := f.service.CreateTarget(name, "", auth, user, secret, mutualUser, mutualSecret); err != nil {
		t.Fatal(err)
	}
	var target iscsiModels.ISCSITarget
	if err := f.service.DB.Where("target_name = ?", name).First(&target).Error; err != nil {
		t.Fatal("cannot load test target")
	}
	if endpoint != "" {
		if err := f.service.AddPortal(target.ID, "127.0.0.1", mustPort(t, endpoint)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.AddLUN(target.ID, 0, zvol); err != nil {
		t.Fatal(err)
	}
	f.record(t)
	return target
}

func (f *iscsiIntegrationFixture) initiator(t *testing.T, target iscsiModels.ISCSITarget, endpoint, nickname, auth, user, secret, mutualUser, mutualSecret string) iscsiModels.ISCSIInitiator {
	t.Helper()
	if err := f.service.CreateInitiator(nickname, endpoint, target.TargetName, f.initiatorName(), auth, user, secret, mutualUser, mutualSecret); err != nil {
		t.Fatal(err)
	}
	var initiator iscsiModels.ISCSIInitiator
	if err := f.service.DB.Where("nickname = ?", nickname).First(&initiator).Error; err != nil {
		t.Fatal("cannot load test initiator")
	}
	f.record(t)
	return initiator
}

func waitIntegration(t *testing.T, description string, check func(context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		if check(ctx) {
			return
		}
		if waitTarget(ctx, 100*time.Millisecond) != nil {
			break
		}
	}
	t.Fatalf("timed out waiting for %s", description)
}

func (f *iscsiIntegrationFixture) session(t *testing.T, name, endpoint string, connected bool) integrationSession {
	t.Helper()
	var result integrationSession
	waitIntegration(t, "test session state", func(ctx context.Context) bool {
		sessions, err := integrationSessions(ctx, f.service)
		if err != nil {
			return false
		}
		for _, session := range sessions {
			if session.Target.Name != name || session.Target.Portal != endpoint || session.Initiator.Name != f.initiatorName() {
				continue
			}
			if connected {
				device, err := integrationSessionDisk(session)
				if err != nil {
					t.Fatal(err)
				}
				if device != "" {
					result = session
					return true
				}
			}
			if !connected {
				if session.State == "Connected" || len(session.Devices.LUNs) > 0 {
					t.Fatal("incorrect credentials connected to a test disk")
				}
				if session.FailureReason == "Authentication failure" || session.FailureReason == "Mutual CHAP failed" {
					result = session
					return true
				}
			}
		}
		return false
	})
	f.record(t)
	return result
}

func (f *iscsiIntegrationFixture) discover(t *testing.T, endpoint string, targets ...iscsiModels.ISCSITarget) {
	t.Helper()
	text := "discovery { targetaddress = " + endpoint + "; initiatorname = " + f.initiatorName() + "; sessiontype = discovery; }\n"
	path := filepath.Join(f.directory, "discovery.conf")
	if err := utils.AtomicWriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationCommand(f.service, "/usr/bin/iscsictl", "-c", path, "-A", "-n", "discovery"); err != nil {
		t.Fatal("cannot start SendTargets discovery")
	}
	for _, target := range targets {
		f.session(t, target.TargetName, endpoint, true)
	}
	for _, target := range targets {
		f.disconnect(t, target, endpoint)
	}
}

func (f *iscsiIntegrationFixture) discoveryInventory(t *testing.T, endpoint string, expected map[string][]string) {
	t.Helper()
	if !slices.Contains(f.manifest.Endpoints, endpoint) {
		t.Fatal("discovery endpoint is not fixture-owned")
	}
	conn, err := net.DialTimeout("tcp4", endpoint, 10*time.Second)
	if err != nil {
		t.Fatal("cannot open owned discovery endpoint")
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	exchange := func(header [48]byte, data string, opcode byte) ([48]byte, string) {
		length := len(data)
		header[5], header[6], header[7] = byte(length>>16), byte(length>>8), byte(length)
		packet := append(header[:], []byte(data)...)
		packet = append(packet, make([]byte, (4-length%4)%4)...)
		if _, err := io.Copy(conn, strings.NewReader(string(packet))); err != nil {
			t.Fatal("cannot send discovery PDU")
		}
		var response [48]byte
		if _, err := io.ReadFull(conn, response[:]); err != nil {
			t.Fatal("cannot read discovery PDU")
		}
		length = int(response[5])<<16 | int(response[6])<<8 | int(response[7])
		if response[0] != opcode || response[4] != 0 || length > 65536 {
			t.Fatal("unsupported discovery response")
		}
		payload := make([]byte, length+(4-length%4)%4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatal("cannot read discovery payload")
		}
		return response, string(payload[:length])
	}
	var login [48]byte
	login[0], login[1] = 0x43, 0x87
	isid := sha256.Sum256([]byte(f.initiatorName()))
	copy(login[8:14], isid[:6])
	login[8] = 0x80
	binary.BigEndian.PutUint32(login[16:20], 1)
	response, _ := exchange(login, "InitiatorName="+f.initiatorName()+"\x00SessionType=Discovery\x00HeaderDigest=None\x00DataDigest=None\x00MaxRecvDataSegmentLength=65536\x00", 0x23)
	if response[1] != 0x87 || response[36] != 0 || response[37] != 0 {
		t.Fatal("native discovery login rejected")
	}
	var text [48]byte
	text[0], text[1] = 0x44, 0x80
	binary.BigEndian.PutUint32(text[16:20], 2)
	binary.BigEndian.PutUint32(text[20:24], ^uint32(0))
	copy(text[24:28], response[28:32])
	binary.BigEndian.PutUint32(text[28:32], binary.BigEndian.Uint32(response[24:28])+1)
	response, payload := exchange(text, "SendTargets=All\x00", 0x24)
	if response[1] != 0x80 || binary.BigEndian.Uint32(response[20:24]) != ^uint32(0) {
		t.Fatal("unsupported continued SendTargets response")
	}
	ctx, cancel := f.service.targetContext()
	defer cancel()
	ports, err := f.service.readCTLPorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string][]string)
	name := ""
	for _, entry := range strings.Split(payload, "\x00") {
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatal("invalid SendTargets entry")
		}
		switch key {
		case "TargetName":
			if _, ok := expected[value]; !ok {
				t.Fatal("unexpected target advertised by SendTargets")
			}
			if _, exists := actual[value]; exists {
				t.Fatal("duplicate SendTargets target")
			}
			name = value
			actual[name] = nil
		case "TargetAddress":
			address, tagText, ok := strings.Cut(value, ",")
			tag, err := strconv.Atoi(tagText)
			if !ok || err != nil || tag <= 0 || name == "" || !slices.Contains(expected[name], address) || slices.Contains(actual[name], address) {
				t.Fatal("unexpected or duplicate discovery address")
			}
			listener, err := f.service.parseListener(address, 3260)
			if err != nil {
				t.Fatal("invalid discovery listener")
			}
			matched := false
			for _, port := range ports.Ports {
				if port.Target == name && port.Group == f.service.groupName(listener) && port.Tag == tag {
					matched = true
				}
			}
			if !matched {
				t.Fatal("advertised portal tag does not match native target port")
			}
			actual[name] = append(actual[name], address)
		default:
			t.Fatal("unsupported SendTargets key")
		}
	}
	if len(actual) != len(expected) {
		t.Fatal("SendTargets did not advertise every expected target")
	}
	for name, addresses := range expected {
		if len(actual[name]) != len(addresses) {
			t.Fatal("SendTargets did not advertise every expected endpoint")
		}
	}
	var logout [48]byte
	logout[0], logout[1] = 0x46, 0x80
	binary.BigEndian.PutUint32(logout[16:20], 3)
	copy(logout[24:28], response[28:32])
	binary.BigEndian.PutUint32(logout[28:32], binary.BigEndian.Uint32(response[24:28])+1)
	response, _ = exchange(logout, "", 0x26)
	if response[2] != 0 {
		t.Fatal("native discovery logout failed")
	}
}

var iscsiTestDiskPattern = regexp.MustCompile(`^da[0-9]+$`)
var iscsiTestProbePattern = regexp.MustCompile(`^probe[0-9]+$`)

func integrationSessionDisk(session integrationSession) (string, error) {
	if session.State != "Connected" {
		return "", nil
	}
	device := ""
	for _, lun := range session.Devices.LUNs {
		if lun.Number == 0 && iscsiTestDiskPattern.MatchString(lun.Device) {
			if device != "" {
				return "", errors.New("ambiguous test disk identity")
			}
			device = lun.Device
		}
	}
	return device, nil
}

func (f *iscsiIntegrationFixture) readyDisk(ctx context.Context, name, endpoint string) (string, error) {
	sessions, err := integrationSessions(ctx, f.service)
	if err != nil {
		return "", err
	}
	var session *integrationSession
	for i := range sessions {
		candidate := &sessions[i]
		if candidate.Target.Name == name && candidate.Target.Portal == endpoint && candidate.Initiator.Name == f.initiatorName() {
			if session != nil {
				return "", errors.New("ambiguous test session identity")
			}
			session = candidate
		}
	}
	if session == nil {
		return "", nil
	}
	if !f.ownsSession(*session) {
		return "", errors.New("test session is not fixture-owned")
	}
	device, err := integrationSessionDisk(*session)
	if err != nil || device == "" {
		return "", err
	}
	luns, err := f.service.readCTLLUNs(ctx)
	if err != nil {
		return "", err
	}
	var backing ctlLUN
	for _, lun := range luns.LUNs {
		if lun.Name == name+",lun,0" {
			if backing.Name != "" {
				return "", errors.New("ambiguous test backing identity")
			}
			backing = lun
		}
	}
	serial := strings.Trim(backing.Serial, " \t\r\n\x00")
	if serial == "" || !f.ownsLUN(backing) {
		return "", errors.New("test backing has no owned CTL identity")
	}
	actual, err := f.service.runTargetCommand(ctx, "", "/sbin/camcontrol", "inquiry", device, "-S")
	if err != nil {
		return "", nil
	}
	if strings.TrimSpace(actual) != serial {
		return "", errors.New("CAM serial does not match the owned target LUN; refusing disk I/O")
	}
	geometry, err := f.service.runTargetCommand(ctx, "", "/usr/sbin/diskinfo", "/dev/"+device)
	if err != nil {
		return "", nil
	}
	fields := strings.Fields(geometry)
	if len(fields) < 3 || fields[1] != strconv.FormatUint(backing.Blocksize, 10) || fields[2] != strconv.FormatUint(backing.Blocks*backing.Blocksize, 10) {
		return "", errors.New("initiator capacity or blocksize does not match the owned backing")
	}
	if err := f.recordSessions([]integrationSession{*session}, luns); err != nil {
		return "", err
	}
	if err := f.saveManifest(); err != nil {
		return "", err
	}
	return device, nil
}

func (f *iscsiIntegrationFixture) disk(t *testing.T, target iscsiModels.ISCSITarget, endpoint string) string {
	t.Helper()
	var device string
	waitIntegration(t, "owned LUN 0 disk identity", func(ctx context.Context) bool {
		var err error
		device, err = f.readyDisk(ctx, target.TargetName, endpoint)
		if err != nil {
			t.Fatal(err)
		}
		return device != ""
	})
	f.record(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, command := range []string{"/sbin/mount", "/usr/sbin/swapinfo"} {
		args := []string{"-p"}
		if command == "/usr/sbin/swapinfo" {
			args = []string{"-k"}
		}
		out, err := f.service.runTargetCommand(ctx, "", command, args...)
		if err != nil {
			t.Fatal("cannot check test disk use")
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/"+device) {
				t.Fatal("test disk is mounted or used as swap; refusing disk I/O")
			}
		}
	}
	return "/dev/" + device
}

func (f *iscsiIntegrationFixture) io(t *testing.T, target iscsiModels.ISCSITarget, endpoint string, pattern byte) {
	t.Helper()
	device := f.disk(t, target, endpoint)
	data := strings.Repeat(string([]byte{pattern}), 4096)
	patternPath := filepath.Join(f.directory, "pattern")
	if err := os.WriteFile(patternPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationCommand(f.service, "/bin/dd", "if="+patternPath, "of="+device, "bs=4096", "count=1", "seek=8"); err != nil {
		t.Fatal("test disk write failed")
	}
	if _, err := integrationCommand(f.service, "/sbin/camcontrol", "cmd", strings.TrimPrefix(device, "/dev/"), "-c", "35 00 00 00 00 00 00 00 00 00"); err != nil {
		t.Fatal("test disk cache flush failed")
	}
	var lun iscsiModels.ISCSITargetLUN
	if err := f.service.DB.Where("target_id = ? AND lun_number = 0", target.ID).First(&lun).Error; err != nil {
		t.Fatal("cannot load test backing")
	}
	for _, path := range []string{device, "/dev/zvol/" + lun.ZVol} {
		out, err := integrationCommand(f.service, "/bin/dd", "if="+path, "bs=4096", "count=1", "skip=8")
		if err != nil || out != data {
			t.Fatal("read pattern does not match the identified target and its backing")
		}
	}
}

func (f *iscsiIntegrationFixture) disconnect(t *testing.T, target iscsiModels.ISCSITarget, endpoint string) {
	t.Helper()
	if _, err := f.service.runInitiatorCommand("-R", "-t", target.TargetName, "-p", endpoint); err != nil {
		t.Fatal("cannot remove owned test session")
	}
	waitIntegration(t, "test session removal", func(ctx context.Context) bool {
		sessions, err := integrationSessions(ctx, f.service)
		if err != nil {
			return false
		}
		for _, session := range sessions {
			if session.Target.Name == target.TargetName && session.Target.Portal == endpoint {
				return false
			}
		}
		return true
	})
}

func (f *iscsiIntegrationFixture) waitDetached(ctx context.Context) error {
	for ctx.Err() == nil {
		sessions, err := integrationSessions(ctx, f.service)
		if err != nil {
			return err
		}
		detached := len(sessions) == 0
		for _, disk := range f.manifest.Disks {
			if !iscsiTestDiskPattern.MatchString(disk.Device) {
				return errors.New("invalid recorded test disk")
			}
			if _, err := os.Stat("/dev/" + disk.Device); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return errors.New("cannot check test disk detach")
			}
			out, err := f.service.runTargetCommand(ctx, "", "/sbin/camcontrol", "inquiry", disk.Device, "-S")
			if err != nil || strings.TrimSpace(out) == disk.Serial {
				detached = false
			}
		}
		if detached {
			return nil
		}
		if waitTarget(ctx, 100*time.Millisecond) != nil {
			break
		}
	}
	return errors.New("test session or CAM disk detach timed out")
}

func (f *iscsiIntegrationFixture) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := f.checkOwnedNamespace(ctx); err != nil {
		return err
	}
	if err := f.disconnectNVMeControllers(ctx); err != nil {
		return err
	}
	sessions, err := integrationSessions(ctx, f.service)
	if err != nil {
		return err
	}
	luns, err := f.service.readCTLLUNs(ctx)
	if err != nil {
		return err
	}
	if err := f.recordSessions(sessions, luns); err != nil {
		return err
	}
	if err := f.saveManifest(); err != nil {
		return err
	}
	for _, session := range sessions {
		args := []string{"-c", f.service.initiatorPath(), "-R", "-p", session.Target.Portal}
		if session.Target.Name != "" {
			args = append(args, "-t", session.Target.Name)
		}
		if _, err := f.service.runTargetCommand(ctx, "", "/usr/bin/iscsictl", args...); err != nil {
			return errors.New("test session removal failed")
		}
	}
	if err := f.waitDetached(ctx); err != nil {
		return err
	}
	if err := f.checkOwnedNamespace(ctx); err != nil {
		return err
	}
	for _, model := range []any{&iscsiModels.ISCSITargetPortal{}, &iscsiModels.ISCSITargetLUN{}, &iscsiModels.ISCSITarget{}} {
		if err := f.service.DB.Where("1 = 1").Delete(model).Error; err != nil {
			return errors.New("cannot clear fixture target rows")
		}
	}
	var settings models.BasicSettings
	if err := f.service.DB.First(&settings).Error; err != nil {
		return err
	}
	if err := f.service.DB.Model(&settings).Select("Services").Updates(&models.BasicSettings{Services: []models.AvailableService{models.ISCSI}}).Error; err != nil {
		return err
	}
	if err := f.service.DB.Model(&iscsiModels.ISCSISettings{}).Where("id = ?", 1).Update("extra_target_config", "").Error; err != nil {
		return errors.New("cannot clear fixture extra config")
	}
	if err := f.service.WriteTargetConfig(true); err != nil {
		return err
	}
	if err := f.service.SetEnabled(false); err != nil {
		return err
	}
	ports, err := f.service.readCTLPorts(ctx)
	if err != nil {
		return err
	}
	luns, err = f.service.readCTLLUNs(ctx)
	if err != nil {
		return err
	}
	for _, port := range ports.Ports {
		if port.Group != "" || port.TransportGroup != "" {
			return errors.New("owned target port remains after cleanup")
		}
	}
	for _, lun := range luns.LUNs {
		if lun.Name != "" {
			return errors.New("owned target LUN remains after cleanup")
		}
	}
	if f.startedISCSID {
		status, err := f.service.runTargetCommand(ctx, "", "/usr/sbin/service", "iscsid", "onestatus")
		if err != nil {
			return errors.New("cannot inspect owned initiator daemon during cleanup")
		}
		identity, err := f.processIdentity(ctx, status, "iscsid")
		if err != nil || identity != f.iscsidIdentity {
			return errors.New("initiator daemon identity changed; left untouched")
		}
		if _, err := f.service.runTargetCommand(ctx, "", "/usr/sbin/service", "iscsid", "onestop"); err != nil {
			return errors.New("owned initiator daemon stop failed")
		}
		for {
			out, err := f.service.runTargetCommand(ctx, "", "/bin/ps", "-p", strconv.Itoa(identity.PID), "-o", "lstart=")
			var exitErr *exec.ExitError
			if err != nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				break
			}
			if err != nil {
				return errors.New("cannot confirm owned initiator daemon stop")
			}
			if strings.TrimSpace(out) != identity.Birth {
				return errors.New("initiator daemon PID reused during stop; left untouched")
			}
			if waitTarget(ctx, 100*time.Millisecond) != nil {
				return errors.New("owned initiator daemon stop timed out")
			}
		}
	}
	f.clean = true
	return nil
}

func (f *iscsiIntegrationFixture) removeFiles(t *testing.T) {
	t.Helper()
	if !f.clean {
		return
	}
	out, err := integrationCommand(f.service, "/sbin/zpool", "list", "-H", "-o", "name")
	if err != nil {
		t.Errorf("cannot confirm test pool removal; kept iSCSI manifest in %s", f.directory)
		return
	}
	if slices.Contains(strings.Fields(out), f.manifest.Pool) {
		t.Errorf("test pool remains; kept iSCSI manifest in %s", f.directory)
		return
	}
	for _, file := range f.manifest.FileBackings {
		if !f.ownsFileBacking(file) {
			t.Error("file backing identity changed; retained fixture evidence")
			return
		}
		if err := os.Remove(file.Path); err != nil {
			t.Error("cannot remove owned file backing")
			return
		}
	}
	for _, name := range []string{"ctl.conf", "ctl.conf.recovery.json", "ctl.conf.recovery.lock", "iscsi.conf", "discovery.conf", "ctld.pid", "test.db", "test.db-wal", "test.db-shm", "pattern"} {
		if err := os.Remove(filepath.Join(f.directory, name)); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove owned fixture file: %v", err)
			return
		}
	}
	if err := os.Remove(filepath.Join(f.directory, "manifest.json")); err != nil {
		t.Errorf("remove test manifest: %v", err)
		return
	}
	if err := os.Remove(f.directory); err != nil {
		t.Errorf("remove fixture directory: %v", err)
	}
}

func TestISCSIFixtureOwnershipNeedsRecordedIdentities(t *testing.T) {
	f := &iscsiIntegrationFixture{manifest: integrationManifest{
		RunID: "ownership-unit", Pool: "sylve-test-owned",
		Targets: []string{iscsiTestIQNPrefix + "ownership-unit:target"}, Endpoints: []string{"127.0.0.1:49222"},
	}}
	var session integrationSession
	session.ID = 12
	session.Initiator.Name = f.initiatorName()
	session.Target.Name = f.manifest.Targets[0]
	session.Target.Portal = f.manifest.Endpoints[0]
	backing := ctlLUN{Name: session.Target.Name + ",lun,0", File: "/dev/zvol/" + f.manifest.Pool + "/volume", Serial: "owned-serial"}
	session.Devices.LUNs = []integrationDevice{{Number: 0, Device: "probe0"}}
	if err := f.recordSessions([]integrationSession{session}, &ctlLUNs{LUNs: []ctlLUN{backing}}); err != nil {
		t.Fatal(err)
	}
	if len(f.manifest.Disks) != 0 {
		t.Fatal("CAM probe was recorded as a disk")
	}
	session.Devices.LUNs = []integrationDevice{{Number: 0, Device: "probe0"}, {Number: 0, Device: "da8"}}
	if err := f.recordSessions([]integrationSession{session}, &ctlLUNs{LUNs: []ctlLUN{backing}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.manifest.SessionIDs, 12) || !slices.Contains(f.manifest.Disks, integrationDisk{Device: "da8", Serial: "owned-serial"}) {
		t.Fatal("owned identity was not recorded")
	}
	for _, change := range []func(*integrationSession, *ctlLUN){
		func(s *integrationSession, _ *ctlLUN) { s.Initiator.Name = iscsiTestIQNPrefix + "other:initiator" },
		func(s *integrationSession, _ *ctlLUN) { s.Target.Portal = "127.0.0.1:49223" },
		func(s *integrationSession, _ *ctlLUN) { s.Target.Name = iscsiTestIQNPrefix + "other:target" },
		func(s *integrationSession, _ *ctlLUN) {
			s.Devices.LUNs = []integrationDevice{{Number: 0, Device: "ada8"}}
		},
		func(_ *integrationSession, l *ctlLUN) { l.File = "/dev/zvol/non-test/volume" },
		func(_ *integrationSession, l *ctlLUN) { l.Serial = "" },
	} {
		candidate, lun := session, backing
		change(&candidate, &lun)
		if err := f.recordSessions([]integrationSession{candidate}, &ctlLUNs{LUNs: []ctlLUN{lun}}); err == nil {
			t.Fatal("unproven disk or session identity was accepted")
		}
	}
}

func TestISCSIFixtureSessionDiskReadiness(t *testing.T) {
	for _, test := range []struct {
		name   string
		state  string
		luns   []integrationDevice
		want   string
		unsafe bool
	}{
		{name: "no-devices", state: "Connected"},
		{name: "probing", state: "Connected", luns: []integrationDevice{{Number: 0, Device: "probe0"}}},
		{name: "wrong-lun", state: "Connected", luns: []integrationDevice{{Number: 1, Device: "da8"}}},
		{name: "disconnected", state: "Disconnected", luns: []integrationDevice{{Number: 0, Device: "da8"}}},
		{name: "invalid-device", state: "Connected", luns: []integrationDevice{{Number: 0, Device: "/dev/da8"}}},
		{name: "ready", state: "Connected", luns: []integrationDevice{{Number: 0, Device: "da8"}}, want: "da8"},
		{name: "probe-and-disk", state: "Connected", luns: []integrationDevice{{Number: 0, Device: "probe0"}, {Number: 0, Device: "da8"}}, want: "da8"},
		{name: "ambiguous", state: "Connected", luns: []integrationDevice{{Number: 0, Device: "da8"}, {Number: 0, Device: "da9"}}, unsafe: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := integrationSession{State: test.state}
			session.Devices.LUNs = test.luns
			device, err := integrationSessionDisk(session)
			if device != test.want || (err != nil) != test.unsafe {
				t.Fatalf("device=%q error=%v want device=%q unsafe=%v", device, err, test.want, test.unsafe)
			}
		})
	}
}

func TestISCSIFixtureDiskWaitsForProbeAndDeviceReadiness(t *testing.T) {
	f := &iscsiIntegrationFixture{directory: t.TempDir(), manifest: integrationManifest{
		RunID: "disk-readiness", Pool: "sylve-test-owned",
		Targets: []string{iscsiTestIQNPrefix + "disk-readiness:target"}, Endpoints: []string{"127.0.0.1:49222"},
	}}
	session := integrationSession{ID: 12, State: "Connected"}
	session.Initiator.Name = f.initiatorName()
	session.Target.Name = f.manifest.Targets[0]
	session.Target.Portal = f.manifest.Endpoints[0]
	backing := ctlLUN{Name: session.Target.Name + ",lun,0", File: "/dev/zvol/" + f.manifest.Pool + "/volume", Serial: "owned-serial", Blocks: 128, Blocksize: 512}
	inventory, err := xml.Marshal(ctlLUNs{LUNs: []ctlLUN{backing}})
	if err != nil {
		t.Fatal(err)
	}
	stage, inquiries, geometries := 0, 0, 0
	f.service = &Service{runtime: &targetRuntime{run: func(_ context.Context, _, command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/iscsictl":
			device := "da8"
			if stage == 0 {
				device = "probe0"
			}
			session.Devices.LUNs = []integrationDevice{{Number: 0, Device: device}}
			data, err := json.Marshal(map[string]any{"iscsictl": map[string]any{"session": []integrationSession{session}}})
			return string(data), err
		case "/usr/sbin/ctladm":
			if slices.Equal(args, []string{"devlist", "-x"}) {
				return string(inventory), nil
			}
		case "/sbin/camcontrol":
			if slices.Equal(args, []string{"inquiry", "da8", "-S"}) {
				inquiries++
				if stage == 1 {
					return "", errors.New("CAM disk is not ready")
				}
				return "owned-serial\n", nil
			}
		case "/usr/sbin/diskinfo":
			if slices.Equal(args, []string{"/dev/da8"}) {
				geometries++
				if stage == 2 {
					return "", errors.New("disk node is not ready")
				}
				return "/dev/da8 512 65536", nil
			}
		}
		t.Fatalf("unexpected disk command: %s %v", command, args)
		return "", nil
	}}}
	for stage = 0; stage < 4; stage++ {
		device, err := f.readyDisk(t.Context(), session.Target.Name, session.Target.Portal)
		if err != nil {
			t.Fatal(err)
		}
		if stage < 3 {
			if device != "" || len(f.manifest.Disks) != 0 {
				t.Fatalf("unready disk accepted at stage %d", stage)
			}
		} else if device != "da8" || !slices.Contains(f.manifest.Disks, integrationDisk{Device: "da8", Serial: "owned-serial"}) {
			t.Fatal("ready disk identity was not returned and recorded")
		}
	}
	if inquiries != 3 || geometries != 2 {
		t.Fatalf("inquiries=%d geometries=%d", inquiries, geometries)
	}
	data, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
	var saved integrationManifest
	if err != nil || json.Unmarshal(data, &saved) != nil || !slices.Contains(saved.Disks, integrationDisk{Device: "da8", Serial: "owned-serial"}) {
		t.Fatal("ready disk identity was not saved before I/O")
	}
}

func TestISCSIFixtureDiskRejectsUnprovenIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ctlLUN, *string, *string)
	}{
		{name: "foreign-backing", change: func(lun *ctlLUN, _, _ *string) { lun.File = "/dev/zvol/non-test/volume" }},
		{name: "missing-serial", change: func(lun *ctlLUN, _, _ *string) { lun.Serial = "" }},
		{name: "foreign-cam-serial", change: func(_ *ctlLUN, serial, _ *string) { *serial = "foreign-serial" }},
		{name: "wrong-blocksize", change: func(_ *ctlLUN, _, geometry *string) { *geometry = "/dev/da8 4096 65536" }},
		{name: "wrong-capacity", change: func(_ *ctlLUN, _, geometry *string) { *geometry = "/dev/da8 512 32768" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := &iscsiIntegrationFixture{directory: t.TempDir(), manifest: integrationManifest{
				RunID: "disk-identity", Pool: "sylve-test-owned",
				Targets: []string{iscsiTestIQNPrefix + "disk-identity:target"}, Endpoints: []string{"127.0.0.1:49222"},
			}}
			session := integrationSession{ID: 12, State: "Connected"}
			session.Initiator.Name = f.initiatorName()
			session.Target.Name = f.manifest.Targets[0]
			session.Target.Portal = f.manifest.Endpoints[0]
			session.Devices.LUNs = []integrationDevice{{Number: 0, Device: "da8"}}
			backing := ctlLUN{Name: session.Target.Name + ",lun,0", File: "/dev/zvol/" + f.manifest.Pool + "/volume", Serial: "owned-serial", Blocks: 128, Blocksize: 512}
			serial, geometry := "owned-serial", "/dev/da8 512 65536"
			test.change(&backing, &serial, &geometry)
			f.service = &Service{runtime: &targetRuntime{run: func(_ context.Context, _, command string, _ ...string) (string, error) {
				switch command {
				case "/usr/bin/iscsictl":
					data, err := json.Marshal(map[string]any{"iscsictl": map[string]any{"session": []integrationSession{session}}})
					return string(data), err
				case "/usr/sbin/ctladm":
					data, err := xml.Marshal(ctlLUNs{LUNs: []ctlLUN{backing}})
					return string(data), err
				case "/sbin/camcontrol":
					return serial, nil
				case "/usr/sbin/diskinfo":
					return geometry, nil
				default:
					t.Fatalf("unsafe disk command: %s", command)
					return "", nil
				}
			}}}
			if device, err := f.readyDisk(t.Context(), session.Target.Name, session.Target.Portal); err == nil || device != "" || len(f.manifest.Disks) != 0 {
				t.Fatalf("unproven disk accepted: device=%q error=%v", device, err)
			}
		})
	}
}
