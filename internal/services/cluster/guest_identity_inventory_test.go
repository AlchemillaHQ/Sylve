// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func inventoryReportJSON(t *testing.T, report GuestIdentityInventoryReport) string {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal inventory report: %v", err)
	}
	return string(raw)
}

func TestBuildGuestIdentityInventoryReportStableAcrossInsertionOrder(t *testing.T) {
	entries := []GuestIdentityInventoryEntry{
		{NodeID: " node-b ", GuestType: " JAIL ", GuestID: 20, RecordID: 4, Name: " jail-20 "},
		{NodeID: "node-a", GuestType: "vm", GuestID: 10, RecordID: 2, Name: "vm-10"},
		{NodeID: "node-c", GuestType: "VM", GuestID: 20, RecordID: 1, Name: "vm-20"},
	}
	reversed := slices.Clone(entries)
	slices.Reverse(reversed)

	first := BuildGuestIdentityInventoryReport(entries)
	second := BuildGuestIdentityInventoryReport(reversed)
	if first.Digest != second.Digest {
		t.Fatalf("digests differ by insertion order: %q != %q", first.Digest, second.Digest)
	}
	if got, want := inventoryReportJSON(t, first), inventoryReportJSON(t, second); got != want {
		t.Fatalf("report JSON differs by insertion order:\n%s\n%s", got, want)
	}
	if entries[0].NodeID != " node-b " || entries[0].GuestType != " JAIL " {
		t.Fatalf("builder mutated caller input: %+v", entries[0])
	}
}

func TestBuildGuestIdentityInventoryDigestIgnoresInformationalMetadata(t *testing.T) {
	first := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
		{NodeID: "node-a", GuestType: "vm", GuestID: 10, RecordID: 1, Name: "before"},
	})
	second := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
		{NodeID: "node-a", GuestType: "vm", GuestID: 10, RecordID: 99, Name: "after"},
	})

	if first.Digest != second.Digest {
		t.Fatalf("identity digest changed with metadata: %q != %q", first.Digest, second.Digest)
	}
	if inventoryReportJSON(t, first) == inventoryReportJSON(t, second) {
		t.Fatal("informational report metadata unexpectedly identical")
	}
}

func TestScanLocalGuestIdentityInventoryReportsVMJailSharedID(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&vmModels.VM{RID: 100, Name: "vm-100"}).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	if err := db.Create(&jailModels.Jail{CTID: 100, Name: "jail-100"}).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}

	report, err := ScanLocalGuestIdentityInventory(db, "node-a")
	if err != nil {
		t.Fatalf("scan inventory: %v", err)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", report.Entries)
	}
	if len(report.Conflicts) != 1 || report.Conflicts[0].Reason != GuestIdentityInventoryConflictSharedGuestID {
		t.Fatalf("conflicts = %+v, want one shared-ID conflict", report.Conflicts)
	}
	if len(report.Conflicts[0].Entries) != 2 ||
		report.Conflicts[0].Entries[0].GuestType != "jail" ||
		report.Conflicts[0].Entries[1].GuestType != "vm" {
		t.Fatalf("shared-ID entries are not canonical: %+v", report.Conflicts[0].Entries)
	}
}

func TestBuildGuestIdentityInventoryReportReportsCrossNodeConflict(t *testing.T) {
	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
		{NodeID: "node-b", GuestType: "vm", GuestID: 101, RecordID: 8, Name: "vm-b"},
		{NodeID: "node-a", GuestType: "vm", GuestID: 101, RecordID: 3, Name: "vm-a"},
	})
	if len(report.Conflicts) != 1 || report.Conflicts[0].Reason != GuestIdentityInventoryConflictSharedGuestID {
		t.Fatalf("conflicts = %+v, want cross-node shared-ID conflict", report.Conflicts)
	}
	if got := report.Conflicts[0].Entries; len(got) != 2 || got[0].NodeID != "node-a" || got[1].NodeID != "node-b" {
		t.Fatalf("cross-node entries are not canonical: %+v", got)
	}
}

