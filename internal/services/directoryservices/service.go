// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

var (
	ErrInvalid     = errors.New("invalid_directory_service_config")
	ErrNotFound    = errors.New("directory_service_entry_not_found")
	ErrConflict    = errors.New("directory_service_entry_in_use")
	ErrBusy        = errors.New("directory_service_operation_in_progress")
	ErrUnavailable = errors.New("directory_service_unavailable")
)

const MaxRequestBodyBytes int64 = 2 * 1024 * 1024

type commandRunner func(context.Context, string, []string, string, []string) (string, error)

type Service struct {
	DB *gorm.DB
	// Samba owns its generated configuration; callbacks avoid a service cycle.
	WriteSamba              func(context.Context, bool) error
	ValidateSamba           func(context.Context, directoryModels.ActiveDirectory) error
	SharePrincipalsInUse    func() (bool, error)
	WithServiceSettingsLock func(func() error) error

	mu                  sync.Mutex
	stateMu             sync.RWMutex
	state               string
	lastError           string
	checkedAt           time.Time
	health              controllerHealth
	run                 commandRunner
	lookupSRV           func(context.Context, string, string, string) (string, []*net.SRV, error)
	krbPath             string
	keytabPath          string
	machineKeytabPath   string
	nssPath             string
	smbPath             string
	dataPath            string
	principalCache      principalSearchCache
	checkSambaStopped   func(context.Context) error
	recoveryServicePath string
}

func NewService(db *gorm.DB) *Service {
	return &Service{
		DB: db, run: runCommand, lookupSRV: net.DefaultResolver.LookupSRV,
		krbPath: "/etc/krb5.conf", keytabPath: "/etc/krb5.keytab",
		machineKeytabPath: MachineKeytabPath, nssPath: "/etc/nsswitch.conf",
		smbPath:             "/usr/local/etc/smb4.conf",
		recoveryServicePath: "/usr/local/etc/rc.d/samba_server",
	}
}

// Commands never contain passwords in argv or inherit a user's ticket cache.
func runCommand(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	// Do not borrow administrator tickets or Samba password environment variables.
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "KRB5_") || strings.HasPrefix(key, "PASSWD") || key == "PASSWORD" || key == "LC_ALL" {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "KRB5CCNAME=MEMORY:sylve-directory-services")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = strings.NewReader(input)
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	text := output.String()
	if password := strings.TrimSuffix(input, "\n"); password != "" {
		text = strings.ReplaceAll(text, password, "[REDACTED]")
	}
	return text, err
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 1024*1024 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func (s *Service) command(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := s.run(ctx, path, args, input, append([]string{"KRB5_CONFIG=" + s.krbPath}, env...))
	if err != nil {
		if input != "" {
			// Never echo credential-command output, even if it contains only a
			// truncated/partially printed password that literal redaction misses.
			detail := "Kerberos ticket acquisition failed; verify credentials, DNS, and time synchronization"
			if ctx.Err() != nil {
				detail = ctx.Err().Error()
			}
			return "", fmt.Errorf("%w: %s: %s", ErrUnavailable, filepath.Base(path), detail)
		}
		detail := fmt.Sprintf("%v: %s", err, strings.TrimSpace(out))
		if len(detail) > 4096 {
			detail = detail[:4096] + " [truncated]"
		}
		return "", fmt.Errorf("%w: %s: %s", ErrUnavailable, filepath.Base(path), detail)
	}
	return out, nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func recordError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) || (err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")) {
		return fmt.Errorf("%w: name already exists", ErrConflict)
	}
	return err
}

func (s *Service) setState(state string, err error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.state, s.lastError = state, ""
	if err != nil {
		s.lastError = err.Error()
	}
	if state == "HEALTHY" || state == "FAULTED" {
		s.checkedAt = time.Now().UTC()
	}
	if state == "" {
		s.checkedAt = time.Time{}
		s.health = controllerHealth{}
	}
}

