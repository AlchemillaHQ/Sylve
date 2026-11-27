// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestValidateSambaShareName(t *testing.T) {
	for _, name := range []string{"Docs", "Team Files", "media-2026", "Fotos Ünïcode", strings.Repeat("a", MaxSambaShareNameLength)} {
		if err := validateSambaShareName(name); err != nil {
			t.Errorf("%q rejected: %v", name, err)
		}
	}
	for name, want := range map[string]string{
		"":            "invalid_share_name",
		" leading":    "invalid_share_name",
		"trailing ":   "invalid_share_name",
		"a|b":         "invalid_share_name",
		"a,b":         "invalid_share_name",
		"a/b":         "invalid_share_name",
		"a:b":         "invalid_share_name",
		"tab\tname":   "invalid_share_name",
		"[global]":    "invalid_share_name",
		"line\nbreak": "invalid_share_name",
		strings.Repeat("a", MaxSambaShareNameLength+1): "share_name_too_long",
		"GLOBAL":   "share_name_reserved",
		"Homes":    "share_name_reserved",
		"printers": "share_name_reserved",
		"IPC$":     "share_name_reserved",
		"print$":   "share_name_reserved",
	} {
		if err := validateSambaShareName(name); err == nil || err.Error() != want {
			t.Errorf("%q returned %v; want %s", name, err, want)
		}
	}
}

func TestShareDatasetBlockRefusesSystemAndSylveData(t *testing.T) {
	datasets := []*gzfs.Dataset{
		{Name: "zroot/ROOT/default", Mountpoint: "/"},
		{Name: "zroot/sylve", Mountpoint: "/zroot/sylve"},
		{Name: "zroot/sylve/virtual-machines", Mountpoint: "none"},
		{Name: "tank/data", Mountpoint: "/tank/data"},
	}
	roots := protectedShareRoots(datasets, "/srv/apps/sylve-data")
	for _, root := range []string{"/", "/zroot/sylve", "/srv/apps/sylve-data"} {
		if !slices.Contains(roots, root) {
			t.Fatalf("protected roots %v are missing %s", roots, root)
		}
	}

	for _, tc := range []struct {
		name, mountpoint, want string
	}{
		{"zroot/ROOT/default", "/", ShareDatasetBootEnvironment},
		{"tank/rootfs", "/", ShareDatasetRoot},
		{"zroot/var/log", "/var/log", ShareDatasetSystemPath},
		{"zroot/usr/src", "/usr/src", ShareDatasetSystemPath},
		{"zroot/tmp", "/tmp", ShareDatasetSystemPath},
		{"zroot/sylve/jails", "/zroot/sylve/jails", ShareDatasetSylveData},
		{"zroot", "/zroot", ShareDatasetContainsProtected},
		{"tank/apps", "/srv/apps", ShareDatasetContainsProtected},
		{"zroot/usr/home", "/usr/home", ""},
		{"zroot/home", "/home", ""},
		{"tank/data", "/tank/data", ""},
		{"tank", "/tank", ""},
		{"tank/legacy", "legacy", ""},
	} {
		got := shareDatasetBlock(&gzfs.Dataset{Name: tc.name, Mountpoint: tc.mountpoint}, roots)
		if got != tc.want {
			t.Errorf("%s at %s: got %q, want %q", tc.name, tc.mountpoint, got, tc.want)
		}
	}
}

func TestCreateShareRefusesDatasetsContainingSylveData(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank", GUID: "guid-tank", Mountpoint: "/tank"},
		{Name: "tank/sylve", GUID: "guid-sylve", Mountpoint: "/tank/sylve"},
	})
	commands := stubExtraShareCommands(t, func(string) error { return nil })

	_, err := svc.CreateShare(context.Background(), "everything", "guid-tank", nil, nil, nil, nil, true, true, "0664", "2775", false, 0, false, 70, nil, true, "", nil)
	var blocked *DatasetNotShareableError
	if !errors.As(err, &blocked) || blocked.Reason != ShareDatasetContainsProtected {
		t.Fatalf("CreateShare returned %v; want dataset_not_shareable: %s", err, ShareDatasetContainsProtected)
	}
	if len(*commands) != 0 {
		t.Fatalf("a refused share ran commands: %v", *commands)
	}
	for _, call := range runner.CallHistory {
		if strings.Contains(call.Cmd, "zfs set ") {
			t.Fatalf("a refused share changed dataset properties: %s", call.Cmd)
		}
	}
	var count int64
	svc.DB.Model(&sambaModels.SambaShare{}).Count(&count)
	if count != 0 {
		t.Fatalf("a refused share was saved")
	}
}

