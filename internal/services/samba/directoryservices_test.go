// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
)

func directoryTestPermissions() directoryModels.SharePermissions {
	return directoryModels.SharePermissions{
		Read:  []directoryModels.Principal{{SID: "S-1-5-21-111-222-333-1100", Name: "EXAMPLE\\alice", Kind: "user", UnixID: 1001100}},
		Write: []directoryModels.Principal{{SID: "S-1-5-21-111-222-333-2100", Name: "EXAMPLE\\File Users", Kind: "group", UnixID: 1002100}},
	}
}

func TestPendingDirectoryRecoveryBlocksSambaWrites(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	dir := filepath.Join(root, "directory-services")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "recovery-pending.enc")
	if err := os.WriteFile(marker, []byte("isolated-pending-journal"), 0600); err != nil {
		t.Fatal(err)
	}
	// No database or host command is needed to refuse a pending recovery.
	s := &Service{Directory: directoryservices.NewService(nil)}
	for _, operation := range []func() error{
		func() error { return s.WriteConfig(context.Background(), true) },
		func() error {
			unlock, err := s.lockDirectoryConfiguration()
			if unlock != nil {
				unlock()
			}
			return err
		},
	} {
		if err := operation(); !errors.Is(err, directoryservices.ErrConflict) {
			t.Fatalf("pending recovery allowed a Samba write: %v", err)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("Samba mutation discarded the protected recovery marker")
	}
}

func TestDomainOnlyShareEmitsSIDsAndNumericACLs(t *testing.T) {
	s, _ := newSambaServiceWithMockRunner(t)
	share := sambaModels.SambaShare{Name: "domain-only", Dataset: "guid", Enabled: true, DirectoryPermissions: directoryTestPermissions(), CreateMask: "0664", DirectoryMask: "2775"}
	text, err := s.shareConfig(context.Background(), []sambaModels.SambaShare{share}, false, map[string]*gzfs.Dataset{"guid": {Mountpoint: "/mnt/share"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"valid users = S-1-5-21-111-222-333-1100 S-1-5-21-111-222-333-2100", "write list = S-1-5-21-111-222-333-2100", "read only = yes", "guest ok = no"} {
		if !strings.Contains(text, line) {
			t.Fatalf("missing %s:\n%s", line, text)
		}
	}
	if strings.Contains(text, "force user") || strings.Contains(text, "guest only") {
		t.Fatal("domain access became guest or force-user access")
	}
	if strings.Contains(text, "EXAMPLE") || strings.Contains(text, "@S-1-") {
		t.Fatal("domain permissions depend on a mutable name or group-prefixed SID")
	}
	commands := []string{}
	old := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = old })
	sambaRunCommand = func(path string, args ...string) (string, error) {
		commands = append(commands, path+" "+strings.Join(args, " "))
		return "", nil
	}
	if err := s.syncSambaDatasetPrincipalACLs("/mnt/share", sambaPrincipalNames{}, namesFromShareACLs(share), true); err != nil {
		t.Fatal(err)
	}
	text = strings.Join(commands, "\n")
	if !strings.Contains(text, "-m u:1001100:"+readACLPerm) || !strings.Contains(text, "-m g:1002100:"+writeACLPerm) || strings.Contains(text, "EXAMPLE") {
		t.Fatalf("domain ACLs are not numeric:\n%s", text)
	}
}

func TestMemberConfigurationKeepsLocalAndDirectoryIdentitiesDistinct(t *testing.T) {
	share := sambaModels.SambaShare{ReadOnlyUsers: []models.User{{Username: "alice"}}, WriteableGroups: []models.Group{{Name: "staff"}}, DirectoryPermissions: directoryTestPermissions()}
	names := memberSharePrincipals(share, directoryModels.ActiveDirectory{Enabled: true, ComputerName: "SYLVE"})
	if !reflect.DeepEqual(names.ReadUsers, []string{"SYLVE\\alice", "S-1-5-21-111-222-333-1100"}) || !reflect.DeepEqual(names.WriteGroups, []string{"Unix Groups\\staff"}) || !reflect.DeepEqual(names.WriteUsers, []string{"S-1-5-21-111-222-333-2100"}) {
		t.Fatalf("local identities became domain identities: %+v", names)
	}
	names = memberSharePrincipals(share, directoryModels.ActiveDirectory{})
	if names.ReadUsers[0] != "alice" || names.WriteGroups[0] != "staff" {
		t.Fatalf("standalone access changed: %+v", names)
	}
}

