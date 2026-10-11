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
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/pkg/utils"
	"golang.org/x/crypto/argon2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

const MaxRecoveryBytes = 64 * 1024 * 1024
const MaxRecoveryRequestBytes int64 = 96 * 1024 * 1024
const recoveryMagic = "SYLVEAD1"

type RecoveryInput struct {
	Passphrase          string `json:"passphrase"`
	File                []byte `json:"file,omitempty"`
	ConfirmComputerName string `json:"confirmComputerName"`
	ManageServices      bool   `json:"manageServices"`
}

type RecoveryInfo struct {
	Pending bool `json:"pending"`
}

type recoveryFile struct {
	Data   []byte      `json:"data"`
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode"`
}

type recoveryBundle struct {
	Version        int                     `json:"version"`
	Hostname       string                  `json:"hostname"`
	Created        time.Time               `json:"created"`
	SambaVersion   string                  `json:"sambaVersion"`
	Database       []byte                  `json:"database"`
	Files          map[string]recoveryFile `json:"files"`
	ResumeServices []string                `json:"resumeServices,omitempty"`
}

type recoveryState struct {
	AD       directoryModels.ActiveDirectory
	Realms   []directoryModels.KerberosRealm
	Keytabs  []directoryModels.KerberosKeytab
	Settings directoryModels.KerberosSettings
	IDs      map[string]directoryModels.Principal
}

func validateRecoveryInput(input RecoveryInput) error {
	if len(input.Passphrase) < 12 || len(input.Passphrase) > 1024 {
		return invalid("a recovery passphrase of 12 to 1024 bytes is required")
	}
	if len(input.File) > MaxRecoveryBytes {
		return invalid("the encrypted recovery file exceeds 64 MiB")
	}
	return nil
}

