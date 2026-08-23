// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"

	"gorm.io/gorm"
)

func TestValidateCloudInitConfiguration(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		data        string
		metadata    string
		network     string
		wantErrCode string
	}{
		{name: "empty clears configuration"},
		{name: "data and metadata", data: "#cloud-config\nusers: []", metadata: "instance-id: vm-101"},
		{name: "network only", network: "version: 2\nethernets: {}"},
		{name: "data without metadata", data: "#cloud-config", wantErrCode: "both_data_and_metadata_must_be_provided"},
		{name: "metadata without data", metadata: "instance-id: vm-101", wantErrCode: "both_data_and_metadata_must_be_provided"},
		{name: "invalid user data", data: "key: [", metadata: "instance-id: vm-101", wantErrCode: "invalid_yaml_in_cloud_init_data_or_metadata"},
		{name: "invalid network", network: "network: [", wantErrCode: "invalid_yaml_in_cloud_init_network_config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCloudInitConfiguration(test.data, test.metadata, test.network)
			if test.wantErrCode == "" {
				if err != nil {
					t.Fatalf("expected valid configuration, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErrCode) {
				t.Fatalf("expected %q, got %v", test.wantErrCode, err)
			}
		})
	}

	if !vmHasCloudInitConfiguration(vmModels.VM{CloudInitNetworkConfig: "version: 2"}) {
		t.Fatal("network-only cloud-init configuration was not recognized")
	}
}

func TestPreserveDomainUUIDAcrossFullXMLRebuild(t *testing.T) {
	const domainUUID = "97963f05-b158-4562-a8e3-e1dab5ec6e7e"
	oldXML := `<domain type="bhyve"><name>101</name><uuid>` + domainUUID + `</uuid><memory unit="B">268435456</memory></domain>`
	rebuiltXML := `<domain type="bhyve"><name>101</name><memory unit="B">536870912</memory><devices/></domain>`

	updated, err := preserveDomainUUID(oldXML, rebuiltXML)
	if err != nil {
		t.Fatalf("preserve domain UUID: %v", err)
	}
	if !strings.Contains(updated, `<uuid>`+domainUUID+`</uuid>`) || strings.Count(updated, "<uuid>") != 1 {
		t.Fatalf("rebuilt XML did not preserve one UUID: %s", updated)
	}
	if !strings.Contains(updated, `<memory unit="B">536870912</memory>`) {
		t.Fatalf("rebuilt XML content was not retained: %s", updated)
	}
}

func TestValidateExtraBhyveOptionsBounds(t *testing.T) {
	t.Parallel()

	valid, err := validateExtraBhyveOptions([]string{" -S ", "", "-u"})
	if err != nil {
		t.Fatalf("valid options: %v", err)
	}
	if len(valid) != 2 || valid[0] != "-S" || valid[1] != "-u" {
		t.Fatalf("unexpected normalization: %#v", valid)
	}

	for _, test := range []struct {
		name    string
		options []string
		code    string
	}{
		{name: "nul", options: []string{"-S\x00-u"}, code: "invalid_extra_bhyve_option"},
		{name: "single option too long", options: []string{strings.Repeat("x", maximumExtraBhyveOptionBytes+1)}, code: "extra_bhyve_option_too_long"},
		{name: "too many", options: make([]string, maximumExtraBhyveOptionCount+1), code: "too_many_extra_bhyve_options"},
		{name: "aggregate too large", options: repeatedVMOptions(17, strings.Repeat("x", maximumExtraBhyveOptionBytes)), code: "extra_bhyve_options_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "too many" {
				for index := range test.options {
					test.options[index] = "-S"
				}
			}
			_, err := validateExtraBhyveOptions(test.options)
			if err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("expected %q, got %v", test.code, err)
			}
		})
	}
}