func TestDirectorySettingsOverrideAdvancedMembershipGlobals(t *testing.T) {
	ad := directoryModels.ActiveDirectory{Enabled: true, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999}
	member, err := directoryservices.SambaConfig(ad, "Version 4.23.11")
	if err != nil {
		t.Fatal(err)
	}
	text := globalConfig(sambaModels.SambaSettings{Workgroup: "WORKGROUP", ExtraGlobalConfig: "security = user\nworkgroup = OTHER"}, false, member)
	if strings.LastIndex(text, "workgroup = EXAMPLE") < strings.LastIndex(text, "workgroup = OTHER") || strings.LastIndex(text, "security = ADS") < strings.LastIndex(text, "security = user") {
		t.Fatalf("advanced config reinterpreted member settings:\n%s", text)
	}
	if !strings.Contains(text, "sync_kvno:machine_password") {
		t.Fatal("machine keytab rotation is not enabled")
	}
}

func TestDirectoryConfigurationUsesDetectedSambaVersion(t *testing.T) {
	old := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = old })
	for _, version := range []string{"Version 4.16.11", "Version 4.20.9", "Version 4.21.0", "unknown"} {
		t.Run(version, func(t *testing.T) {
			ad := directoryModels.ActiveDirectory{Enabled: true, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999}
			s := &Service{directoryOverride: &ad}
			calls := 0
			sambaRunCommand = func(path string, args ...string) (string, error) {
				if path != "/usr/local/bin/net" || !reflect.DeepEqual(args, []string{"--version"}) {
					t.Fatalf("unexpected command: %s %v", path, args)
				}
				calls++
				return version, nil
			}
			text, err := s.directoryConfig("")
			if version == "unknown" {
				if !errors.Is(err, directoryservices.ErrUnavailable) || text != "" {
					t.Fatalf("unknown version guessed a workflow: %v\n%s", err, text)
				}
			} else if err != nil || strings.Contains(text, "sync machine password to keytab") != (version == "Version 4.21.0") {
				t.Fatalf("wrong generated configuration for %s: %v\n%s", version, err, text)
			}
			ad.Enabled = false
			if text, err := s.directoryConfig(""); err != nil || text != "" || calls != 1 {
				t.Fatal("disabled directory config still requires Samba version detection")
			}
		})
	}
}

func TestDirectoryMappingLocksCoverExtraGlobalConfig(t *testing.T) {
	old := sambaRunCommand
	oldPath := sambaConfigFilePath
	sambaConfigFilePath = filepath.Join(t.TempDir(), "smb4.conf")
	t.Cleanup(func() { sambaRunCommand, sambaConfigFilePath = old, oldPath })
	sambaRunCommand = func(path string, args ...string) (string, error) {
		if path == "/usr/local/bin/net" && reflect.DeepEqual(args, []string{"--version"}) {
			return "Version 4.23.11", nil
		}
		if path == sambaTestparmPath {
			return "[global]\n", nil
		}
		t.Fatalf("unexpected command: %s %v", path, args)
		return "", errors.New("unexpected command")
	}
	for _, mode := range []string{"enabled", "joined but disabled", "pending join", "pending leave", "saved disabled share"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newSambaServiceWithMockRunner(t)
			if err := s.DB.AutoMigrate(&directoryModels.ActiveDirectory{}); err != nil {
				t.Fatal(err)
			}
			s.Directory = directoryservices.NewService(s.DB)
			ad := directoryModels.ActiveDirectory{ID: 1, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999}
			switch mode {
			case "enabled":
				ad.Enabled, ad.Joined = true, true
			case "joined but disabled":
				ad.Joined = true
			case "pending join":
				ad.MembershipPending = "join"
			case "pending leave":
				ad.MembershipPending = "leave"
			case "saved disabled share":
				share := sambaModels.SambaShare{Name: "saved", Dataset: "guid", DirectoryPermissions: directoryTestPermissions()}
				if err := s.DB.Create(&share).Error; err != nil {
					t.Fatal(err)
				}
				if err := s.DB.Model(&share).Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := s.DB.Save(&ad).Error; err != nil {
				t.Fatal(err)
			}
			for _, extra := range []string{
				"idmap config EXAMPLE : base_rid = 100",
				"IDMAP CONFIG * : rangesize = 200000",
				"idmap\tconfig OTHER : backend = rid",
				"idmap \\\n config EXAMPLE : range = 2000000-2999999",
				"idmap \\ \t\r\n config EXAMPLE : base_rid = 100",
				"# comment \\\n idmap config EXAMPLE : base_rid = 100",
				"winbind separator = +",
				"winbind nss info = sfu",
				"winbind normalize names = yes",
				"username map = /etc/smbusers",
				"passdb backend = tdbsam:/other/passdb.tdb",
				"include = /etc/smb-extra.conf",
				"config backend = registry",
				"registry shares = yes",
				"[other]\npath = /",
			} {
				setExtraGlobalConfigValue(t, s, extra)
				if _, err := s.GlobalConfig(); !errors.Is(err, ErrInvalidGlobalConfig) {
					t.Fatalf("allowed mapping override %q: %v", extra, err)
				}
			}
			allowed := "# idmap config EXAMPLE : base_rid = 100\n; include = ignored\nserver min protocol = SMB3\nsecurity = user"
			setExtraGlobalConfigValue(t, s, allowed)
			if text, err := s.GlobalConfig(); err != nil || !strings.Contains(text, allowed) {
				t.Fatalf("rejected unrelated advanced settings: %v", err)
			}
		})
	}
}