func recoveryCipher(passphrase string, salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealRecovery(bundle recoveryBundle, passphrase string) ([]byte, error) {
	plain, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	if len(plain) > MaxRecoveryBytes-64 {
		return nil, invalid("the recovery state exceeds the 64 MiB bundle limit")
	}
	header := make([]byte, len(recoveryMagic)+16+12)
	copy(header, recoveryMagic)
	if _, err := rand.Read(header[len(recoveryMagic):]); err != nil {
		return nil, err
	}
	aead, err := recoveryCipher(passphrase, header[8:24])
	if err != nil {
		return nil, err
	}
	return aead.Seal(header, header[24:36], plain, header[:24]), nil
}

func openRecovery(data []byte, passphrase string) (recoveryBundle, error) {
	if len(data) < 52 || len(data) > MaxRecoveryBytes || string(data[:8]) != recoveryMagic {
		return recoveryBundle{}, invalid("the recovery file format is invalid")
	}
	aead, err := recoveryCipher(passphrase, data[8:24])
	if err != nil {
		return recoveryBundle{}, err
	}
	plain, err := aead.Open(nil, data[24:36], data[36:], data[:24])
	if err != nil {
		return recoveryBundle{}, invalid("the recovery passphrase is incorrect or the file is damaged")
	}
	defer clear(plain)
	var bundle recoveryBundle
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil || bundle.Version != 1 || len(bundle.Database) == 0 || len(bundle.Files) > 20 {
		return recoveryBundle{}, invalid("the recovery file contents are invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return recoveryBundle{}, invalid("the recovery file contents are invalid")
	}
	if len(bundle.ResumeServices) > 3 {
		return recoveryBundle{}, invalid("the recovery service state is invalid")
	}
	for _, daemon := range bundle.ResumeServices {
		if daemon != "nmbd" && daemon != "smbd" && daemon != "winbindd" {
			return recoveryBundle{}, invalid("the recovery service state is invalid")
		}
	}
	host, err := os.Hostname()
	if err != nil || !strings.EqualFold(bundle.Hostname, host) {
		return recoveryBundle{}, fmt.Errorf("%w: the recovery file belongs to a different node", ErrConflict)
	}
	return bundle, nil
}

func (s *Service) recoveryJournal() (string, error) {
	dir, err := s.runtimeDirectoryPath()
	return filepath.Join(dir, "recovery-pending.enc"), err
}

// A pending journal is deliberately not restored at boot. Management remains
// available, but Samba must stay stopped until an administrator rolls it back.
func (s *Service) RecoveryPending() bool {
	path, err := s.recoveryJournal()
	if err != nil {
		return true
	}
	_, err = os.Lstat(path)
	return !os.IsNotExist(err)
}

func (s *Service) recoveryReady() error {
	if s.RecoveryPending() {
		return fmt.Errorf("%w: an interrupted directory services restore must be rolled back in Recovery before making changes", ErrConflict)
	}
	return nil
}

func (s *Service) requireStoppedSamba(ctx context.Context) error {
	if s.checkSambaStopped != nil {
		return s.checkSambaStopped(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	jid, err := s.sambaJailID(ctx)
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "/usr/bin/pgrep", []string{"-j", jid, "-x", "smbd|nmbd|winbindd|samba"}, "", nil)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: unable to verify the Samba service state", ErrUnavailable)
	}
	return fmt.Errorf("%w: stop Samba and Winbind before exporting or restoring recovery state", ErrConflict)
}

func (s *Service) recoveryPaths(ctx context.Context) (map[string]string, error) {
	text, err := s.command(ctx, "/usr/local/bin/testparm", []string{"-s", "-v", s.smbPath}, "", nil)
	if err != nil {
		return nil, err
	}
	dirs := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			dirs[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"private dir", "state directory", "cache directory"} {
		value := dirs[key]
		if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == "/" {
			return nil, fmt.Errorf("%w: unable to determine the Samba recovery directories", ErrUnavailable)
		}
		info, err := os.Lstat(value)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%w: Samba recovery directories must be existing directories", ErrUnavailable)
		}
	}
	keyDir, err := s.runtimeDir()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"encryption-key":     filepath.Join(keyDir, "keytab.key"),
		"machine-keytab":     s.machineKeytabPath,
		"system-keytab":      s.keytabPath,
		"original-keytab":    s.keytabPath + ".pre-sylve",
		"kerberos-config":    s.krbPath,
		"original-kerberos":  s.krbPath + ".pre-sylve",
		"nss-config":         s.nssPath,
		"original-nss":       s.nssPath + ".pre-sylve",
		"secrets.tdb":        filepath.Join(dirs["private dir"], "secrets.tdb"),
		"winbindd_idmap.tdb": filepath.Join(dirs["state directory"], "winbindd_idmap.tdb"),
		"autorid.tdb":        filepath.Join(dirs["state directory"], "autorid.tdb"),
		"group_mapping.tdb":  filepath.Join(dirs["state directory"], "group_mapping.tdb"),
		"winbindd_cache.tdb": filepath.Join(dirs["state directory"], "winbindd_cache.tdb"),
		"gencache.tdb":       filepath.Join(dirs["cache directory"], "gencache.tdb"),
	}, nil
}