func TestVMOptionMethodsValidateBeforeMutation(t *testing.T) {
	t.Parallel()

	service := &Service{}
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "negative boot order", err: service.ModifyBootOrder(101, false, -1), code: "start_order_must_be_greater_than_or_equal_to_0"},
		{name: "zero shutdown wait", err: service.ModifyShutdownWaitTime(101, 0), code: "shutdown_wait_time_out_of_range"},
		{name: "large shutdown wait", err: service.ModifyShutdownWaitTime(101, maximumShutdownWaitTimeSeconds+1), code: "shutdown_wait_time_out_of_range"},
		{name: "invalid clock", err: service.ModifyClock(101, "host"), code: "invalid_time_offset"},
		{name: "invalid boot rom", err: service.ModifyBootROM(101, "bios"), code: "invalid_boot_rom"},
		{name: "partial cloud init", err: service.ModifyCloudInitData(101, "#cloud-config", "", ""), code: "both_data_and_metadata_must_be_provided"},
		{name: "invalid extra option", err: service.ModifyExtraBhyveOptions(101, []string{"-S\x00"}), code: "invalid_extra_bhyve_option"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.err == nil || !strings.Contains(test.err.Error(), test.code) {
				t.Fatalf("expected %q, got %v", test.code, test.err)
			}
		})
	}
}

func repeatedVMOptions(count int, value string) []string {
	options := make([]string, count)
	for index := range options {
		options[index] = value
	}
	return options
}

func TestVMOptionXMLTransformationsAreIdempotentAndPreserveUnrelatedXML(t *testing.T) {
	t.Parallel()

	clockXML, err := updateClockOptionXML(`<domain><name>101</name></domain>`, "localtime")
	if err != nil || !strings.Contains(clockXML, `offset="localtime"`) {
		t.Fatalf("clock update failed: xml=%s err=%v", clockXML, err)
	}

	serialSource := `<domain><devices><disk/><serial type="nmdm"><source master="/dev/nmdm101A" slave="/dev/nmdm101B"/></serial><console type="pty"><source path="/dev/pts/1"/></console></devices></domain>`
	serialXML, err := updateSerialOptionXML(serialSource, 101, true)
	if err != nil {
		t.Fatalf("serial enable: %v", err)
	}
	if strings.Count(serialXML, `/dev/nmdm101A`) != 1 || !strings.Contains(serialXML, `<disk`) || !strings.Contains(serialXML, `/dev/pts/1`) {
		t.Fatalf("serial update duplicated or removed unrelated XML: %s", serialXML)
	}
	serialXML, err = updateSerialOptionXML(serialXML, 101, false)
	if err != nil || strings.Contains(serialXML, `/dev/nmdm101A`) || !strings.Contains(serialXML, `/dev/pts/1`) {
		t.Fatalf("serial disable failed: xml=%s err=%v", serialXML, err)
	}

	umsrSource := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><features><acpi/><msrs unknown="ignore"/></features><bhyve:commandline><bhyve:arg value="-w"/><bhyve:arg value="-S"/></bhyve:commandline></domain>`
	umsrXML, err := updateIgnoreUMSROptionXML(umsrSource, true)
	if err != nil {
		t.Fatalf("UMSR update: %v", err)
	}
	if strings.Count(umsrXML, `<msrs`) != 1 || strings.Contains(umsrXML, `value="-w"`) || !strings.Contains(umsrXML, `value="-S"`) {
		t.Fatalf("UMSR update left duplicates or removed unrelated arguments: %s", umsrXML)
	}

	tpmSource := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><bhyve:commandline><bhyve:arg value="-ltpm,swtpm,/old.sock"/><bhyve:arg value="-S"/></bhyve:commandline></domain>`
	tpmXML, err := updateTPMOptionXML(tpmSource, 101, "/vm/101", true)
	if err != nil {
		t.Fatalf("TPM enable: %v", err)
	}
	if strings.Count(tpmXML, "-ltpm") != 1 || !strings.Contains(tpmXML, "/vm/101/101_tpm.socket") || !strings.Contains(tpmXML, `value="-S"`) {
		t.Fatalf("TPM update left duplicates or removed unrelated arguments: %s", tpmXML)
	}
	tpmXML, err = updateTPMOptionXML(`<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><bhyve:commandline><bhyve:arg value="-ltpm,swtpm,/old.sock"/></bhyve:commandline></domain>`, 101, "/vm/101", false)
	if err != nil || strings.Contains(tpmXML, "commandline") {
		t.Fatalf("TPM disable left an empty commandline: xml=%s err=%v", tpmXML, err)
	}
}