// Share mutations and membership changes must not race with ID-map validation.
// WriteSamba is called with this lock held, so configuration generation itself
// deliberately does not acquire it again.
func (s *Service) LockConfiguration() (func(), error) {
	if !s.mu.TryLock() {
		return nil, ErrBusy
	}
	if err := s.recoveryReady(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	return s.mu.Unlock, nil
}

// Dataset deletion has already completed before Samba cleanup is called. Wait
// for membership/configuration writes rather than dropping this reconciliation.
func (s *Service) LockConfigurationForCleanup() func() {
	s.mu.Lock()
	return s.mu.Unlock
}

func (s *Service) runtimeDirectoryPath() (string, error) {
	root := s.dataPath
	if root == "" {
		var err error
		root, err = config.GetDataPath()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "directory-services"), nil
}

func (s *Service) runtimeDir() (string, error) {
	dir, err := s.runtimeDirectoryPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

type fileSnapshot struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshot(path string) (fileSnapshot, error) {
	f := fileSnapshot{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() {
		return f, fmt.Errorf("refusing to replace non-regular file %s", path)
	}
	f.data, err = os.ReadFile(path)
	f.mode, f.exists = info.Mode().Perm(), true
	return f, err
}

func restoreFiles(files []fileSnapshot) error {
	var errs []error
	for _, f := range files {
		var err error
		if f.exists {
			err = utils.AtomicWriteFile(f.path, f.data, f.mode)
		} else {
			err = os.Remove(f.path)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func backupFile(path string, mode os.FileMode) error {
	if info, err := os.Lstat(path + ".pre-sylve"); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular backup %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := snapshot(path)
	if err != nil || !f.exists {
		return err
	}
	return utils.AtomicWriteFile(path+".pre-sylve", f.data, mode)
}

// Reconcile only configured features. A fresh installation never rewrites
// Kerberos/NSS files just because this service exists.
func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.recoveryReady(); err != nil {
		s.setState("FAULTED", err)
		return err
	}
	ad, err := s.GetAD()
	if err != nil {
		return err
	}
	if ad.MembershipPending == "join" {
		if _, err := s.netCommand(ctx, ad, nil, "ads", "testjoin"); err == nil {
			// Keep the recovery marker until Samba/NSS/keytab setup has also
			// completed. The monitor retries it after Samba starts.
			ad.Joined = true
			ad.JoinWarning = recoveredJoinWarning(ad.JoinWarning, ad.SkipDNSRegistration)
			if err := s.DB.Save(&ad).Error; err != nil {
				return err
			}
		}
	}
	var realms, keytabs int64
	if err := s.DB.Model(&directoryModels.KerberosRealm{}).Count(&realms).Error; err != nil {
		return err
	}
	if err := s.DB.Model(&directoryModels.KerberosKeytab{}).Count(&keytabs).Error; err != nil {
		return err
	}
	settings, err := s.GetKerberosSettings()
	if err != nil {
		return err
	}
	if ad.Joined || ad.Enabled || realms > 0 || keytabs > 0 || settings.AppDefaults != "" || settings.LibDefaults != "" {
		var machineErr error
		if ad.Joined {
			machineErr = s.syncMachineKeytab(ctx)
		}
		if err := s.writeKerberos(s.DB); err != nil {
			return errors.Join(machineErr, err)
		}
		if machineErr != nil {
			s.setState("FAULTED", machineErr)
		}
	}
	if _, err := os.Lstat(s.nssPath + ".pre-sylve"); ad.Joined || err == nil {
		return s.writeNSS(ad.Enabled)
	}
	return nil
}

func (s *Service) Monitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	check := func() {
		if s.RecoveryPending() {
			return
		}
		ad, err := s.GetAD()
		if err != nil || !ad.Enabled || ad.MembershipPending == "leave" {
			return
		}
		if ad.MembershipPending == "join" {
			_, _ = s.Status(ctx, true)
			return
		}
		if !ad.Joined {
			return
		}
		if s.mu.TryLock() {
			if s.RecoveryPending() {
				s.mu.Unlock()
				return
			}
			err = s.syncMachineKeytab(ctx)
			// Also restore the merged keytab if a legacy refresh cannot reach
			// the DC. Its last saved credentials must remain usable offline.
			err = errors.Join(err, s.writeKerberos(s.DB))
			s.mu.Unlock()
			if err != nil {
				s.setState("FAULTED", err)
				logger.L.Warn().Err(err).Msg("directory_services_keytab_sync_failed")
				return
			}
		}
		_, _ = s.Status(ctx, true)
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