func (s *Service) captureRecovery(ctx context.Context, paths map[string]string) (recoveryBundle, error) {
	bundle := recoveryBundle{Version: 1, Created: time.Now().UTC(), Files: map[string]recoveryFile{}}
	var err error
	bundle.Hostname, err = os.Hostname()
	if err != nil {
		return bundle, err
	}
	bundle.SambaVersion, err = s.command(ctx, "/usr/local/bin/net", []string{"--version"}, "", nil)
	if err != nil {
		return bundle, err
	}
	bundle.SambaVersion = strings.TrimSpace(bundle.SambaVersion)
	if _, err := supportsMachineKeytabSync(bundle.SambaVersion); err != nil {
		return bundle, err
	}
	total := 0
	for name, path := range paths {
		if info, err := os.Lstat(path); err == nil && info.Size() > MaxRecoveryBytes {
			return bundle, invalid("a recovery state file exceeds 64 MiB")
		}
		file, err := snapshot(path)
		if err != nil {
			return bundle, fmt.Errorf("%w: a recovery file could not be safely read", ErrUnavailable)
		}
		bundle.Files[name] = recoveryFile{Data: file.data, Exists: file.exists, Mode: file.mode}
		total += len(file.data)
		if total > MaxRecoveryBytes*3/4 {
			return bundle, invalid("the recovery state exceeds the 64 MiB bundle limit")
		}
	}
	dir, err := s.runtimeDir()
	if err != nil {
		return bundle, err
	}
	stage, err := os.MkdirTemp(dir, "recovery-")
	if err != nil {
		return bundle, err
	}
	defer os.RemoveAll(stage)
	database := filepath.Join(stage, "sylve.db")
	if err := s.DB.WithContext(ctx).Exec("VACUUM INTO ?", database).Error; err != nil {
		return bundle, fmt.Errorf("%w: the application database snapshot could not be created", ErrUnavailable)
	}
	if err := os.Chmod(database, 0600); err != nil {
		return bundle, err
	}
	if info, err := os.Stat(database); err != nil || info.Size()+int64(total) > MaxRecoveryBytes*3/4 {
		return bundle, invalid("the recovery state exceeds the 64 MiB bundle limit")
	}
	bundle.Database, err = os.ReadFile(database)
	return bundle, err
}

func readRecoveryState(db *gorm.DB) (recoveryState, error) {
	state := recoveryState{IDs: map[string]directoryModels.Principal{}}
	if err := db.First(&state.AD, 1).Error; err != nil {
		return state, err
	}
	if err := db.Order("id").Find(&state.Realms).Error; err != nil {
		return state, err
	}
	if err := db.Order("id").Find(&state.Keytabs).Error; err != nil {
		return state, err
	}
	var err error
	state.Settings, err = getKerberosSettings(db)
	if err != nil {
		return state, err
	}
	if db.Migrator().HasTable(&sambaModels.SambaShare{}) {
		var shares []sambaModels.SambaShare
		if err := db.Select("directory_permissions").Find(&shares).Error; err != nil {
			return state, err
		}
		for _, share := range shares {
			for _, principal := range append(share.DirectoryPermissions.Read, share.DirectoryPermissions.Write...) {
				if previous, ok := state.IDs[principal.SID]; ok && (previous.UnixID != principal.UnixID || previous.Kind != principal.Kind) {
					return state, invalid("saved share permissions contain inconsistent identity mappings")
				}
				state.IDs[principal.SID] = principal
			}
		}
	}
	return state, nil
}

func openRecoveryDatabase(dir string, image []byte) (*gorm.DB, error) {
	if !bytes.HasPrefix(image, []byte("SQLite format 3\x00")) {
		return nil, invalid("the recovery database format is invalid")
	}
	path := filepath.Join(dir, "sylve.db")
	if err := utils.AtomicWriteFile(path, image, 0600); err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("PRAGMA trusted_schema = OFF").Error; err != nil {
		sqlDB.Close()
		return nil, err
	}
	var integrity string
	if err := db.Raw("PRAGMA quick_check").Scan(&integrity).Error; err != nil || integrity != "ok" {
		sqlDB.Close()
		return nil, invalid("the recovery database failed its integrity check")
	}
	return db, nil
}

func (s *Service) withRecoveryLock(operation func() error) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if s.WithServiceSettingsLock != nil {
		return s.WithServiceSettingsLock(operation)
	}
	return operation()
}