func TestApplyVMOptionDataMutationRollsBackAndRestoresReplica(t *testing.T) {
	t.Parallel()

	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	vm := vmModels.VM{RID: 101, Name: "options-rollback", WoL: false}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	restored := false
	service := &Service{DB: db}
	err := service.applyVMOptionDataMutation(
		vm.RID,
		func(tx *gorm.DB) error {
			return tx.Model(&vm).Update("wo_l", true).Error
		},
		vmOptionDataMutationHooks{
			writeVMJSON: func(tx *gorm.DB, rid uint) error {
				var pending vmModels.VM
				if err := tx.Where("rid = ?", rid).First(&pending).Error; err != nil {
					return err
				}
				if !pending.WoL {
					return errors.New("pending option update was not visible")
				}
				return errors.New("simulated vm.json failure")
			},
			restoreVMJSON: func(uint) error {
				restored = true
				return nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "failed_to_write_vm_json_after_option_update") {
		t.Fatalf("expected strict vm.json failure, got %v", err)
	}
	if !restored {
		t.Fatal("vm.json reconciliation did not run")
	}

	var stored vmModels.VM
	if err := db.Where("rid = ?", vm.RID).First(&stored).Error; err != nil {
		t.Fatalf("reload VM: %v", err)
	}
	if stored.WoL {
		t.Fatal("database option mutation was not rolled back")
	}
}

func TestCreateCloudInitISOClearRemovesStaleArtifacts(t *testing.T) {
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)

	vmPath := filepath.Join(dataPath, "vms", "101")
	cloudInitPath := filepath.Join(vmPath, "cloud-init")
	if err := os.MkdirAll(cloudInitPath, 0o700); err != nil {
		t.Fatalf("create cloud-init path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloudInitPath, "user-data"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write stale user data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "cloud-init.iso"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale ISO: %v", err)
	}

	if err := (&Service{}).CreateCloudInitISO(vmModels.VM{RID: 101}); err != nil {
		t.Fatalf("clear cloud-init artifacts: %v", err)
	}
	for _, path := range []string{cloudInitPath, filepath.Join(vmPath, "cloud-init.iso")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale artifact remains at %s: %v", path, err)
		}
	}
}

func TestCreateCloudInitISONetworkOnlyUsesPrivateArtifacts(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/makefs"); err != nil {
		t.Skipf("makefs unavailable: %v", err)
	}
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)

	vm := vmModels.VM{
		RID:                    102,
		CloudInitNetworkConfig: "version: 2\nethernets: {}\n",
	}
	if err := (&Service{}).CreateCloudInitISO(vm); err != nil {
		t.Fatalf("create network-only cloud-init ISO: %v", err)
	}

	vmPath := filepath.Join(dataPath, "vms", "102")
	for _, path := range []string{
		filepath.Join(vmPath, "cloud-init.iso"),
		filepath.Join(vmPath, "cloud-init", "user-data"),
		filepath.Join(vmPath, "cloud-init", "meta-data"),
		filepath.Join(vmPath, "cloud-init", "network-config"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%#o, want 0600", path, info.Mode().Perm())
		}
	}

	networkConfig, err := os.ReadFile(filepath.Join(vmPath, "cloud-init", "network-config"))
	if err != nil {
		t.Fatalf("read network config: %v", err)
	}
	if string(networkConfig) != vm.CloudInitNetworkConfig {
		t.Fatalf("network config mismatch: %q", networkConfig)
	}
}

func TestGetQemuGuestAgentInfoValidatesServiceAndRID(t *testing.T) {
	t.Parallel()

	if _, err := (*Service)(nil).GetQemuGuestAgentInfo(101); err == nil || !strings.Contains(err.Error(), "db_not_initialized") {
		t.Fatalf("expected nil-service validation, got %v", err)
	}
	if _, err := (&Service{}).GetQemuGuestAgentInfo(0); err == nil || !strings.Contains(err.Error(), "invalid_vm_rid") {
		t.Fatalf("expected RID validation, got %v", err)
	}
	if _, err := (*Service)(nil).RunQemuGuestAgentCommand(101, "guest-ping"); err == nil || !strings.Contains(err.Error(), "db_not_initialized") {
		t.Fatalf("expected command service validation, got %v", err)
	}
}