func TestShareNamesAreUniqueIgnoringCase(t *testing.T) {
	svc := &Service{DB: testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{})}
	if err := svc.DB.Create(&sambaModels.SambaShare{Name: "Docs", Dataset: "guid-docs"}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateShare(context.Background(), "docs", "guid-other", nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, true, "", nil)
	if err == nil || err.Error() != "share_with_name_exists" {
		t.Fatalf("CreateShare returned %v; want share_with_name_exists", err)
	}
	_, err = svc.CreateShare(context.Background(), "Global", "guid-other", nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, true, "", nil)
	if err == nil || err.Error() != "share_name_reserved" {
		t.Fatalf("CreateShare returned %v; want share_name_reserved", err)
	}
}

func TestApplySambaServiceReloadsOnlyAnEnabledService(t *testing.T) {
	originalRunning, originalCommand, originalReload := sambaServiceRunning, sambaServiceCommand, sambaReloadDaemons
	t.Cleanup(func() {
		sambaServiceRunning, sambaServiceCommand, sambaReloadDaemons = originalRunning, originalCommand, originalReload
	})

	for _, tc := range []struct {
		name         string
		enabled      bool
		running      bool
		reloadFails  bool
		action       sambaServiceAction
		wantReload   bool
		wantCommands []string
	}{
		{"disabled reload", false, true, false, sambaReloadService, false, nil},
		{"disabled restart", false, false, false, sambaRestartService, false, nil},
		{"running reload", true, true, false, sambaReloadService, true, nil},
		{"rejected reload", true, true, true, sambaReloadService, true, []string{"samba_server onereload"}},
		{"stopped reload", true, false, false, sambaReloadService, false, []string{"samba_server onestart"}},
		{"restart", true, true, false, sambaRestartService, false, []string{"samba_server onerestart"}},
		{"keep", true, true, false, sambaKeepService, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
			settings := models.BasicSettings{}
			if tc.enabled {
				settings.Services = []models.AvailableService{models.SambaServer}
			}
			if err := db.Create(&settings).Error; err != nil {
				t.Fatal(err)
			}
			var ran []string
			reloaded := false
			sambaServiceRunning = func() bool { return tc.running }
			sambaReloadDaemons = func() error {
				reloaded = true
				if tc.reloadFails {
					return errors.New("smbd did not accept the message")
				}
				return nil
			}
			sambaServiceCommand = func(name, action string) error {
				ran = append(ran, name+" "+action)
				return nil
			}
			if err := (&Service{DB: db}).applySambaService(tc.action); err != nil {
				t.Fatal(err)
			}
			if reloaded != tc.wantReload || !slices.Equal(ran, tc.wantCommands) {
				t.Fatalf("reloaded=%t ran %v; want reloaded=%t ran %v", reloaded, ran, tc.wantReload, tc.wantCommands)
			}
		})
	}

	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatal(err)
	}
	sambaServiceRunning = func() bool { return true }
	sambaReloadDaemons = func() error { return errors.New("smbd did not accept the message") }
	sambaServiceCommand = func(string, string) error { return errors.New("rc script failed") }
	err := (&Service{DB: db}).applySambaService(sambaReloadService)
	if err == nil || !strings.HasPrefix(err.Error(), "samba_service_reload_failed") {
		t.Fatalf("failed reload returned %v", err)
	}
}

func TestSambaDaemonControlUsesSmbcontrol(t *testing.T) {
	original, originalPath := sambaControlCommand, sambaConfigFilePath
	t.Cleanup(func() { sambaControlCommand, sambaConfigFilePath = original, originalPath })
	sambaConfigFilePath = "/etc/test-smb4.conf"
	type reply struct {
		output string
		err    error
	}
	var calls []string
	replies := map[string]reply{}
	sambaControlCommand = func(command string, args ...string) (string, error) {
		call := command + " " + strings.Join(args, " ")
		calls = append(calls, call)
		reply := replies[strings.Join(args[:2], " ")]
		return reply.output, reply.err
	}

	replies["smbd ping"] = reply{"PONG from pid 30228", nil}
	if !sambaSmbdResponds() || calls[0] != sambaControlPath+" smbd ping --configfile=/etc/test-smb4.conf" {
		t.Fatalf("a running smbd was not detected: %v", calls)
	}
	replies["smbd ping"] = reply{"Can't load /etc/test-smb4.conf - run testparm to debug it", nil}
	if sambaSmbdResponds() {
		t.Fatal("a ping without PONG counted as running")
	}
	replies["smbd ping"] = reply{"Can't find pid for destination 'smbd'", errors.New("exit status 1")}
	if sambaSmbdResponds() {
		t.Fatal("a stopped smbd counted as running")
	}

	calls = nil
	if err := reloadSambaDaemons(); err != nil {
		t.Fatalf("a reload failed: %v", err)
	}
	if len(calls) != 1 || calls[0] != sambaControlPath+" all reload-config --configfile=/etc/test-smb4.conf" {
		t.Fatalf("reload calls = %v; want one message to every Samba process", calls)
	}
	replies["all reload-config"] = reply{"", errors.New("messaging failed")}
	if err := reloadSambaDaemons(); err == nil {
		t.Fatal("a failed reload message succeeded")
	}
}