func (s *Service) ExportRecovery(ctx context.Context, input RecoveryInput) (result []byte, err error) {
	if err := validateRecoveryInput(input); err != nil {
		return nil, err
	}
	err = s.withRecoveryLock(func() error {
		if err := s.recoveryReady(); err != nil {
			return err
		}
		ad, err := s.GetAD()
		if err != nil {
			return err
		}
		if !ad.Joined || ad.MembershipPending != "" || input.ConfirmComputerName != ad.ComputerName {
			return fmt.Errorf("%w: confirm the joined computer name before exporting recovery state", ErrConflict)
		}
		return s.withRecoveryServices(ctx, input.ManageServices, nil, func([]string) error {
			if err := s.requireStoppedSamba(ctx); err != nil {
				return err
			}
			paths, err := s.recoveryPaths(ctx)
			if err != nil {
				return err
			}
			bundle, err := s.captureRecovery(ctx, paths)
			if err != nil {
				return err
			}
			if !bundle.Files["secrets.tdb"].Exists || len(bundle.Files["encryption-key"].Data) != 32 || !bundle.Files["machine-keytab"].Exists {
				return fmt.Errorf("%w: complete machine trust secrets, keytab and encryption key are required for export", ErrConflict)
			}
			if validateLegacyMachineKeytab(bundle.Files["machine-keytab"].Data, ad) != nil {
				return fmt.Errorf("%w: a complete machine keytab is required for export", ErrConflict)
			}
			if !bundle.Files["original-keytab"].Exists {
				return fmt.Errorf("%w: the original system keytab backup is required for export", ErrConflict)
			}
			if _, err := parseKeytab(bundle.Files["original-keytab"].Data); err != nil {
				return invalid("the original system keytab backup is invalid")
			}
			if ad.IDMapBackend == "autorid" && !bundle.Files["autorid.tdb"].Exists {
				return fmt.Errorf("%w: the AUTORID allocation database is required for export", ErrConflict)
			}
			if err := s.requireStoppedSamba(ctx); err != nil {
				return err
			}
			result, err = sealRecovery(bundle, input.Passphrase)
			return err
		})
	})
	return result, err
}

func sameRecoveryConfiguration(a, b directoryModels.ActiveDirectory) bool {
	a.JoinPasswordCiphertext, b.JoinPasswordCiphertext = nil, nil
	a.JoinPasswordConfigured, b.JoinPasswordConfigured = false, false
	a.JoinWarning, b.JoinWarning = "", ""
	return reflect.DeepEqual(a, b)
}

func maxKeytabKVNO(data []byte) (uint32, error) {
	entries, err := parseKeytab(data)
	var result uint32
	for _, entry := range entries {
		if entry.kvno > result {
			result = entry.kvno
		}
	}
	return result, err
}