func TestNativeMachineKeytabAliasesWithInstalledTestparm(t *testing.T) {
	if testing.Short() {
		t.Skip("installed Samba tooling check")
	}
	path, err := exec.LookPath("testparm")
	if err != nil {
		t.Skip("testparm is not installed")
	}
	version, err := exec.Command(filepath.Join(filepath.Dir(path), "net"), "--version").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	ad := directoryModels.ActiveDirectory{Enabled: true, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999}
	member, err := directoryservices.SambaConfig(ad, string(version))
	if err != nil || !strings.Contains(member, "sync machine password to keytab") {
		t.Skip("native Samba keytab synchronization is not installed")
	}
	oldRun, oldPath, oldTestparm := sambaRunCommand, sambaConfigFilePath, sambaTestparmPath
	t.Cleanup(func() { sambaRunCommand, sambaConfigFilePath, sambaTestparmPath = oldRun, oldPath, oldTestparm })
	sambaConfigFilePath = filepath.Join(t.TempDir(), "smb4.conf")
	sambaTestparmPath = path
	sambaRunCommand = func(command string, args ...string) (string, error) {
		if command == "/usr/local/bin/net" && reflect.DeepEqual(args, []string{"--version"}) {
			return string(version), nil
		}
		if command != path || len(args) != 2 || args[0] != "-s" || filepath.Dir(args[1]) != filepath.Dir(sambaConfigFilePath) {
			t.Fatalf("unsafe command in hostname parse check: %s %v", command, args)
		}
		output, err := exec.Command(command, args...).CombinedOutput()
		return string(output), err
	}
	extra := "dns hostname = files.example.com\nnetbios aliases = OLD\nnetbios aliases = FILES \\\n ARCHIVE\nadditional dns hostnames = files2.example.com,other.example.com\n"
	s := &Service{directoryOverride: &ad}
	text, err := s.directoryConfig(extra)
	if err != nil {
		t.Fatal(err)
	}
	for _, principal := range []string{"SYLVE$@EXAMPLE.COM", "host/SYLVE@EXAMPLE.COM", "cifs/sylve.example.com@EXAMPLE.COM", "host/FILES@EXAMPLE.COM", "cifs/archive.example.com@EXAMPLE.COM", "host/files.example.com@EXAMPLE.COM", "cifs/files2.example.com@EXAMPLE.COM", "host/other.example.com@EXAMPLE.COM"} {
		if !strings.Contains(text, principal) {
			t.Fatalf("native config lost %s:\n%s", principal, text)
		}
	}
	if strings.Contains(text, "host/OLD@") {
		t.Fatal("SPNs ignored Samba's effective alias setting")
	}
	if err := validateSambaConfig("[global]\n" + extra + "\n" + text); err != nil {
		t.Fatal(err)
	}
}

func TestJoinValidationRejectsExistingMappingOverrides(t *testing.T) {
	s, _ := newSambaServiceWithMockRunner(t)
	setExtraGlobalConfigValue(t, s, "idmap config EXAMPLE : base_rid = 100")
	ad := directoryModels.ActiveDirectory{Enabled: true, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999}
	if err := s.ValidateDirectoryConfig(context.Background(), ad); !errors.Is(err, directoryservices.ErrInvalid) {
		t.Fatalf("join accepted conflicting standalone mapping: %v", err)
	}
}

