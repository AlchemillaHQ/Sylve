// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/system/pciconf"
)

func setPassthroughTestDependencies(
	t *testing.T,
	path string,
	devices []pciconf.PCIDevice,
	runner func(string, ...string) (string, error),
) {
	t.Helper()

	oldPath := loaderConfPath
	oldList := getPCIDevicesOperation
	oldRunner := runPPTCommand
	loaderConfPath = path
	getPCIDevicesOperation = func() ([]pciconf.PCIDevice, error) {
		return append([]pciconf.PCIDevice(nil), devices...), nil
	}
	runPPTCommand = runner

	t.Cleanup(func() {
		loaderConfPath = oldPath
		getPCIDevicesOperation = oldList
		runPPTCommand = oldRunner
	})
}

func TestPassthroughAddressAndDomainValidation(t *testing.T) {
	if _, err := parseDomain("1"); !errors.Is(err, ErrUnsupportedPassthroughDomain) {
		t.Fatalf("domain error = %v; want ErrUnsupportedPassthroughDomain", err)
	}

	for _, id := range []string{"", "1/2", "256/0/0", "0/32/0", "0/0/8"} {
		if _, err := parsePPTAddress(id); !errors.Is(err, ErrInvalidPassthroughDevice) {
			t.Fatalf("address %q error = %v; want ErrInvalidPassthroughDevice", id, err)
		}
	}

	parts, err := parsePPTAddress("255/31/7")
	if err != nil || parts != [3]int{255, 31, 7} {
		t.Fatalf("valid address parsed as %v with error %v", parts, err)
	}
}

func TestRewriteLoaderPPTIDsDeduplicatesAssignments(t *testing.T) {
	lines := []string{
		`vmm_load="YES"`,
		`pptdevs="1/2/3 1/2/3"`,
		`pptdevs="4/5/6"`,
	}

	rewritten := rewriteLoaderPPTIDs(lines, []string{"1/2/3", "1/2/3", "4/5/6"})
	joined := strings.Join(rewritten, "\n")
	if strings.Count(joined, "pptdevs=") != 1 || !strings.Contains(joined, `pptdevs="1/2/3 4/5/6"`) {
		t.Fatalf("unexpected loader.conf rewrite: %q", joined)
	}
}

func TestEnsureVMMLoadForPPTDevices(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name: "does_not_manage_modules_without_ppt_devices",
			lines: []string{
				`vmm_load="NO"`,
				`ppt_load="NO"`,
			},
			want: []string{
				`vmm_load="NO"`,
				`ppt_load="NO"`,
			},
		},
		{
			name: "adds_vmm_before_ppt_devices",
			lines: []string{
				`ppt_load="NO"`,
				`pptdevs="1/2/3"`,
			},
			want: []string{
				`ppt_load="NO"`,
				`vmm_load="YES"`,
				`pptdevs="1/2/3"`,
			},
		},
		{
			name: "enables_existing_vmm_and_preserves_ppt_load",
			lines: []string{
				`vmm_load="NO"`,
				`ppt_load="NO"`,
				`pptdevs="1/2/3"`,
			},
			want: []string{
				`vmm_load="YES"`,
				`ppt_load="NO"`,
				`pptdevs="1/2/3"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureVMMLoadForPPTDevices(tt.lines)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("loader lines = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestAddLoaderPPTDeviceEnsuresVMMForExistingID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loader.conf")
	if err := os.WriteFile(path, []byte("ppt_load=\"NO\"\npptdevs=\"1/2/3\"\n"), 0644); err != nil {
		t.Fatalf("creating loader.conf fixture: %v", err)
	}
	setPassthroughTestDependencies(t, path, nil, nil)

	if err := (&Service{}).addLoaderPPTDevice("1/2/3"); err != nil {
		t.Fatalf("ensuring existing passthrough device failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading loader.conf: %v", err)
	}
	want := "ppt_load=\"NO\"\nvmm_load=\"YES\"\npptdevs=\"1/2/3\"\n"
	if string(data) != want {
		t.Fatalf("loader.conf = %q; want %q", data, want)
	}
}

func TestWriteFileAtomicallyReplacesContentAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loader.conf")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatalf("creating loader.conf fixture: %v", err)
	}

	if err := writeFileAtomically(path, []byte("new\n"), 0640); err != nil {
		t.Fatalf("atomic write failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading replaced loader.conf: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stating replaced loader.conf: %v", err)
	}
	if string(data) != "new\n" || info.Mode().Perm() != 0640 {
		t.Fatalf("content=%q permissions=%#o", string(data), info.Mode().Perm())
	}
}

func TestAddPPTDeviceRequiresImportWhenAlreadyAttached(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.PassedThroughIDs{}, &models.BasicSettings{})
	commandCalls := 0
	setPassthroughTestDependencies(t, filepath.Join(t.TempDir(), "loader.conf"), []pciconf.PCIDevice{{
		Name: "ppt", Domain: 0, Bus: 1, Device: 2, Function: 3,
	}}, func(string, ...string) (string, error) {
		commandCalls++
		return "", nil
	})

	_, err := (&Service{DB: db}).AddPPTDevice("0", "1/2/3")
	if !errors.Is(err, ErrPassthroughDeviceNeedsImport) {
		t.Fatalf("error = %v; want ErrPassthroughDeviceNeedsImport", err)
	}
	if commandCalls != 0 {
		t.Fatalf("devctl called %d times before state preflight", commandCalls)
	}
}

func TestAddPPTDeviceRollsBackDatabaseAndDriverWhenLoaderWriteFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.PassedThroughIDs{}, &models.BasicSettings{})
	var commands []string
	setPassthroughTestDependencies(t, filepath.Join(t.TempDir(), "missing", "loader.conf"), []pciconf.PCIDevice{{
		Name: "xhci", Domain: 0, Bus: 1, Device: 2, Function: 3,
	}}, func(command string, args ...string) (string, error) {
		commands = append(commands, command+" "+strings.Join(args, " "))
		return "", nil
	})

	_, err := (&Service{DB: db}).AddPPTDevice("0", "1/2/3")
	if err == nil {
		t.Fatal("expected loader write failure")
	}

	var count int64
	if db.Model(&models.PassedThroughIDs{}).Count(&count).Error != nil || count != 0 {
		t.Fatalf("passthrough mapping count = %d; want 0", count)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "set driver pci0:1:2:3 ppt") ||
		!strings.Contains(joined, "set driver pci0:1:2:3 xhci") {
		t.Fatalf("missing attach or restore command:\n%s", joined)
	}
}

func TestRemovePPTDeviceStopsWhenVMAssignmentQueryFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.PassedThroughIDs{})
	record := models.PassedThroughIDs{Domain: 0, DeviceID: "1/2/3", OldDriver: "xhci"}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("creating passthrough mapping: %v", err)
	}

	if _, err := (&Service{DB: db}).RemovePPTDevice(uint(record.ID)); err == nil {
		t.Fatal("expected VM assignment query failure")
	}

	var count int64
	if db.Model(&models.PassedThroughIDs{}).Where("id = ?", record.ID).Count(&count).Error != nil || count != 1 {
		t.Fatalf("mapping count after failed preflight = %d; want 1", count)
	}
}
