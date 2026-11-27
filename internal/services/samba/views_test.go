// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/sharerules"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func sharesWithDataset(t *testing.T, svc *Service, guid string) int {
	t.Helper()
	share := sambaModels.SambaShare{Name: "share-" + guid, Dataset: guid, Enabled: true, GuestOk: true, CreateMask: "0664", DirectoryMask: "2775"}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	return share.ID
}

func TestParseSmbstatusReadsSessionsAndConnections(t *testing.T) {
	status, err := parseSmbstatus(`{
		"version": "4.23.13",
		"sessions": {
			"3184588419": {"session_id": "3184588419", "server_id": {"pid": "8123"}, "username": "alice", "groupname": "staff",
				"remote_machine": "192.0.2.10", "hostname": "ipv4:192.0.2.10:51234", "session_dialect": "SMB3_11",
				"encryption": {"cipher": "AES-128-GCM", "degree": "partial"}, "signing": {"cipher": "AES-128-GMAC", "degree": "partial"}}
		},
		"tcons": {
			"1": {"service": "documents", "session_id": 3184588419, "server_id": {"pid": 8123}, "machine": "192.0.2.10",
				"connected_at": "2026-11-25T10:00:00+05:30", "encryption": {"cipher": "-", "degree": "none"}, "signing": {"cipher": "-", "degree": "none"}}
		},
		"open_files": {"/mnt/documents/a.txt": {}, "/mnt/documents/b.txt": {}}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if status.Version != "4.23.13" || len(status.Sessions) != 1 || len(status.Connections) != 1 || status.OpenFiles != 2 {
		t.Fatalf("status = %+v", status)
	}
	session, connection := status.Sessions[0], status.Connections[0]
	if session.Username != "alice" || session.PID != "8123" || session.Dialect != "SMB3_11" || session.Encryption != "AES-128-GCM partial" {
		t.Fatalf("session = %+v", session)
	}
	if connection.Share != "documents" || connection.SessionID != "3184588419" || connection.PID != "8123" || connection.Encryption != "none" {
		t.Fatalf("connection = %+v", connection)
	}
	if _, err := parseSmbstatus("not json"); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestStatusReportsAStoppedServiceWithItsVersion(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatal(err)
	}
	originalRunning, originalRun := sambaServiceRunning, sambaRunCommand
	t.Cleanup(func() { sambaServiceRunning, sambaRunCommand = originalRunning, originalRun })
	sambaServiceRunning = func() bool { return false }
	sambaRunCommand = func(command string, args ...string) (string, error) {
		if command != sambaSmbdPath {
			t.Errorf("a stopped service ran %s", command)
		}
		return "Version 4.23.13\n", nil
	}
	status, err := (&Service{DB: db}).Status()
	if err != nil || !status.ServiceEnabled || status.Running || status.Version != "4.23.13" || status.Sessions == nil {
		t.Fatalf("status = %+v err=%v", status, err)
	}
}

func TestPrincipalsReportSambaAccounts(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Group{})
	for _, user := range []models.User{{Username: "alice", Source: "pam"}, {Username: "bob", Source: "pam"}, {Username: "admin", Source: "local"}} {
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.Group{Name: "staff"}).Error; err != nil {
		t.Fatal(err)
	}
	original := sambaListUsers
	t.Cleanup(func() { sambaListUsers = original })
	sambaListUsers = func() ([]string, error) { return []string{"alice", "admin"}, nil }

	principals, err := (&Service{DB: db}).Principals()
	if err != nil {
		t.Fatal(err)
	}
	if len(principals.Users) != 2 || len(principals.Groups) != 1 || principals.Groups[0].Name != "staff" {
		t.Fatalf("principals = %+v; want the two PAM users and the group", principals)
	}
	accounts := map[string]bool{}
	for _, user := range principals.Users {
		if user.SambaAccount == nil {
			t.Fatalf("%s has no account flag", user.Username)
		}
		accounts[user.Username] = *user.SambaAccount
	}
	if !accounts["alice"] || accounts["bob"] {
		t.Fatalf("accounts = %v; want alice only", accounts)
	}

	sambaListUsers = func() ([]string, error) { return nil, errors.New("pdbedit missing") }
	principals, err = (&Service{DB: db}).Principals()
	if err != nil || principals.Users[0].SambaAccount != nil {
		t.Fatalf("an unreadable account list returned %+v %v; want unknown flags", principals.Users, err)
	}
}

func TestShareDatasetsAndStatesExplainEachShare(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank", GUID: "guid-tank", Mountpoint: "/tank"},
		{Name: "tank/sylve", GUID: "guid-sylve", Mountpoint: "/tank/sylve"},
		{Name: "tank/data", GUID: "guid-data", Mountpoint: "/tank/data"},
		{Name: "tank/offline", GUID: "guid-offline", Mountpoint: "/tank/offline", Mounted: "no"},
	})
	active := sharesWithDataset(t, svc, "guid-data")
	unmounted := sharesWithDataset(t, svc, "guid-offline")
	missing := sharesWithDataset(t, svc, "guid-gone")
	disabled := sharesWithDataset(t, svc, "guid-tank")
	if err := svc.DB.Model(&sambaModels.SambaShare{}).Where("id = ?", disabled).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	datasets, err := svc.ShareDatasets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ShareDataset{}
	for _, dataset := range datasets {
		byName[dataset.Name] = dataset
	}
	if byName["tank"].Block != sharerules.BlockContainsProtected || byName["tank/sylve"].Block != sharerules.BlockSylveData || byName["tank/data"].Block != "" {
		t.Fatalf("blocks = %+v", byName)
	}
	if byName["tank/data"].SharedBy == nil || byName["tank/data"].SharedBy.ID != active || !byName["tank/data"].Mounted || byName["tank/offline"].Mounted {
		t.Fatalf("tank/data = %+v, tank/offline = %+v", byName["tank/data"], byName["tank/offline"])
	}

	shares, err := svc.GetShares()
	if err != nil {
		t.Fatal(err)
	}
	states, err := svc.ShareStates(context.Background(), shares)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]string{active: sharerules.StateActive, unmounted: sharerules.StateDatasetUnmounted, missing: sharerules.StateDatasetMissing, disabled: sharerules.StateDisabled} {
		if states[id].State != want {
			t.Errorf("share %d state = %q; want %q", id, states[id].State, want)
		}
	}
	if states[active].DatasetName != "tank/data" || states[active].Mountpoint != "/tank/data" || states[disabled].Block != sharerules.BlockContainsProtected {
		t.Fatalf("states = %+v", states)
	}
}