func TestStandaloneMappingGlobalsRemainAvailableBeforeDirectoryUse(t *testing.T) {
	s, _ := newSambaServiceWithMockRunner(t)
	if err := s.DB.AutoMigrate(&directoryModels.ActiveDirectory{}); err != nil {
		t.Fatal(err)
	}
	s.Directory = directoryservices.NewService(s.DB)
	extra := "idmap config EXAMPLE : base_rid = 100\ninclude = /etc/smb-extra.conf"
	setExtraGlobalConfigValue(t, s, extra)
	if text, err := s.GlobalConfig(); err != nil || !strings.Contains(text, extra) {
		t.Fatalf("unconfigured Directory Services restricted standalone globals: %v", err)
	}
}

func TestMappingGlobalRejectionRollsBackSettings(t *testing.T) {
	stubGlobalConfigDependencies(t)
	s, _ := newSambaServiceWithMockRunner(t)
	if err := s.DB.AutoMigrate(&directoryModels.ActiveDirectory{}, &networkModels.StandardSwitch{}); err != nil {
		t.Fatal(err)
	}
	s.Directory = directoryservices.NewService(s.DB)
	if err := s.DB.Save(&directoryModels.ActiveDirectory{ID: 1, Joined: true}).Error; err != nil {
		t.Fatal(err)
	}
	before, err := s.GetGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	sambaWriteConfig = func(candidate *Service, _ context.Context, _ bool) error {
		_, err := candidate.GlobalConfig()
		return err
	}
	extra := "winbind separator = +"
	if err := s.SetGlobalConfig(context.Background(), "UTF-8", "WORKGROUP", "Changed", "lo0", false, false, false, &extra); !errors.Is(err, ErrInvalidGlobalConfig) {
		t.Fatalf("changed mapping through transactional global update: %v", err)
	}
	after, err := s.GetGlobalConfig()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed global update changed settings: %+v %v", after, err)
	}
}

func TestOmittedDirectoryArraysPreserveSavedAccess(t *testing.T) {
	s, _ := newSambaServiceWithMockRunner(t)
	if err := s.DB.AutoMigrate(&directoryModels.ActiveDirectory{}); err != nil {
		t.Fatal(err)
	}
	s.Directory = directoryservices.NewService(s.DB)
	previous := directoryTestPermissions()
	for _, input := range []*directoryservices.SharePermissionInput{nil, {}, {ReadSIDs: []string{previous.Read[0].SID}}} {
		result, err := s.resolveDirectoryPermissions(context.Background(), previous, input)
		if err != nil || !reflect.DeepEqual(result, previous) {
			t.Fatalf("omitted arrays lost permissions: %+v %v", result, err)
		}
	}
	result, err := s.resolveDirectoryPermissions(context.Background(), previous, &directoryservices.SharePermissionInput{ReadSIDs: []string{}, WriteSIDs: []string{}})
	if err != nil || len(result.Read)+len(result.Write) != 0 {
		t.Fatalf("explicit arrays did not clear permissions: %+v %v", result, err)
	}
}

