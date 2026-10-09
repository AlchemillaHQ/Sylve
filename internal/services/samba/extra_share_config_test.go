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
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
)

func stubExtraShareCommands(t *testing.T, validate func(string) error) *[]string {
	t.Helper()
	originalPath := sambaConfigFilePath
	originalRun := sambaRunCommand
	originalWrite := sambaWriteConfig
	t.Cleanup(func() {
		sambaConfigFilePath = originalPath
		sambaRunCommand = originalRun
		sambaWriteConfig = originalWrite
	})
	sambaConfigFilePath = filepath.Join(t.TempDir(), "smb4.conf")
	commands := []string{}
	sambaRunCommand = func(command string, args ...string) (string, error) {
		commands = append(commands, command)
		if command == sambaTestparmPath {
			if len(args) != 2 || args[0] != "-s" {
				t.Fatalf("unexpected testparm args: %v", args)
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				t.Fatal(err)
			}
			return "", validate(string(data))
		}
		if command != "/bin/setfacl" {
			t.Fatalf("unexpected command %q", command)
		}
		return "", nil
	}
	sambaWriteConfig = func(*Service, context.Context, bool) error { return nil }
	return &commands
}

func TestValidateExtraShareConfig(t *testing.T) {
	for _, test := range []struct {
		name, config string
		invalid      bool
	}{
		{"empty", "", false},
		{"AD groups", "valid users = \"@EXAMPLE\\Share Users\"\n", false},
		{"comments", "# [not a section]\n; [comment]\nsmb encrypt = required", false},
		{"global section", "smb encrypt = required\n[global]\nsecurity = user", true},
		{"other share", "  [other-share]\r\npath = /", true},
		{"null byte", "valid users = alice\x00bob", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateExtraShareConfig(test.config)
			if errors.Is(err, ErrInvalidShareConfig) != test.invalid {
				t.Fatalf("invalid=%v, got %v", test.invalid, err)
			}
		})
	}
}

func TestShareConfigAppendsExtraConfigVerbatimWithinShare(t *testing.T) {
	for _, test := range []struct {
		name  string
		guest bool
	}{
		{"authenticated", false},
		{"guest", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, runner := newSambaServiceWithMockRunner(t)
			stubExtraShareCommands(t, func(string) error { return nil })
			raw := "  valid users = \"@EXAMPLE\\Share Users\"\n\nread only = no"
			shares := []sambaModels.SambaShare{
				{Name: "first", Dataset: "guid-first", GuestOk: test.guest, ReadOnly: true, CreateMask: "0664", DirectoryMask: "2775", ExtraShareConfig: raw},
				{Name: "second", Dataset: "guid-second", GuestOk: true, ReadOnly: true, CreateMask: "0664", DirectoryMask: "2775"},
			}
			if err := svc.DB.Create(&shares).Error; err != nil {
				t.Fatal(err)
			}
			addDatasetLookupMocks(t, runner, []mockDataset{
				{Name: "tank/first", GUID: "guid-first", Mountpoint: "/mnt/first"},
				{Name: "tank/second", GUID: "guid-second", Mountpoint: "/mnt/second"},
			})
			config, err := svc.ShareConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			sections := strings.Split(config, "[second]\n")
			if len(sections) != 2 || !strings.HasSuffix(sections[0], raw+"\n\n") {
				t.Fatalf("extra configuration was not appended verbatim with section separation:\n%s", config)
			}
			if strings.Contains(sections[1], "@EXAMPLE") {
				t.Fatalf("extra configuration leaked into another share:\n%s", config)
			}
			if strings.Index(config, raw) < strings.Index(config, "vfs objects = zfsacl") {
				t.Fatal("extra configuration must follow managed directives")
			}
		})
	}
}