func TestShareChangesReloadAndReportFailuresAfterSaving(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	addDatasetLookupMocks(t, runner, []mockDataset{{Name: "tank/public", GUID: "guid-public", Mountpoint: "/mnt/public"}})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/public", "", "", nil)
	commands := stubExtraShareCommands(t, func(string) error { return nil })
	original := sambaWriteConfig
	t.Cleanup(func() { sambaWriteConfig = original })
	var actions []sambaServiceAction
	sambaWriteConfig = func(_ *Service, _ context.Context, action sambaServiceAction) error {
		actions = append(actions, action)
		return errors.New("reload failed")
	}

	id, err := svc.CreateShare(context.Background(), "public", "guid-public", nil, nil, nil, nil, true, true, "0664", "2775", false, 0, false, 70, nil, true, "", nil)
	if !errors.Is(err, ErrSambaApplyFailed) || id == 0 {
		t.Fatalf("CreateShare returned id=%d err=%v; want a saved share and %v", id, err, ErrSambaApplyFailed)
	}
	if _, err := svc.GetShare(id); err != nil {
		t.Fatalf("the share was not kept after the apply failure: %v", err)
	}

	*commands = (*commands)[:0]
	if err := svc.SetShareEnabled(context.Background(), id, false); !errors.Is(err, ErrSambaApplyFailed) {
		t.Fatalf("SetShareEnabled returned %v; want %v", err, ErrSambaApplyFailed)
	}
	if !slices.Contains(*commands, "/bin/setfacl") {
		t.Fatalf("disabling did not remove the share's ACL entries: %v", *commands)
	}
	if err := svc.DeleteShare(context.Background(), id); !errors.Is(err, ErrSambaApplyFailed) {
		t.Fatalf("DeleteShare returned %v; want %v", err, ErrSambaApplyFailed)
	}
	for _, action := range actions {
		if action != sambaReloadService {
			t.Fatalf("a share change used action %d; shares must reload, not restart", action)
		}
	}
	if len(actions) != 3 {
		t.Fatalf("apply ran %d times; want 3", len(actions))
	}
	if _, err := svc.GetShare(id); err == nil || err.Error() != "share_not_found" {
		t.Fatalf("GetShare after delete returned %v; want share_not_found", err)
	}
}

func TestSambaACLPropertiesAreOnlySetWhenTheyDiffer(t *testing.T) {
	property := func(value string) gzfs.ZFSProperty { return gzfs.ZFSProperty{Value: value} }
	matching := &gzfs.Dataset{Name: "tank/docs", Type: gzfs.DatasetTypeFilesystem, Properties: map[string]gzfs.ZFSProperty{
		"acltype":    property(sambaACLType),
		"aclmode":    property(sambaACLMode),
		"aclinherit": property(sambaACLInherit),
	}}
	if changes := sambaACLPropertyChanges(matching); len(changes) != 0 {
		t.Fatalf("matching properties produced changes %v", changes)
	}
	if err := (&Service{}).ensureSambaDatasetACLProperties(context.Background(), matching, true); err != nil {
		t.Fatalf("matching properties were set again: %v", err)
	}

	partial := &gzfs.Dataset{Name: "tank/docs", Type: gzfs.DatasetTypeFilesystem, Properties: map[string]gzfs.ZFSProperty{
		"acltype":    property(sambaACLType),
		"aclmode":    property("discard"),
		"aclinherit": property(sambaACLInherit),
	}}
	if changes := sambaACLPropertyChanges(partial); !slices.Equal(changes, []string{"aclmode", sambaACLMode}) {
		t.Fatalf("a single differing property produced %v", changes)
	}
	if changes := sambaACLPropertyChanges(&gzfs.Dataset{}); len(changes) != 6 {
		t.Fatalf("unknown properties produced %v; want all three set", changes)
	}
}

func TestShareConfigReloadAnnouncesOnlyAfterASuccessfulWrite(t *testing.T) {
	original := sambaWriteConfig
	t.Cleanup(func() { sambaWriteConfig = original })
	var writeErr error
	sambaWriteConfig = func(*Service, context.Context, sambaServiceAction) error { return writeErr }
	announced := 0
	svc := &Service{OnConfigChange: func() error {
		announced++
		return errors.New("responder unavailable")
	}}

	if err := svc.reloadConfig(context.Background()); err != nil {
		t.Fatalf("an mDNS failure failed the share reload: %v", err)
	}
	writeErr = errors.New("testparm rejected the config")
	if err := svc.reloadConfig(context.Background()); err == nil {
		t.Fatal("a failed write succeeded")
	}
	if announced != 1 {
		t.Fatalf("mDNS was rebuilt %d times; want once, after the successful write only", announced)
	}
}