func TestScanLocalGuestIdentityInventoryClean(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&vmModels.VM{RID: 11, Name: "vm-11"}).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	if err := db.Create(&jailModels.Jail{CTID: 12, Name: "jail-12"}).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}

	report, err := ScanLocalGuestIdentityInventory(db, " node-a ")
	if err != nil {
		t.Fatalf("scan inventory: %v", err)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", report.Conflicts)
	}
	if len(report.Entries) != 2 || report.Entries[0].GuestID != 11 || report.Entries[1].GuestID != 12 {
		t.Fatalf("unexpected canonical entries: %+v", report.Entries)
	}
	for _, entry := range report.Entries {
		if entry.NodeID != "node-a" || entry.RecordID == 0 {
			t.Fatalf("unexpected scanned entry: %+v", entry)
		}
	}
	if report.Digest == "" {
		t.Fatal("clean report digest is empty")
	}
}

func TestBuildGuestIdentityInventoryReportReportsInvalidFields(t *testing.T) {
	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
		{NodeID: " ", GuestType: "container", GuestID: 0, RecordID: 9, Name: "bad"},
	})
	wantReasons := []string{
		GuestIdentityInventoryConflictInvalidGuestID,
		GuestIdentityInventoryConflictInvalidGuestType,
		GuestIdentityInventoryConflictInvalidNodeID,
	}
	if len(report.Conflicts) != len(wantReasons) {
		t.Fatalf("conflicts = %+v, want reasons %v", report.Conflicts, wantReasons)
	}
	for i, want := range wantReasons {
		if report.Conflicts[i].Reason != want {
			t.Fatalf("conflict[%d] reason = %q, want %q", i, report.Conflicts[i].Reason, want)
		}
		if len(report.Conflicts[i].Entries) != 1 {
			t.Fatalf("conflict[%d] entries = %+v, want one", i, report.Conflicts[i].Entries)
		}
	}
}

func TestBuildGuestIdentityInventoryReportRejectsOutOfRangeIDAndOversizedNode(t *testing.T) {
	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
		{
			NodeID:    string(make([]byte, 129)),
			GuestType: "vm",
			GuestID:   10000,
			RecordID:  1,
			Name:      "bad-range",
		},
	})
	if len(report.Conflicts) != 2 ||
		report.Conflicts[0].Reason != GuestIdentityInventoryConflictInvalidGuestID ||
		report.Conflicts[1].Reason != GuestIdentityInventoryConflictInvalidNodeID {
		t.Fatalf("unexpected out-of-range conflicts: %+v", report.Conflicts)
	}
}

func TestRequireGuestIDAvailableStandaloneUsesSharedLocalNamespace(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := db.Create(&clusterModels.Cluster{Enabled: false}).Error; err != nil {
		t.Fatalf("seed standalone cluster state: %v", err)
	}
	if err := db.Create(&jailModels.Jail{CTID: 700, Name: "jail-700"}).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	service := &Service{DB: db, NodeID: "standalone-node"}
	if err := service.RequireGuestIDAvailable(t.Context(), 701); err != nil {
		t.Fatalf("free standalone guest ID rejected: %v", err)
	}
	if err := service.RequireGuestIDAvailable(t.Context(), 700); err == nil ||
		!strings.Contains(err.Error(), "guest_id_already_in_use") {
		t.Fatalf("occupied standalone guest ID error = %v", err)
	}
	if err := db.Create(&vmModels.VM{RID: 700, Name: "vm-700"}).Error; err != nil {
		t.Fatalf("seed conflicting VM: %v", err)
	}
	if err := service.RequireGuestIDAvailable(t.Context(), 701); err == nil ||
		!strings.Contains(err.Error(), "guest_identity_inventory_conflict") {
		t.Fatalf("dirty standalone inventory error = %v", err)
	}
}

func TestRequireGuestIDAvailableClusteredWithoutRaftFailsClosed(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := db.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatalf("seed clustered state: %v", err)
	}

	service := &Service{DB: db, NodeID: "clustered-node"}
	err := service.RequireGuestIDAvailable(t.Context(), 702)
	if err == nil || !strings.Contains(err.Error(), "guest_identity_inventory_unavailable") {
		t.Fatalf("clustered check without Raft error = %v", err)
	}
}