func (s *Service) validateRecovery(ctx context.Context, bundle recoveryBundle, state recoveryState, paths map[string]string, stage string) error {
	current, err := readRecoveryState(s.DB)
	if err != nil {
		return err
	}
	if !state.AD.Joined || state.AD.MembershipPending != "" || !sameRecoveryConfiguration(current.AD, state.AD) {
		return fmt.Errorf("%w: recovery requires the same membership, computer name, domain and saved configuration", ErrConflict)
	}
	version, err := s.command(ctx, "/usr/local/bin/net", []string{"--version"}, "", nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(version) != bundle.SambaVersion {
		return fmt.Errorf("%w: the installed Samba version differs from the recovery snapshot", ErrConflict)
	}
	if !bytes.Equal(current.AD.JoinPasswordCiphertext, state.AD.JoinPasswordCiphertext) {
		return fmt.Errorf("%w: saved enrollment credentials differ from the recovery snapshot", ErrConflict)
	}
	if !reflect.DeepEqual(current.Realms, state.Realms) || current.Settings.AppDefaults != state.Settings.AppDefaults || current.Settings.LibDefaults != state.Settings.LibDefaults {
		return fmt.Errorf("%w: Kerberos configuration differs from the recovery snapshot", ErrConflict)
	}
	for sid, principal := range current.IDs {
		previous, ok := state.IDs[sid]
		if !ok || previous.UnixID != principal.UnixID || previous.Kind != principal.Kind {
			return fmt.Errorf("%w: saved share identities differ from the recovery snapshot", ErrConflict)
		}
	}
	if len(current.Keytabs) != len(state.Keytabs) {
		return fmt.Errorf("%w: saved keytabs differ from the recovery snapshot", ErrConflict)
	}
	for i, row := range current.Keytabs {
		old := state.Keytabs[i]
		if row.ID != old.ID || row.Name != old.Name || row.Managed != old.Managed || !reflect.DeepEqual(row.Principals, old.Principals) {
			return fmt.Errorf("%w: saved keytabs differ from the recovery snapshot", ErrConflict)
		}
		if !row.Managed && !bytes.Equal(row.Ciphertext, old.Ciphertext) {
			return fmt.Errorf("%w: saved keytab credentials differ from the recovery snapshot", ErrConflict)
		}
	}
	if err := validateRecoveryFiles(bundle, paths); err != nil {
		return err
	}
	// ID allocations can outlive saved share permissions in on-disk ACLs.
	// Never rewind a still-present allocation database, even when every
	// principal currently named in the editor appears unchanged.
	for _, name := range []string{"winbindd_idmap.tdb", "autorid.tdb", "group_mapping.tdb"} {
		file, err := snapshot(paths[name])
		if err != nil {
			return err
		}
		if file.exists && (!bundle.Files[name].Exists || !bytes.Equal(file.data, bundle.Files[name].Data)) {
			return fmt.Errorf("%w: existing Samba ID allocations differ from the recovery snapshot; older allocations cannot be restored", ErrConflict)
		}
	}
	key := bundle.Files["encryption-key"].Data
	if len(key) != 32 || !bundle.Files["secrets.tdb"].Exists {
		return invalid("the recovery file is missing machine trust secrets or the encryption key")
	}
	if !bundle.Files["original-keytab"].Exists {
		return invalid("the recovery file is missing the original system keytab backup")
	}
	if _, err := parseKeytab(bundle.Files["original-keytab"].Data); err != nil {
		return invalid("the recovery original system keytab backup is invalid")
	}
	if original, err := os.ReadFile(paths["original-keytab"]); err == nil && !bytes.Equal(original, bundle.Files["original-keytab"].Data) {
		return fmt.Errorf("%w: the original system keytab differs from the recovery snapshot", ErrConflict)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if existing, err := os.ReadFile(paths["encryption-key"]); err == nil && !bytes.Equal(existing, key) {
		return fmt.Errorf("%w: the current encryption key does not match the recovery snapshot", ErrConflict)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	decrypt := func(data []byte, purpose string) ([]byte, error) {
		if len(data) < aead.NonceSize() {
			return nil, invalid("the recovery file contains invalid encrypted credentials")
		}
		plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(purpose))
		if err != nil {
			return nil, invalid("the recovery credentials do not match the encryption key")
		}
		return plain, nil
	}
	for _, row := range state.Keytabs {
		plain, err := decrypt(row.Ciphertext, "sylve-kerberos-keytab-v1")
		if err != nil {
			return err
		}
		_, err = parseKeytab(plain)
		clear(plain)
		if err != nil {
			return invalid("the recovery file contains an invalid keytab")
		}
	}
	if len(state.AD.JoinPasswordCiphertext) > 0 {
		plain, err := decrypt(state.AD.JoinPasswordCiphertext, joinPasswordPurpose(state.AD))
		clear(plain)
		if err != nil {
			return err
		}
	}
	backupKVNO, err := maxKeytabKVNO(bundle.Files["machine-keytab"].Data)
	if err != nil || backupKVNO == 0 || validateLegacyMachineKeytab(bundle.Files["machine-keytab"].Data, state.AD) != nil {
		return invalid("the recovery machine keytab is invalid")
	}
	if current, err := os.ReadFile(s.machineKeytabPath); err == nil {
		kvno, err := maxKeytabKVNO(current)
		if err != nil || kvno > backupKVNO {
			return fmt.Errorf("%w: the recovery machine credentials are older than the installed keys", ErrConflict)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if state.AD.IDMapBackend == "autorid" && !bundle.Files["autorid.tdb"].Exists {
		return invalid("the recovery file is missing the AUTORID allocation database")
	}
	for _, subdir := range []string{"private", "state", "cache", "lock", "pid"} {
		if err := os.Mkdir(filepath.Join(stage, subdir), 0700); err != nil {
			return err
		}
	}
	for name, file := range bundle.Files {
		if !strings.HasSuffix(name, ".tdb") || !file.Exists {
			continue
		}
		dir := "state"
		if name == "secrets.tdb" {
			dir = "private"
		}
		path := filepath.Join(stage, dir, name)
		if err := utils.AtomicWriteFile(path, file.Data, 0600); err != nil {
			return err
		}
		if _, err := s.run(ctx, "/usr/local/bin/tdbbackup", []string{"-v", path}, "", nil); err != nil {
			return invalid("a recovery Samba database failed its integrity check")
		}
	}
	text, err := os.ReadFile(s.smbPath)
	if err != nil {
		return err
	}
	config := filepath.Join(stage, "smb4.conf")
	if err := utils.AtomicWriteFile(config, text, 0600); err != nil {
		return err
	}
	discovery, err := s.DiscoverAD(ctx, discoveryInput(state.AD))
	if err != nil {
		return err
	}
	if err := validateDiscoveredWorkgroup(state.AD, discovery); err != nil {
		return err
	}
	args := []string{"--configfile=" + config, "--realm=" + state.AD.Domain, "--workgroup=" + state.AD.Workgroup, "--server=" + discovery.DomainController,
		"--option=security=ads", "--option=server role=member server", "--option=netbios name=" + state.AD.ComputerName, "--option=kerberos method=secrets only"}
	for _, pair := range [][2]string{{"private dir", "private"}, {"state directory", "state"}, {"cache directory", "cache"}, {"lock directory", "lock"}, {"pid directory", "pid"}} {
		args = append(args, "--option="+pair[0]+"="+filepath.Join(stage, pair[1]))
	}
	args = append(args, "--machine-pass", "ads", "testjoin")
	if _, err := s.command(ctx, "/usr/local/bin/net", args, "", nil); err != nil {
		return fmt.Errorf("%w: the saved machine trust could not be verified with the domain; no recovery state was applied", ErrUnavailable)
	}
	return nil
}

func applyRecoveryDatabase(db *gorm.DB, state recoveryState) error {
	// A durable DB commit must precede removing the filesystem journal. The
	// normal application connection uses NORMAL synchronization in WAL mode.
	return db.Session(&gorm.Session{Logger: gormLogger.Default.LogMode(gormLogger.Silent)}).Connection(func(conn *gorm.DB) (err error) {
		var previous int
		if err := conn.Session(&gorm.Session{NewDB: true}).Raw("PRAGMA synchronous").Scan(&previous).Error; err != nil {
			return err
		}
		if err := conn.Session(&gorm.Session{NewDB: true}).Exec("PRAGMA synchronous = FULL").Error; err != nil {
			return err
		}
		defer func() {
			err = errors.Join(err, conn.Session(&gorm.Session{NewDB: true}).Exec(fmt.Sprintf("PRAGMA synchronous = %d", previous)).Error)
		}()
		return conn.Session(&gorm.Session{NewDB: true}).Transaction(func(tx *gorm.DB) error {
			for _, model := range []any{&directoryModels.KerberosKeytab{}, &directoryModels.KerberosRealm{}, &directoryModels.KerberosSettings{}} {
				if err := tx.Where("1 = 1").Delete(model).Error; err != nil {
					return err
				}
			}
			if len(state.Realms) > 0 {
				if err := tx.Create(&state.Realms).Error; err != nil {
					return err
				}
			}
			if len(state.Keytabs) > 0 {
				if err := tx.Create(&state.Keytabs).Error; err != nil {
					return err
				}
			}
			state.Settings.ID, state.AD.ID = 1, 1
			if err := tx.Save(&state.Settings).Error; err != nil {
				return err
			}
			return tx.Save(&state.AD).Error
		})
	})
}

func validateRecoveryFiles(bundle recoveryBundle, paths map[string]string) error {
	if len(bundle.Files) != len(paths) {
		return invalid("the recovery file is missing required state entries")
	}
	for name := range paths {
		file, ok := bundle.Files[name]
		if !ok || (!file.Exists && len(file.Data) > 0) || file.Mode&^0777 != 0 {
			return invalid("the recovery file contains invalid state entries")
		}
	}
	return nil
}

func applyRecoveryFiles(paths map[string]string, files map[string]recoveryFile) error {
	var snapshots []fileSnapshot
	for name, path := range paths {
		if name == "nss-config" || name == "original-nss" {
			continue
		}
		file, ok := files[name]
		if !ok {
			return invalid("the recovery file is missing a required state entry")
		}
		// Live files are checked again immediately before replacement. The
		// artifact never supplies filesystem paths or follows a symlink.
		if _, err := snapshot(path); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if name == "kerberos-config" || name == "original-kerberos" {
			mode = file.Mode
		}
		snapshots = append(snapshots, fileSnapshot{path: path, data: file.Data, mode: mode, exists: file.Exists})
	}
	if err := restoreFiles(snapshots); err != nil {
		return err
	}
	for _, file := range snapshots {
		if err := syncRecoveryDirectory(file.path); err != nil {
			return err
		}
	}
	return nil
}

func syncRecoveryDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func removeRecoveryJournal(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncRecoveryDirectory(path)
}

func (s *Service) RestoreRecovery(ctx context.Context, input RecoveryInput) error {
	if err := validateRecoveryInput(input); err != nil {
		return err
	}
	return s.withRecoveryLock(func() error {
		if err := s.recoveryReady(); err != nil {
			return err
		}
		ad, err := s.GetAD()
		if err != nil {
			return err
		}
		if input.ConfirmComputerName != ad.ComputerName {
			return invalid("confirm the computer name before restoring recovery state")
		}
		bundle, err := openRecovery(input.File, input.Passphrase)
		if err != nil {
			return err
		}
		return s.withRecoveryServices(ctx, input.ManageServices, nil, func(resume []string) error {
			if err := s.requireStoppedSamba(ctx); err != nil {
				return err
			}
			paths, err := s.recoveryPaths(ctx)
			if err != nil {
				return err
			}
			stage, err := os.MkdirTemp(filepath.Dir(paths["encryption-key"]), "restore-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(stage)
			db, err := openRecoveryDatabase(stage, bundle.Database)
			if err != nil {
				return invalid("the recovery database snapshot could not be opened")
			}
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			defer sqlDB.Close()
			state, err := readRecoveryState(db)
			if err != nil {
				return invalid("the recovery database snapshot is incomplete")
			}
			if err := s.validateRecovery(ctx, bundle, state, paths, stage); err != nil {
				return err
			}
			// The daemon may have rotated its keytab just before export, before
			// the monitor copied it into SQLite. Publish that captured keytab,
			// not a potentially older encrypted managed row from the DB image.
			block, _ := aes.NewCipher(bundle.Files["encryption-key"].Data)
			aead, _ := cipher.NewGCM(block)
			entries, _ := parseKeytab(bundle.Files["machine-keytab"].Data)
			foundManaged := false
			for i := range state.Keytabs {
				row := &state.Keytabs[i]
				if row.Managed && row.Name == MachineKeytabName {
					nonce := make([]byte, aead.NonceSize())
					if _, err := rand.Read(nonce); err != nil {
						return err
					}
					row.Ciphertext = aead.Seal(nonce, nonce, bundle.Files["machine-keytab"].Data, []byte("sylve-kerberos-keytab-v1"))
					row.Principals = keytabPrincipals(entries)
					row.UpdatedAt = bundle.Created
					foundManaged = true
				}
			}
			if !foundManaged {
				return invalid("the recovery snapshot is missing its managed machine keytab")
			}
			previous, err := s.captureRecovery(ctx, paths)
			if err != nil {
				return err
			}
			// Only the encrypted pending journal carries resumption intent; an
			// imported backup never decides which services to start.
			previous.ResumeServices = resume
			previousState, err := readRecoveryState(s.DB)
			if err != nil {
				return err
			}
			journal, err := sealRecovery(previous, input.Passphrase)
			if err != nil {
				return err
			}
			journalPath, err := s.recoveryJournal()
			if err != nil {
				return err
			}
			if err := utils.AtomicWriteFile(journalPath, journal, 0600); err != nil {
				return err
			}
			if err := syncRecoveryDirectory(journalPath); err != nil {
				return err
			}
			s.principalCache.clear()
			defer s.principalCache.clear()
			applyErr := s.requireStoppedSamba(ctx)
			if applyErr != nil {
				return errors.Join(applyErr, removeRecoveryJournal(journalPath))
			}
			// Neither NSS nor unrelated current configuration is restored from
			// an older snapshot. Only our Kerberos configuration is regenerated.
			bundle.Files["kerberos-config"] = previous.Files["kerberos-config"]
			bundle.Files["original-kerberos"] = previous.Files["original-kerberos"]
			for _, name := range []string{"winbindd_cache.tdb", "gencache.tdb"} {
				bundle.Files[name] = recoveryFile{}
			}
			applyErr = applyRecoveryFiles(paths, bundle.Files)
			if applyErr == nil {
				applyErr = applyRecoveryDatabase(s.DB, state)
			}
			if applyErr == nil {
				// Preserve current NSS content and regenerate only owned Kerberos
				// configuration. No share regeneration or filesystem ACL apply occurs.
				applyErr = s.writeKerberos(s.DB)
			}
			if applyErr == nil {
				applyErr = syncRecoveryDirectory(s.krbPath)
			}
			if applyErr == nil {
				applyErr = syncRecoveryDirectory(s.keytabPath)
			}
			if applyErr != nil {
				rollbackErr := errors.Join(applyRecoveryFiles(paths, previous.Files), applyRecoveryDatabase(s.DB, previousState))
				if rollbackErr == nil {
					rollbackErr = removeRecoveryJournal(journalPath)
				}
				if rollbackErr != nil {
					s.setState("FAULTED", fmt.Errorf("an interrupted directory services restore must be rolled back in Recovery before starting Samba"))
					return fmt.Errorf("%w: recovery failed and rollback needs administrator attention; keep Samba stopped", ErrUnavailable)
				}
				return fmt.Errorf("%w: recovery failed and the previous state was restored", ErrUnavailable)
			}
			if err := removeRecoveryJournal(journalPath); err != nil {
				return err
			}
			s.setState("", nil)
			return nil
		})
	})
}

func (s *Service) RollbackRecovery(ctx context.Context, input RecoveryInput) error {
	if err := validateRecoveryInput(input); err != nil {
		return err
	}
	return s.withRecoveryLock(func() error {
		ad, err := s.GetAD()
		if err != nil {
			return err
		}
		if input.ConfirmComputerName != ad.ComputerName {
			return invalid("confirm the computer name before rolling back recovery state")
		}
		path, err := s.recoveryJournal()
		if err != nil {
			return err
		}
		file, err := snapshot(path)
		if err != nil || !file.exists {
			return fmt.Errorf("%w: no readable pending recovery journal was found", ErrConflict)
		}
		bundle, err := openRecovery(file.data, input.Passphrase)
		if err != nil {
			return err
		}
		return s.withRecoveryServices(ctx, input.ManageServices, bundle.ResumeServices, func([]string) error {
			if err := s.requireStoppedSamba(ctx); err != nil {
				return err
			}
			paths, err := s.recoveryPaths(ctx)
			if err != nil {
				return err
			}
			stage, err := os.MkdirTemp(filepath.Dir(paths["encryption-key"]), "rollback-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(stage)
			db, err := openRecoveryDatabase(stage, bundle.Database)
			if err != nil {
				return invalid("the rollback database snapshot could not be opened")
			}
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			defer sqlDB.Close()
			state, err := readRecoveryState(db)
			if err != nil {
				return err
			}
			if err := validateRecoveryFiles(bundle, paths); err != nil {
				return err
			}
			if err := applyRecoveryFiles(paths, bundle.Files); err != nil {
				return err
			}
			if err := applyRecoveryDatabase(s.DB, state); err != nil {
				return err
			}
			if err := removeRecoveryJournal(path); err != nil {
				return err
			}
			s.principalCache.clear()
			s.setState("", nil)
			return nil
		})
	})
}