func TestCreateSharePersistsExtraConfigAndKeepsManagedACLs(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	raw := "smb encrypt = required\n"
	var settings sambaModels.SambaSettings
	if err := svc.DB.First(&settings).Error; err != nil {
		t.Fatal(err)
	}
	settings.ExtraGlobalConfig = "server min protocol = SMB3"
	if err := svc.DB.Save(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&sambaModels.SambaShare{
		Name: "existing", Dataset: "guid-existing", GuestOk: true,
		CreateMask: "0664", DirectoryMask: "2775",
	}).Error; err != nil {
		t.Fatal(err)
	}
	commands := stubExtraShareCommands(t, func(config string) error {
		if !strings.Contains(config, "[documents]\n") || !strings.Contains(config, raw) || !strings.Contains(config, "valid users = @staff") {
			t.Fatalf("candidate configuration is incomplete:\n%s", config)
		}
		if !strings.Contains(config, settings.ExtraGlobalConfig) || !strings.Contains(config, "[existing]\n") {
			t.Fatalf("candidate validation omitted other configuration:\n%s", config)
		}
		return nil
	})
	group := models.Group{Name: "staff"}
	if err := svc.DB.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/documents", GUID: "guid-documents", Mountpoint: "/mnt/documents"},
		{Name: "tank/existing", GUID: "guid-existing", Mountpoint: "/mnt/existing"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/documents", "", "", nil)
	if err := svc.CreateShare(context.Background(), "documents", "guid-documents", nil, nil, []uint{group.ID}, nil, false, false, "0664", "2775", false, 0, false, 70, nil, true, raw); err != nil {
		t.Fatal(err)
	}
	var share sambaModels.SambaShare
	if err := svc.DB.Where("name = ?", "documents").First(&share).Error; err != nil {
		t.Fatal(err)
	}
	if share.ExtraShareConfig != raw {
		t.Fatalf("stored extra config=%q want %q", share.ExtraShareConfig, raw)
	}
	if len(*commands) < 2 || (*commands)[0] != sambaTestparmPath || (*commands)[1] != "/bin/setfacl" {
		t.Fatalf("expected validation before managed ACL updates, got %v", *commands)
	}
}

func TestRejectedExtraConfigDoesNotPersistOrChangeACLs(t *testing.T) {
	for _, operation := range []string{"create", "update", "enable", "disabled missing dataset"} {
		t.Run(operation, func(t *testing.T) {
			svc, runner := newSambaServiceWithMockRunner(t)
			commands := stubExtraShareCommands(t, func(string) error { return errors.New("invalid parameter") })
			raw := "smb encrypt = invalid"
			share := sambaModels.SambaShare{
				Name: "documents", Dataset: "guid-documents", Enabled: true, GuestOk: true, ReadOnly: true,
				CreateMask: "0664", DirectoryMask: "2775", ExtraShareConfig: "smb encrypt = required",
			}
			if operation != "create" {
				if err := svc.DB.Create(&share).Error; err != nil {
					t.Fatal(err)
				}
				if operation == "enable" || operation == "disabled missing dataset" {
					share.Enabled = false
					if err := svc.DB.Model(&share).Update("enabled", false).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			var datasets []mockDataset
			if operation != "disabled missing dataset" {
				datasets = []mockDataset{{Name: "tank/documents", GUID: "guid-documents", Mountpoint: "/mnt/documents"}}
			}
			addDatasetLookupMocks(t, runner, datasets)
			runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/documents", "", "", errors.New("ACL properties must not be changed"))
			var err error
			switch operation {
			case "create":
				err = svc.CreateShare(context.Background(), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, false, raw)
			case "enable":
				err = svc.SetShareEnabled(context.Background(), uint(share.ID), true)
			default:
				err = svc.UpdateShare(context.Background(), uint(share.ID), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, &share.Enabled, &raw)
			}
			if !errors.Is(err, ErrInvalidShareConfig) {
				t.Fatalf("expected testparm rejection, got %v", err)
			}
			if len(*commands) != 1 || (*commands)[0] != sambaTestparmPath {
				t.Fatalf("unexpected mutations during failed validation: %v", *commands)
			}
			if operation == "create" {
				var count int64
				if err := svc.DB.Model(&sambaModels.SambaShare{}).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("invalid share was persisted: %d", count)
				}
			} else {
				var stored sambaModels.SambaShare
				if err := svc.DB.First(&stored, share.ID).Error; err != nil {
					t.Fatal(err)
				}
				if stored.ExtraShareConfig != share.ExtraShareConfig || stored.Enabled != share.Enabled {
					t.Fatalf("invalid edit changed the stored share: %+v", stored)
				}
			}
		})
	}
}

func TestUpdateSharePreservesAndClearsExtraConfig(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	raw := "smb encrypt = required"
	stubExtraShareCommands(t, func(string) error { return nil })
	share := sambaModels.SambaShare{
		Name: "documents", Dataset: "guid-documents", GuestOk: true, ReadOnly: true,
		CreateMask: "0664", DirectoryMask: "2775", ExtraShareConfig: raw,
	}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	addDatasetLookupMocks(t, runner, []mockDataset{{Name: "tank/documents", GUID: "guid-documents", Mountpoint: "/mnt/documents"}})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/documents", "", "", nil)
	for _, extra := range []*string{nil, new(string)} {
		if err := svc.UpdateShare(context.Background(), uint(share.ID), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, nil, extra); err != nil {
			t.Fatal(err)
		}
		var stored sambaModels.SambaShare
		if err := svc.DB.First(&stored, share.ID).Error; err != nil {
			t.Fatal(err)
		}
		want := raw
		if extra != nil {
			want = ""
		}
		if stored.ExtraShareConfig != want {
			t.Fatalf("extra config=%q want %q", stored.ExtraShareConfig, want)
		}
	}
}

func TestExtraShareConfigDoesNotBypassManagedAccessValidation(t *testing.T) {
	svc, _ := newSambaServiceWithMockRunner(t)
	err := svc.CreateShare(context.Background(), "documents", "guid-documents", nil, nil, nil, nil,
		false, false, "0664", "2775", false, 0, false, 70, nil, true, `valid users = "@EXAMPLE\Share Users"`)
	if err == nil || err.Error() != "no_principals_selected_and_guests_not_allowed" {
		t.Fatalf("extra config bypassed managed principal requirement: %v", err)
	}
}

func TestWriteConfigIncludesValidatedExtraShareConfig(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	raw := "smb encrypt = required"
	var validated string
	stubExtraShareCommands(t, func(config string) error {
		validated = config
		return nil
	})
	if err := svc.DB.Create(&sambaModels.SambaShare{
		Name: "documents", Dataset: "guid-documents", GuestOk: true, ReadOnly: true,
		CreateMask: "0664", DirectoryMask: "2775", ExtraShareConfig: raw,
	}).Error; err != nil {
		t.Fatal(err)
	}
	addDatasetLookupMocks(t, runner, []mockDataset{{Name: "tank/documents", GUID: "guid-documents", Mountpoint: "/mnt/documents"}})
	originalWrite := sambaAtomicWriteFile
	t.Cleanup(func() { sambaAtomicWriteFile = originalWrite })
	var written string
	sambaAtomicWriteFile = func(path string, data []byte, perm os.FileMode) error {
		written = string(data)
		return nil
	}
	if err := svc.WriteConfig(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if written != validated || !strings.Contains(written, raw) {
		t.Fatal("active config differs from validated config or omits the share override")
	}
}

func TestExtraShareConfigOverridesManagedDirectiveWithTestparm(t *testing.T) {
	path, err := exec.LookPath("testparm")
	if err != nil {
		t.Skip("testparm is not installed")
	}
	svc, _ := newSambaServiceWithMockRunner(t)
	share := sambaModels.SambaShare{
		Name: "documents", Dataset: "guid-documents", GuestOk: true, ReadOnly: true,
		CreateMask: "0664", DirectoryMask: "2775", ExtraShareConfig: "read only = no\nsmb encrypt = required",
	}
	config, err := svc.shareConfig(context.Background(), []sambaModels.SambaShare{share}, false,
		map[string]*gzfs.Dataset{share.Dataset: {Mountpoint: "/reference-only"}})
	if err != nil {
		t.Fatal(err)
	}
	global, err := svc.GlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "smb4.conf")
	if err := os.WriteFile(configPath, []byte(global+"\n"+config), 0600); err != nil {
		t.Fatal(err)
	}
	for parameter, want := range map[string]string{"read only": "No", "smb encrypt": "required"} {
		output, err := exec.Command(path, "-s", "--section-name=documents", "--parameter-name="+parameter, configPath).Output()
		if err != nil {
			t.Fatalf("testparm rejected %s: %v", parameter, err)
		}
		if !strings.EqualFold(strings.TrimSpace(string(output)), want) {
			t.Fatalf("%s=%q want %q", parameter, output, want)
		}
	}
}