func TestDomainOnlyShareUpdateAndGuestConversionDuringOutage(t *testing.T) {
	s, runner := newSambaServiceWithMockRunner(t)
	if err := s.DB.AutoMigrate(&directoryModels.ActiveDirectory{}); err != nil {
		t.Fatal(err)
	}
	s.Directory = directoryservices.NewService(s.DB)
	stubExtraShareCommands(t, func(string) error { return nil })
	share := sambaModels.SambaShare{Name: "domain-only", Dataset: "guid", Enabled: true, DirectoryPermissions: directoryTestPermissions(), CreateMask: "0664", DirectoryMask: "2775"}
	if err := s.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	addDatasetLookupMocks(t, runner, []mockDataset{{Name: "tank/share", GUID: "guid", Mountpoint: "/mnt/share"}})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/share", "", "", nil)
	if err := s.UpdateShare(context.Background(), uint(share.ID), share.Name, share.Dataset, nil, nil, nil, nil, false, false, "0664", "2775", false, 0, false, 70, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var saved sambaModels.SambaShare
	if err := s.DB.First(&saved, share.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.DirectoryPermissions, share.DirectoryPermissions) {
		t.Fatal("old client erased saved domain permissions")
	}
	if inUse, err := s.DirectoryPrincipalsInUse(); err != nil || !inUse {
		t.Fatalf("mapping guard ignored saved share: %v %v", inUse, err)
	}
	if err := s.UpdateShare(context.Background(), uint(share.ID), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, nil, nil, nil); err == nil || err.Error() != "guest_only_share_cannot_have_principals" {
		t.Fatalf("guest mode bypassed domain permissions: %v", err)
	}
	if err := s.UpdateShare(context.Background(), uint(share.ID), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, nil, nil, &directoryservices.SharePermissionInput{ReadSIDs: []string{}, WriteSIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if inUse, err := s.DirectoryPrincipalsInUse(); err != nil || inUse {
		t.Fatalf("cleared share still locks mappings: %v %v", inUse, err)
	}
}

func TestMembershipLockRejectsConcurrentShareMutations(t *testing.T) {
	s := &Service{Directory: directoryservices.NewService(nil)}
	unlock, err := s.Directory.LockConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := s.SetShareEnabled(context.Background(), 1, false); !errors.Is(err, directoryservices.ErrBusy) {
		t.Fatalf("share mutation raced membership: %v", err)
	}
}

func TestDeletedDatasetCleanupWaitsForMembershipAndPreservesIdentities(t *testing.T) {
	s, _ := newSambaServiceWithMockRunner(t)
	if err := s.DB.AutoMigrate(&models.BasicSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatal(err)
	}
	s.Directory = directoryservices.NewService(s.DB)
	share := sambaModels.SambaShare{Name: "deleted", Dataset: "guid", Enabled: true, DirectoryPermissions: directoryTestPermissions()}
	if err := s.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	oldWrite := sambaWriteConfig
	t.Cleanup(func() { sambaWriteConfig = oldWrite })
	sambaWriteConfig = func(*Service, context.Context, bool) error { return nil }
	unlock, err := s.Directory.LockConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- s.DisableSharesForDatasets(context.Background(), []string{share.Dataset})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("dataset cleanup raced membership or was dropped: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unlock()
	unlock = nil
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dataset cleanup did not finish after membership unlocked")
	}
	var saved sambaModels.SambaShare
	if err := s.DB.First(&saved, share.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Enabled || !reflect.DeepEqual(saved.DirectoryPermissions, share.DirectoryPermissions) {
		t.Fatalf("cleanup discarded saved identities: %+v", saved)
	}
}

// This is a read-only compatibility check. It only invokes testparm on a
// temporary file; it does not enroll a host, touch ACLs, or start Samba.
func TestMemberConfigWithInstalledTestparm(t *testing.T) {
	if testing.Short() {
		t.Skip("installed Samba tooling check")
	}
	path, err := exec.LookPath("testparm")
	if err != nil {
		t.Skip("testparm is not installed")
	}
	version, err := exec.Command(filepath.Join(filepath.Dir(path), "net"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("cannot determine installed Samba version: %v\n%s", err, version)
	}
	for _, backend := range []string{"rid", "ad", "autorid"} {
		t.Run(backend, func(t *testing.T) {
			ad := directoryModels.ActiveDirectory{Enabled: true, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", Site: "Default-First-Site-Name", IDMapBackend: backend, IDMapSchema: "rfc2307", RangeLow: 1000000, RangeHigh: 1999999999}
			for _, v := range []string{"Version 4.16.11", string(version)} {
				member, err := directoryservices.SambaConfig(ad, v)
				if err != nil {
					t.Fatal(err)
				}
				text := globalConfig(sambaModels.SambaSettings{UnixCharset: "UTF-8", Workgroup: "WORKGROUP", ServerString: "Sylve", Interfaces: "lo0"}, false, member)
				text += "\n[domain-only]\npath = /\nguest ok = no\nvalid users = " + sambaPrincipalList([]string{"SYLVE\\alice", "@Unix Groups\\staff", "S-1-5-21-111-222-333-1100", "S-1-5-21-111-222-333-2100"}) + "\n"
				file := filepath.Join(t.TempDir(), "smb4.conf")
				if err := os.WriteFile(file, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				output, err := exec.Command(path, "-s", file).CombinedOutput()
				if err != nil || strings.Contains(strings.ToLower(string(output)), "unknown parameter") || strings.Contains(strings.ToLower(string(output)), "ignoring unknown") {
					t.Fatalf("Samba rejected %s config for %s: %v\n%s", backend, v, err, output)
				}
			}
		})
	}
}
